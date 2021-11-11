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
	"fmt"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/utils/openstack/clientconfig"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
	ctrl "sigs.k8s.io/controller-runtime"
)

type openStackContext struct {
	provider *gophercloud.ProviderClient
	cloud    *clientconfig.Cloud
}

func clientOptsForCloud(cloud *clientconfig.Cloud) *clientconfig.ClientOpts {
	return &clientconfig.ClientOpts{
		AuthInfo:   cloud.AuthInfo,
		RegionName: cloud.RegionName,
	}
}

func (osc *openStackContext) getComputeService() (*compute.Service, error) {
	return compute.NewService(osc.provider, clientOptsForCloud(osc.cloud), ctrl.Log)
}

func (osc *openStackContext) getNetworkService() (*networking.Service, error) {
	return networking.NewService(osc.provider, clientOptsForCloud(osc.cloud), ctrl.Log)
}

func getClusterNameWithNamespace(machine *machinev1.Machine) string {
	clusterName := machine.Labels[machinev1.MachineClusterIDLabel]
	return fmt.Sprintf("%s-%s", machine.Namespace, clusterName)
}
