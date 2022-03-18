/*
Copyright 2021 The Kubernetes Authors.

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
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/utils/openstack/clientconfig"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
	ctrl "sigs.k8s.io/controller-runtime"
)

type openStackContext struct {
	provider       *gophercloud.ProviderClient
	cloud          *clientconfig.Cloud
	computeService *compute.Service
	networkService *networking.Service
}

func clientOptsForCloud(cloud *clientconfig.Cloud) *clientconfig.ClientOpts {
	return &clientconfig.ClientOpts{
		AuthInfo:   cloud.AuthInfo,
		RegionName: cloud.RegionName,
	}
}

func (osc *openStackContext) getComputeService() (*compute.Service, error) {
	if osc.computeService == nil {
		computeService, err := compute.NewService(osc.provider, clientOptsForCloud(osc.cloud), ctrl.Log.WithName("capo-compute"))
		if err != nil {
			return nil, err
		}
		osc.computeService = computeService
	}
	return osc.computeService, nil
}

func (osc *openStackContext) getNetworkService() (*networking.Service, error) {
	if osc.networkService == nil {
		networkService, err := networking.NewService(osc.provider, clientOptsForCloud(osc.cloud), ctrl.Log.WithName("capo-network"))
		if err != nil {
			return nil, err
		}
		osc.networkService = networkService
	}
	return osc.networkService, nil
}
