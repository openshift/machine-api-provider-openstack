/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machine

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"

	openstackconfigv1 "github.com/openshift/machine-api-provider-openstack/pkg/apis/openstackproviderconfig/v1alpha1"
	"github.com/openshift/machine-api-provider-openstack/pkg/clients"

	machinev1 "github.com/openshift/api/machine/v1beta1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	maoMachine "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ActuatorParams holds parameter information for Actuator
type ActuatorParams struct {
	KubeClient    kubernetes.Interface
	Client        client.Client
	ConfigClient  configclient.ConfigV1Interface
	EventRecorder record.EventRecorder
	Scheme        *runtime.Scheme
}

const (
	// The prefix of ProviderID for OpenStack machines
	providerPrefix = "openstack:///"

	// Event Action Constants
	createEventAction = "Create"
	updateEventAction = "Update"
	deleteEventAction = "Delete"
	noEventAction     = ""
)

type OpenstackClient struct {
	params        ActuatorParams
	scheme        *runtime.Scheme
	client        client.Client
	eventRecorder record.EventRecorder
}

func NewActuator(params ActuatorParams) (*OpenstackClient, error) {
	return &OpenstackClient{
		params:        params,
		client:        params.Client,
		scheme:        params.Scheme,
		eventRecorder: params.EventRecorder,
	}, nil
}

func (oc *OpenstackClient) getOpenStackContext(machine *machinev1.Machine) (*openStackContext, error) {
	cloud, err := clients.GetCloud(oc.params.KubeClient, machine)
	if err != nil {
		return nil, err
	}
	provider, err := clients.GetProviderClient(cloud, clients.GetCACertificate(oc.params.KubeClient))
	if err != nil {
		return nil, err
	}
	return &openStackContext{provider, &cloud}, nil
}

func getOSCluster() v1alpha4.OpenStackCluster {
	// TODO(egarcia): if we ever use the cluster object, this will benifit from reading from it
	var clusterSpec openstackconfigv1.OpenstackClusterProviderSpec

	return openstackconfigv1.NewOpenStackCluster(clusterSpec, openstackconfigv1.OpenstackClusterProviderStatus{})
}

func (oc *OpenstackClient) setProviderID(ctx context.Context, machine *machinev1.Machine, instanceID string) error {
	// Don't update existing providerID
	if machine.Spec.ProviderID != nil {
		return nil
	}

	patch := client.MergeFromWithOptions(machine.DeepCopy(), client.MergeFromWithOptimisticLock{})

	providerID := fmt.Sprintf("%s%s", providerPrefix, instanceID)
	machine.Spec.ProviderID = &providerID

	return oc.client.Patch(ctx, machine, patch)
}

func getInstanceStatus(machine *machinev1.Machine, computeService *compute.Service) (*compute.InstanceStatus, error) {
	providerID := machine.Spec.ProviderID
	if providerID == nil {
		return computeService.GetInstanceStatusByName(machine, machine.Name)
	}

	if !strings.HasPrefix(*providerID, providerPrefix) {
		return nil, fmt.Errorf("OpenStack Machine %s has invalid provider ID: %s", machine.Name, *providerID)
	}

	instanceID := (*providerID)[len(providerPrefix):]
	return computeService.GetInstanceStatus(instanceID)
}

func (oc *OpenstackClient) Create(ctx context.Context, machine *machinev1.Machine) error {
	providerSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.InvalidMachineConfiguration("Cannot unmarshal providerSpec for %s: %v", machine.Name, err), createEventAction)
	}

	osc, err := oc.getOpenStackContext(machine)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.CreateMachine("%v", err), createEventAction)
	}

	if err := oc.validateMachine(machine); err != nil {
		verr := maoMachine.InvalidMachineConfiguration("Machine validation failed: %v", err)
		return oc.handleMachineError(machine, verr, createEventAction)
	}

	userDataRendered, err := oc.getUserData(machine, providerSpec, oc.params.KubeClient)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.CreateMachine("error creating bootstrap for %s: %v", machine.Name, err), createEventAction)
	}

	// Convert to v1alpha4
	osMachine, err := openstackconfigv1.NewOpenStackMachine(machine)
	if err != nil {
		return err
	}
	osCluster := getOSCluster()

	// XXX(mdbooth): v1Machine is also used to set security group based on IsControlPlaneMachine
	v1Machine := clusterv1.Machine{}
	v1Machine.Spec.FailureDomain = &providerSpec.AvailabilityZone

	computeService, err := osc.getComputeService()
	if err != nil {
		return err
	}

	clusterNameWithNamespace := getClusterNameWithNamespace(machine)
	instanceStatus, err := computeService.CreateInstance(&osCluster, &v1Machine, osMachine, clusterNameWithNamespace, userDataRendered)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.CreateMachine(
			"error creating Openstack instance: %v", err), createEventAction)
	}
	oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Created OpenStack instance %s", instanceStatus.ID())

	// Atomically set the providerID. Although this should not happen unless
	// our deployment is broken somehow, we guard against a potential race
	// with another Create() by patching with an optimistic lock. This will
	// cause the update to fail if some other process updated the machine
	// since we were called. Consequently, to safeguard against a resource
	// leak we need to immediately delete the instance we just created if we
	// failed to set providerID.
	if err := oc.setProviderID(ctx, machine, instanceStatus.ID()); err != nil {
		oc.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "CreateConflict",
			"Deleting OpenStack instance %s due to concurrent update", instanceStatus.ID())

		msg := fmt.Sprintf("error setting provider ID: %v", err)
		if cleanupErr := computeService.DeleteInstance(machine, instanceStatus); cleanupErr != nil {
			msg = fmt.Sprintf("error deleting OpenStack instance %s: %v; original error %s",
				instanceStatus.ID(), cleanupErr, msg)
		}
		return oc.handleMachineError(machine, maoMachine.CreateMachine(msg), createEventAction)
	}

	if err := oc.updateMachine(ctx, machine, osc, providerSpec, instanceStatus, &osCluster); err != nil {
		return oc.handleMachineError(machine, maoMachine.CreateMachine("%v", err), createEventAction)
	}

	return nil
}

func (oc *OpenstackClient) Delete(ctx context.Context, machine *machinev1.Machine) error {
	osc, err := oc.getOpenStackContext(machine)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.DeleteMachine("%v", err), deleteEventAction)
	}

	computeService, err := osc.getComputeService()
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.DeleteMachine("%v", err), deleteEventAction)
	}

	instanceStatus, err := getInstanceStatus(machine, computeService)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.DeleteMachine(
			"error getting OpenStack instance: %v", err), deleteEventAction)
	}
	if instanceStatus == nil {
		klog.Infof("Skipped deleting %s that is already deleted.\n", machine.Name)
		return nil
	}

	osCluster := getOSCluster()
	if err != nil {
		return err
	}
	err = computeService.DeleteInstance(&osCluster, instanceStatus)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.DeleteMachine(
			"error deleting Openstack instance: %v", err), deleteEventAction)
	}

	oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %v", machine.Name)
	return nil
}

func (oc *OpenstackClient) Update(ctx context.Context, machine *machinev1.Machine) error {
	providerSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.InvalidMachineConfiguration("Cannot unmarshal providerSpec for %s: %v", machine.Name, err), updateEventAction)
	}

	osc, err := oc.getOpenStackContext(machine)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.CreateMachine("%v", err), createEventAction)
	}

	computeService, err := osc.getComputeService()
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.UpdateMachine("%v", err), updateEventAction)
	}

	instanceStatus, err := getInstanceStatus(machine, computeService)
	if err != nil {
		return oc.handleMachineError(machine, maoMachine.UpdateMachine("error getting instance: %v", err), updateEventAction)
	}

	// Upgrade of Machine with no ProviderID set
	if machine.Spec.ProviderID == nil {
		if err = oc.setProviderID(ctx, machine, instanceStatus.ID()); err != nil {
			return oc.handleMachineError(machine, maoMachine.UpdateMachine("error setting provier ID: %v", err), updateEventAction)
		}
	}

	osCluster := getOSCluster()
	if err := oc.updateMachine(ctx, machine, osc, providerSpec, instanceStatus, &osCluster); err != nil {
		return oc.handleMachineError(machine, maoMachine.UpdateMachine("%v", err), updateEventAction)
	}

	oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Updated", "Updated machine %v", machine.Name)
	return nil
}

func (oc *OpenstackClient) updateMachine(ctx context.Context, machine *machinev1.Machine, osc *openStackContext, providerSpec *openstackconfigv1.OpenstackProviderSpec, instanceStatus *compute.InstanceStatus, osCluster *v1alpha4.OpenStackCluster) error {
	if providerSpec.FloatingIP != "" {
		networkStatus, err := instanceStatus.NetworkStatus()
		if err != nil {
			return err
		}
		var addressExists bool
		for _, address := range networkStatus.Addresses() {
			if address.Type == corev1.NodeExternalIP && address.Address == providerSpec.FloatingIP {
				addressExists = true
				break
			}
		}
		if !addressExists {
			networkService, err := osc.getNetworkService()
			if err != nil {
				return err
			}
			fp, err := networkService.GetOrCreateFloatingIP(osCluster, getClusterNameWithNamespace(machine), providerSpec.FloatingIP)
			if err != nil {
				return fmt.Errorf("get floatingIP err: %v", err)
			}
			computeService, err := osc.getComputeService()
			if err != nil {
				return err
			}
			// XXX(mdbooth): Network isn't set on osCluster, so this won't work
			port, err := computeService.GetManagementPort(osCluster, instanceStatus)
			if err != nil {
				return fmt.Errorf("get management port err: %v", err)
			}

			err = networkService.AssociateFloatingIP(osCluster, fp, port.ID)
			if err != nil {
				return fmt.Errorf("associate floatingIP err: %v", err)
			}
		}
	}

	patch := client.MergeFrom(machine.DeepCopy())

	setMachineLabels(machine, osc.cloud.RegionName, instanceStatus.AvailabilityZone(), providerSpec.Flavor)
	if err := updateStatus(machine, instanceStatus); err != nil {
		return err
	}

	return oc.client.Status().Patch(ctx, machine, patch)
}

func setMachineLabels(machine *machinev1.Machine, region, availability_zone, flavor string) {
	// Don't update labels which have already been set
	if machine.Labels[maoMachine.MachineRegionLabelName] != "" && machine.Labels[maoMachine.MachineAZLabelName] != "" && machine.Labels[maoMachine.MachineInstanceTypeLabelName] != "" {
		return
	}

	if machine.Labels == nil {
		machine.Labels = make(map[string]string)
	}

	// Set the region
	machine.Labels[maoMachine.MachineRegionLabelName] = region

	// Set the availability zone
	machine.Labels[maoMachine.MachineAZLabelName] = availability_zone

	// Set the flavor name
	machine.Labels[maoMachine.MachineInstanceTypeLabelName] = flavor
}

func updateStatus(machine *machinev1.Machine, instanceStatus *compute.InstanceStatus) error {
	// TODO: Delete InstanceStatusAnnotationKey if it is set
	//const InstanceStatusAnnotationKey = "instance-status"

	// Former annotations
	// machine.ObjectMeta.Annotations[openstack.OpenstackIdAnnotationKey] = instanceID
	// machine.ObjectMeta.Annotations[openstack.OpenstackIPAnnotationKey] = primaryIP

	networkStatus, err := instanceStatus.NetworkStatus()
	if err != nil {
		return err
	}
	networkAddresses := networkStatus.Addresses()
	networkAddresses = append(networkAddresses, corev1.NodeAddress{
		Type:    corev1.NodeHostName,
		Address: machine.Name,
	})
	networkAddresses = append(networkAddresses, corev1.NodeAddress{
		Type:    corev1.NodeInternalDNS,
		Address: machine.Name,
	})
	machine.Status.Addresses = networkAddresses

	return nil
}

func (oc *OpenstackClient) Exists(ctx context.Context, machine *machinev1.Machine) (bool, error) {
	osc, err := oc.getOpenStackContext(machine)
	if err != nil {
		return false, err
	}

	computeService, err := osc.getComputeService()
	if err != nil {
		return false, err
	}

	instanceStatus, err := getInstanceStatus(machine, computeService)
	if err != nil {
		return false, nil
	}
	return instanceStatus != nil, nil
}

// If the OpenstackClient has a client for updating Machine objects, this will set
// the appropriate reason/message on the Machine.Status. If not, such as during
// cluster installation, it will operate as a no-op. It also returns the
// original error for convenience, so callers can do "return handleMachineError(...)".
func (oc *OpenstackClient) handleMachineError(machine *machinev1.Machine, err *maoMachine.MachineError, eventAction string) error {
	if eventAction != noEventAction {
		oc.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v", err.Reason)
	}
	if oc.client != nil {
		reason := err.Reason
		message := err.Message
		machine.Status.ErrorReason = &reason
		machine.Status.ErrorMessage = &message

		// Set state label to indicate that this machine is broken
		if machine.ObjectMeta.Annotations == nil {
			machine.ObjectMeta.Annotations = make(map[string]string)
		}

		if err := oc.client.Update(context.TODO(), machine); err != nil {
			return fmt.Errorf("unable to update machine status: %v", err)
		}
	}

	klog.Errorf("Machine error %s: %v", machine.Name, err.Message)
	return err
}

func (oc *OpenstackClient) validateMachine(machine *machinev1.Machine) error {
	machineSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return fmt.Errorf("\nError getting the machine spec from the provider spec: %v", err)
	}

	machineService, err := clients.NewInstanceServiceFromMachine(oc.params.KubeClient, machine)
	if err != nil {
		return fmt.Errorf("\nError getting a new instance service from the machine: %v", err)
	}

	// TODO(mfedosin): add more validations here

	// Validate that image exists when not booting from volume
	if machineSpec.RootVolume == nil {
		err = machineService.DoesImageExist(machineSpec.Image)
		if err != nil {
			return err
		}
	}

	// Validate that flavor exists
	err = machineService.DoesFlavorExist(machineSpec.Flavor)
	if err != nil {
		return err
	}

	// Validate that Availability Zone exists
	err = machineService.DoesAvailabilityZoneExist(machineSpec.AvailabilityZone)
	if err != nil {
		return err
	}

	return nil
}
