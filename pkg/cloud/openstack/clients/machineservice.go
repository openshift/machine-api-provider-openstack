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

package clients

import (
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/common/extensions"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/attachinterfaces"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	netext "github.com/gophercloud/gophercloud/openstack/networking/v2/extensions"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/portsecurity"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/trunks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/utils/openstack/clientconfig"
	azutils "github.com/gophercloud/utils/openstack/compute/v2/availabilityzones"
	flavorutils "github.com/gophercloud/utils/openstack/compute/v2/flavors"
	imageutils "github.com/gophercloud/utils/openstack/imageservice/v2/images"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	CloudsSecretKey = "clouds.yaml"

	TimeoutTrunkDelete       = 3 * time.Minute
	RetryIntervalTrunkDelete = 5 * time.Second

	TimeoutPortDelete       = 3 * time.Minute
	RetryIntervalPortDelete = 5 * time.Second

	// Maximum port name length supported by Neutron
	PortNameMaxSize = 255

	// MachineRegionLabelName as annotation name for a machine region
	MachineRegionLabelName = "machine.openshift.io/region"

	// MachineAZLabelName as annotation name for a machine AZ
	MachineAZLabelName = "machine.openshift.io/zone"

	// MachineInstanceTypeLabelName as annotation name for a machine instance type
	MachineInstanceTypeLabelName = "machine.openshift.io/instance-type"
)

type InstanceService struct {
	provider       *gophercloud.ProviderClient
	computeClient  *gophercloud.ServiceClient
	identityClient *gophercloud.ServiceClient
	networkClient  *gophercloud.ServiceClient
	imagesClient   *gophercloud.ServiceClient
	volumeClient   *gophercloud.ServiceClient

	regionName string
}

type Instance struct {
	servers.Server
}

type ServerNetwork struct {
	networkID    string
	subnetID     string
	portTags     []string
	vnicType     string
	portSecurity *bool
}

// for updating the state of ports with port security
var portWithPortSecurityExtensions struct {
	ports.Port
	portsecurity.PortSecurityExt
}

type InstanceListOpts struct {
	// Name of the image in URL format.
	Image string `q:"image"`

	// Name of the flavor in URL format.
	Flavor string `q:"flavor"`

	// Name of the server as a string; can be queried with regular expressions.
	// Realize that ?name=bob returns both bob and bobb. If you need to match bob
	// only, you can use a regular expression matching the syntax of the
	// underlying database server implemented for Compute.
	Name string `q:"name"`
}

type serverMetadata struct {
	// AZ contains name of the server's availability zone
	AZ string `json:"OS-EXT-AZ:availability_zone"`

	// Flavor refers to a JSON object, which itself indicates the hardware
	// configuration of the deployed server.
	Flavor map[string]interface{} `json:"flavor"`

	// Status contains the current operational status of the server,
	// such as IN_PROGRESS or ACTIVE.
	Status string `json:"status"`
}

func GetCloudFromSecret(kubeClient kubernetes.Interface, namespace string, secretName string, cloudName string) (clientconfig.Cloud, error) {
	emptyCloud := clientconfig.Cloud{}

	if secretName == "" {
		return emptyCloud, nil
	}

	if secretName != "" && cloudName == "" {
		return emptyCloud, fmt.Errorf("Secret name set to %v but no cloud was specified. Please set cloud_name in your machine spec.", secretName)
	}

	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return emptyCloud, fmt.Errorf("Failed to get secrets from kubernetes api: %v", err)
	}

	content, ok := secret.Data[CloudsSecretKey]
	if !ok {
		return emptyCloud, fmt.Errorf("OpenStack credentials secret %v did not contain key %v",
			secretName, CloudsSecretKey)
	}
	var clouds clientconfig.Clouds
	err = yaml.Unmarshal(content, &clouds)
	if err != nil {
		return emptyCloud, fmt.Errorf("failed to unmarshal clouds credentials stored in secret %v: %v", secretName, err)
	}

	return clouds.Clouds[cloudName], nil
}

// TODO: Eventually we'll have a NewInstanceServiceFromCluster too
func NewInstanceServiceFromMachine(kubeClient kubernetes.Interface, machine *machinev1.Machine) (*InstanceService, error) {
	cloud, err := GetCloud(kubeClient, machine)
	if err != nil {
		return nil, err
	}

	return NewInstanceServiceFromCloud(cloud, GetCACertificate(kubeClient))
}

func NewInstanceService() (*InstanceService, error) {
	cloud := clientconfig.Cloud{}
	return NewInstanceServiceFromCloud(cloud, nil)
}

func NewInstanceServiceFromCloud(cloud clientconfig.Cloud, cert []byte) (*InstanceService, error) {
	provider, err := GetProviderClient(cloud, cert)
	if err != nil {
		return nil, err
	}

	identityClient, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{
		Region: "",
	})
	if err != nil {
		return nil, fmt.Errorf("Create identityClient err: %v", err)
	}
	serverClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})

	if err != nil {
		return nil, fmt.Errorf("Create serviceClient err: %v", err)
	}

	networkingClient, err := openstack.NewNetworkV2(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})
	if err != nil {
		return nil, fmt.Errorf("Create networkingClient err: %v", err)
	}

	imagesClient, err := openstack.NewImageServiceV2(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})
	if err != nil {
		return nil, fmt.Errorf("Create ImageClient err: %v", err)
	}

	volumeClient, err := openstack.NewBlockStorageV3(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})
	if err != nil {
		return nil, fmt.Errorf("Create VolumeClient err: %v", err)
	}

	return &InstanceService{
		provider:       provider,
		identityClient: identityClient,
		computeClient:  serverClient,
		networkClient:  networkingClient,
		imagesClient:   imagesClient,
		volumeClient:   volumeClient,
		regionName:     cloud.RegionName,
	}, nil
}

func (is *InstanceService) deleteInstancePorts(id string) error {
	// get instance port id
	allInterfaces, err := attachinterfaces.List(is.computeClient, id).AllPages()
	if err != nil {
		return err
	}
	instanceInterfaces, err := attachinterfaces.ExtractInterfaces(allInterfaces)
	if err != nil {
		return err
	}
	if len(instanceInterfaces) < 1 {
		return servers.Delete(is.computeClient, id).ExtractErr()
	}

	trunkSupport, err := GetTrunkSupport(is)
	if err != nil {
		return fmt.Errorf("Obtaining network extensions err: %v", err)
	}
	// get and delete trunks
	for _, port := range instanceInterfaces {
		err := attachinterfaces.Delete(is.computeClient, id, port.PortID).ExtractErr()
		if err != nil {
			return err
		}
		if trunkSupport {
			listOpts := trunks.ListOpts{
				PortID: port.PortID,
			}
			allTrunks, err := trunks.List(is.networkClient, listOpts).AllPages()
			if err != nil {
				return err
			}
			trunkInfo, err := trunks.ExtractTrunks(allTrunks)
			if err != nil {
				return err
			}
			if len(trunkInfo) == 1 {
				err = util.PollImmediate(RetryIntervalTrunkDelete, TimeoutTrunkDelete, func() (bool, error) {
					err := trunks.Delete(is.networkClient, trunkInfo[0].ID).ExtractErr()
					if err != nil {
						return false, nil
					}
					return true, nil
				})
				if err != nil {
					return fmt.Errorf("Error deleting the trunk %v", trunkInfo[0].ID)
				}
			}
		}

		// delete port
		err = util.PollImmediate(RetryIntervalPortDelete, TimeoutPortDelete, func() (bool, error) {
			err := ports.Delete(is.networkClient, port.PortID).ExtractErr()
			if err != nil {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return fmt.Errorf("Error deleting the port %v", port.PortID)
		}
	}

	return nil
}

func (is *InstanceService) InstanceDelete(id string) error {
	err := is.deleteInstancePorts(id)
	if err != nil {
		klog.Warningf("Couldn't delete all instance %v ports: %v", id, err)
	}

	// delete instance
	return servers.Delete(is.computeClient, id).ExtractErr()
}

func GetTrunkSupport(is *InstanceService) (bool, error) {
	allPages, err := netext.List(is.networkClient).AllPages()
	if err != nil {
		return false, err
	}

	allExts, err := extensions.ExtractExtensions(allPages)
	if err != nil {
		return false, err
	}

	for _, ext := range allExts {
		if ext.Alias == "trunk" {
			return true, nil
		}
	}
	return false, nil
}

func (is *InstanceService) GetInstanceList(opts *InstanceListOpts) ([]*Instance, error) {
	var listOpts servers.ListOpts
	if opts != nil {
		listOpts = servers.ListOpts{
			// Name is a regular expression, so we need to explicitly specify a
			// whole string match. https://bugzilla.redhat.com/show_bug.cgi?id=1747270
			Name: fmt.Sprintf("^%s$", opts.Name),
		}
	} else {
		listOpts = servers.ListOpts{}
	}

	allPages, err := servers.List(is.computeClient, listOpts).AllPages()
	if err != nil {
		return nil, fmt.Errorf("Get service list err: %v", err)
	}
	serverList, err := servers.ExtractServers(allPages)
	if err != nil {
		return nil, fmt.Errorf("Extract services list err: %v", err)
	}
	var instanceList []*Instance
	for _, server := range serverList {
		instanceList = append(instanceList, serverToInstance(&server))
	}
	return instanceList, nil
}

// DoesFlavorExist returns nil if exactly one flavor exists with the given name.
func (is *InstanceService) DoesFlavorExist(flavorName string) error {
	_, err := flavorutils.IDFromName(is.computeClient, flavorName)
	return err
}

// DoesImageExist returns nil if exactly one image exists with the given name.
func (is *InstanceService) DoesImageExist(imageName string) error {
	_, err := imageutils.IDFromName(is.imagesClient, imageName)
	return err
}

// DoesAvailabilityZoneExist return an error if AZ with the given name doesn't exist, and nil otherwise
func (is *InstanceService) DoesAvailabilityZoneExist(azName string) error {
	if azName == "" {
		return nil
	}
	zones, err := azutils.ListAvailableAvailabilityZones(is.computeClient)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		return fmt.Errorf("could not find an available compute availability zone")
	}
	for _, zoneName := range zones {
		if zoneName == azName {
			return nil
		}
	}
	return fmt.Errorf("could not find compute availability zone: %s", azName)
}

func (is *InstanceService) GetInstance(resourceId string) (instance *Instance, err error) {
	if resourceId == "" {
		return nil, fmt.Errorf("ResourceId should be specified to  get detail.")
	}
	server, err := servers.Get(is.computeClient, resourceId).Extract()
	if err != nil {
		return nil, fmt.Errorf("Get server %q detail failed: %v", resourceId, err)
	}
	return serverToInstance(server), err
}

// SetMachineLabels set labels describing the machine
func (is *InstanceService) SetMachineLabels(machine *machinev1.Machine, instanceID string) error {
	if machine.Labels[MachineRegionLabelName] != "" && machine.Labels[MachineAZLabelName] != "" && machine.Labels[MachineInstanceTypeLabelName] != "" {
		return nil
	}

	var sm serverMetadata
	err := servers.Get(is.computeClient, instanceID).ExtractInto(&sm)
	if err != nil {
		return err
	}

	if machine.Labels == nil {
		machine.Labels = make(map[string]string)
	}

	// Set the region
	machine.Labels[MachineRegionLabelName] = is.regionName

	// Set the availability zone
	machine.Labels[MachineAZLabelName] = sm.AZ

	// Set the flavor name
	flavor, err := flavors.Get(is.computeClient, sm.Flavor["id"].(string)).Extract()
	if err != nil {
		return err
	}
	machine.Labels[MachineInstanceTypeLabelName] = flavor.Name

	return nil
}

func (is *InstanceService) GetFlavorInfo(flavorID string) (flavor *flavors.Flavor, err error) {

	info, err := flavors.Get(is.computeClient, flavorID).Extract()
	if err != nil {
		return nil, fmt.Errorf("Could not find information for flavor id %s", flavorID)
	}
	return info, nil
}

func (is *InstanceService) GetFlavorID(flavorName string) (string, error) {
	return flavorutils.IDFromName(is.computeClient, flavorName)
}

func serverToInstance(server *servers.Server) *Instance {
	return &Instance{*server}
}
