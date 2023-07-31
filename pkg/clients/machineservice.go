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
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/utils/openstack/clientconfig"
	azutils "github.com/gophercloud/utils/openstack/compute/v2/availabilityzones"
	flavorutils "github.com/gophercloud/utils/openstack/compute/v2/flavors"
	imageutils "github.com/gophercloud/utils/openstack/imageservice/v2/images"
	machinev1 "github.com/openshift/api/machine/v1beta1"
)

type InstanceService struct {
	computeClient *gophercloud.ServiceClient
	imagesClient  *gophercloud.ServiceClient
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

	computeClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
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
		computeClient: computeClient,
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

func (is *InstanceService) CreateServerGroup(name string) (*servergroups.ServerGroup, error) {
	// Microversion "2.15" is the first that supports "soft"-anti-affinity.
	// Microversions starting from "2.64" accept policies as a string
	// instead of an array.
	defer func(microversion string) {
		is.computeClient.Microversion = microversion
	}(is.computeClient.Microversion)
	is.computeClient.Microversion = "2.15"

	return servergroups.Create(is.computeClient, &servergroups.CreateOpts{
		Name:     name,
		Policies: []string{"soft-anti-affinity"},
	}).Extract()
}

func (is *InstanceService) GetServerGroupsByName(name string) ([]servergroups.ServerGroup, error) {
	pages, err := servergroups.List(is.computeClient, servergroups.ListOpts{}).AllPages()
	if err != nil {
		return nil, err
	}

	allServerGroups, err := servergroups.ExtractServerGroups(pages)
	if err != nil {
		return nil, err
	}

	serverGroups := make([]servergroups.ServerGroup, 0, len(allServerGroups))
	for _, serverGroup := range allServerGroups {
		if serverGroup.Name == name {
			serverGroups = append(serverGroups, serverGroup)
		}
	}

	return serverGroups, nil
}

func (is *InstanceService) GetServerGroupByID(id string) (*servergroups.ServerGroup, error) {
	servergroup, err := servergroups.Get(is.computeClient, id).Extract()
	if err != nil {
		return nil, err
	}
	return servergroup, nil
}
