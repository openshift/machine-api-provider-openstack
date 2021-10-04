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
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/portsecurity"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/utils/openstack/clientconfig"
	azutils "github.com/gophercloud/utils/openstack/compute/v2/availabilityzones"
	flavorutils "github.com/gophercloud/utils/openstack/compute/v2/flavors"
	imageutils "github.com/gophercloud/utils/openstack/imageservice/v2/images"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	computeClient *gophercloud.ServiceClient
	imagesClient  *gophercloud.ServiceClient

	regionName string
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

	serverClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})
	if err != nil {
		return nil, fmt.Errorf("create serviceClient err: %v", err)
	}

	imagesClient, err := openstack.NewImageServiceV2(provider, gophercloud.EndpointOpts{
		Region: cloud.RegionName,
	})
	if err != nil {
		return nil, fmt.Errorf("create ImageClient err: %v", err)
	}

	return &InstanceService{
		computeClient: serverClient,
		imagesClient:  imagesClient,
	}, nil
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

func (is *InstanceService) GetFlavorInfo(flavorID string) (flavor *flavors.Flavor, err error) {

	info, err := flavors.Get(is.computeClient, flavorID).Extract()
	if err != nil {
		return nil, fmt.Errorf("could not find information for flavor id %s", flavorID)
	}
	return info, nil
}

func (is *InstanceService) GetFlavorID(flavorName string) (string, error) {
	return flavorutils.IDFromName(is.computeClient, flavorName)
}
