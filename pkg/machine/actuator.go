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
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"

	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha7"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
	capoRecorder "sigs.k8s.io/cluster-api-provider-openstack/pkg/record"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/scope"

	"github.com/openshift/machine-api-provider-openstack/pkg/clients"
	"github.com/openshift/machine-api-provider-openstack/pkg/utils"

	configv1 "github.com/openshift/api/config/v1"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	maoMachine "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
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
)

type OpenstackClient struct {
	params        ActuatorParams
	scheme        *runtime.Scheme
	client        client.Client
	eventRecorder record.EventRecorder
}

func NewActuator(params ActuatorParams) (*OpenstackClient, error) {
	capoRecorder.InitFromRecorder(params.EventRecorder)

	return &OpenstackClient{
		params:        params,
		client:        params.Client,
		scheme:        params.Scheme,
		eventRecorder: params.EventRecorder,
	}, nil
}

func (oc *OpenstackClient) getScope(ctx context.Context, machine *machinev1.Machine) (scope.Scope, string, error) {
	log := ctrl.LoggerFrom(ctx)
	log = log.WithValues("machine", machine.Name)
	cloud, err := clients.GetCloud(oc.params.KubeClient, machine)
	if err != nil {
		return nil, "", err
	}
	regionName := cloud.RegionName
	scope, err := scope.NewProviderScope(cloud, clients.GetCACertificate(oc.params.KubeClient), log)
	return scope, regionName, err
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

func getInstanceStatus(scope scope.Scope, machine *machinev1.Machine) (*compute.InstanceStatus, error) {
	computeService, err := compute.NewService(scope)
	if err != nil {
		return nil, err
	}

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

func (oc *OpenstackClient) convertMachineToCapoInstanceSpec(scope scope.Scope, machine *machinev1.Machine) (*compute.InstanceSpec, error) {
	machineSpec, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate MachineSpec object: %v", err)
	}

	clusterInfra, err := oc.params.ConfigClient.Infrastructures().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cluster Infrastructure object: %v", err)
	}

	instanceService, err := clients.NewInstanceServiceFromMachine(oc.params.KubeClient, machine)
	if err != nil {
		return nil, err
	}

	userDataRendered, err := oc.getUserData(machine, machineSpec, oc.params.KubeClient)
	if err != nil {
		return nil, fmt.Errorf("error getting bootstrap for %s: %v", machine.Name, err)
	}

	var ignoreAddressPairs bool = false
	if clusterInfra.Status.PlatformStatus.OpenStack.LoadBalancer != nil && clusterInfra.Status.PlatformStatus.OpenStack.LoadBalancer.Type == configv1.LoadBalancerTypeUserManaged {
		// If the load balancer type is managed by the user, we don't want to create address pairs because the
		// API & Ingress VIPs are not managed by the cluster.
		ignoreAddressPairs = true
	}

	// Convert to CAPO InstanceSpec
	instanceSpec, err := MachineToInstanceSpec(
		machine,
		clusterInfra.Status.PlatformStatus.OpenStack.APIServerInternalIPs,
		clusterInfra.Status.PlatformStatus.OpenStack.IngressIPs,
		userDataRendered, instanceService,
		ignoreAddressPairs,
	)
	if err != nil {
		return nil, err
	}

	return instanceSpec, nil
}

func (oc *OpenstackClient) Create(ctx context.Context, machine *machinev1.Machine) error {
	return oc.reconcile(ctx, machine)
}

func (oc *OpenstackClient) Update(ctx context.Context, machine *machinev1.Machine) error {
	return oc.reconcile(ctx, machine)
}

func (oc *OpenstackClient) reconcile(ctx context.Context, machine *machinev1.Machine) error {
	originalResourceVersion := machine.ResourceVersion

	machineSpec, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return maoMachine.InvalidMachineConfiguration("Cannot unmarshal providerSpec for %s: %v", machine.Name, err)
	}

	scope, regionName, err := oc.getScope(ctx, machine)
	if err != nil {
		return err
	}

	instanceStatus, err := getInstanceStatus(scope, machine)
	if err != nil {
		return err
	}

	// MAO shouldn't have called reconcile if the ProviderID is already set.
	// We check here anyway just in case because we definitely don't want to
	// recreate a deleted machine. If this did happen we would fall through
	// below and MAO will mark the machine failed on the next reconcile when
	// Exists() returns false.
	if instanceStatus == nil && machine.Spec.ProviderID == nil {
		instanceStatus, err = oc.createInstance(ctx, machine, scope)
		if err != nil {
			return err
		}
	}

	if instanceStatus == nil {
		// Instance is still creating.
		return &maoMachine.RequeueAfterError{RequeueAfter: 30 * time.Second}
	}

	if err := oc.setProviderID(ctx, machine, instanceStatus.ID()); err != nil {
		return fmt.Errorf("error setting provider ID for %q: %w", machine.Name, err)
	}

	if err := reconcileFloatingIP(machine, machineSpec, instanceStatus, scope); err != nil {
		return err
	}

	// Apply labels and annotations and patch the machine object
	patch := client.MergeFrom(machine.DeepCopy())
	setMachineLabels(machine, regionName, instanceStatus.AvailabilityZone(), machineSpec.Flavor)
	setMachineAnnotations(machine, instanceStatus)
	if err := oc.client.Patch(ctx, machine, patch); err != nil {
		return err
	}

	// Update machine status and patch the machine status object
	patch = client.MergeFrom(machine.DeepCopy())
	if err := setMachineStatus(machine, instanceStatus); err != nil {
		return err
	}
	if err := oc.client.Status().Patch(ctx, machine, patch); err != nil {
		return err
	}

	// Only record the Updated event if the machine was actually modified
	if machine.ResourceVersion != originalResourceVersion {
		oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Updated", "Updated machine %v", machine.Name)
	}
	return nil
}

func (oc *OpenstackClient) createInstance(ctx context.Context, machine *machinev1.Machine, scope scope.Scope) (*compute.InstanceStatus, error) {
	if err := oc.validateMachine(machine); err != nil {
		return nil, maoMachine.InvalidMachineConfiguration("Machine validation failed: %v", err)
	}

	instanceSpec, err := oc.convertMachineToCapoInstanceSpec(scope, machine)
	if err != nil {
		return nil, err
	}

	computeService, err := compute.NewService(scope)
	if err != nil {
		return nil, err
	}

	var osCluster capov1.OpenStackCluster
	clusterNameWithNamespace := utils.GetClusterNameWithNamespace(machine)
	instanceStatus, err := computeService.CreateInstance(machine, &osCluster, instanceSpec, clusterNameWithNamespace)
	if err != nil {
		return nil, maoMachine.CreateMachine("error creating Openstack instance: %v", err)
	}
	oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Created OpenStack instance %s", instanceStatus.ID())
	return instanceStatus, nil
}

func reconcileFloatingIP(machine *machinev1.Machine, machineSpec *machinev1alpha1.OpenstackProviderSpec, instanceStatus *compute.InstanceStatus, scope scope.Scope) error {
	if machineSpec.FloatingIP == "" {
		return nil
	}

	networkStatus, err := instanceStatus.NetworkStatus()
	if err != nil {
		return err
	}

	// Look for the floating IP on the server
	for _, address := range networkStatus.Addresses() {
		if address.Type == corev1.NodeExternalIP && address.Address == machineSpec.FloatingIP {
			return nil
		}
	}

	networkService, err := networking.NewService(scope)
	if err != nil {
		return err
	}
	var osCluster capov1.OpenStackCluster
	fp, err := networkService.GetOrCreateFloatingIP(machine, &osCluster, utils.GetClusterNameWithNamespace(machine), machineSpec.FloatingIP)
	if err != nil {
		return fmt.Errorf("get floatingIP err: %v", err)
	}
	computeService, err := compute.NewService(scope)
	if err != nil {
		return err
	}
	// XXX(mdbooth): Network isn't set on osCluster, so this won't work
	port, err := computeService.GetManagementPort(&osCluster, instanceStatus)
	if err != nil {
		return fmt.Errorf("get management port err: %v", err)
	}

	err = networkService.AssociateFloatingIP(&osCluster, fp, port.ID)
	if err != nil {
		return fmt.Errorf("associate floatingIP err: %v", err)
	}

	return &maoMachine.RequeueAfterError{RequeueAfter: 5 * time.Second}
}

func (oc *OpenstackClient) Delete(ctx context.Context, machine *machinev1.Machine) error {
	osc, _, err := oc.getScope(ctx, machine)
	if err != nil {
		return err
	}

	instanceStatus, err := getInstanceStatus(osc, machine)
	if err != nil {
		return fmt.Errorf("error getting instance status for %q: %w", machine.Name, err)
	}

	computeService, err := compute.NewService(osc)
	if err != nil {
		return err
	}

	machineSpec, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return err
	}
	// Create a minimal instancespec since we don't want to reparse and reconstruct all the networking info just to delete
	instanceSpec := compute.InstanceSpec{
		Name: machine.Name,
		// Ports are required when deleting a server in the ERROR state: OCPBUGS-33806
		// We only need a list of port names, so apiVIPs and ingressVIPs are unnecessary
		Ports:      createCAPOPorts(machineSpec, nil, nil, true),
		RootVolume: extractRootVolumeFromProviderSpec(machineSpec),
	}

	var osCluster capov1.OpenStackCluster
	err = computeService.DeleteInstance(&osCluster, machine, instanceStatus, &instanceSpec)
	if err != nil {
		return err
	}

	oc.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %v", machine.Name)
	return nil
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

func setMachineAnnotations(machine *machinev1.Machine, instanceStatus *compute.InstanceStatus) {
	const InstanceStatusAnnotationKey = "instance-status"
	const OpenstackIdAnnotationKey = "openstack-resourceId"

	// Former annotation
	// machine.ObjectMeta.Annotations[openstack.OpenstackIPAnnotationKey] = primaryIP

	if machine.Annotations == nil {
		machine.Annotations = make(map[string]string)
	}

	// instance-status was previously used to determine if the object had
	// been changed. It is no longer used.
	if _, ok := machine.Annotations[InstanceStatusAnnotationKey]; ok {
		klog.Infof("Machine %s: Removed legacy instance-status annotation", machine.Name)
		delete(machine.Annotations, InstanceStatusAnnotationKey)
	}

	machine.Annotations[OpenstackIdAnnotationKey] = instanceStatus.ID()
	machine.Annotations[maoMachine.MachineInstanceStateAnnotationName] = string(instanceStatus.State())
}

func setMachineStatus(machine *machinev1.Machine, instanceStatus *compute.InstanceStatus) error {
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
	osc, _, err := oc.getScope(ctx, machine)
	if err != nil {
		return false, err
	}

	instanceStatus, err := getInstanceStatus(osc, machine)
	if err != nil {
		return false, err
	}
	return instanceStatus != nil, nil
}

func (oc *OpenstackClient) validateMachine(machine *machinev1.Machine) error {
	machineSpec, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
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

	// Check that server group exists or values aren't inconsistent
	if machineSpec.ServerGroupID != "" && machineSpec.ServerGroupName != "" {
		serverGroup, err := machineService.GetServerGroupByID(machineSpec.ServerGroupID)
		if err != nil {
			return fmt.Errorf("\nError when looking up server group with ID %s: %v", machineSpec.ServerGroupID, err)
		}
		if serverGroup.Name != machineSpec.ServerGroupName {
			return fmt.Errorf("\nName of a %s server group does not match defined name %s", machineSpec.ServerGroupID, machineSpec.ServerGroupName)
		}
	} else if machineSpec.ServerGroupID != "" {
		_, err := machineService.GetServerGroupByID(machineSpec.ServerGroupID)
		if err != nil {
			return fmt.Errorf("\nError when looking up server group with ID %s: %v", machineSpec.ServerGroupID, err)
		}
	} else if machineSpec.ServerGroupName != "" {
		serverGroups, err := machineService.GetServerGroupsByName(machineSpec.ServerGroupName)
		if err != nil {
			return err
		}
		if len(serverGroups) > 1 {
			return fmt.Errorf("\n%d server groups named %s exist", len(serverGroups), machineSpec.ServerGroupName)
		}
	}

	return nil
}
