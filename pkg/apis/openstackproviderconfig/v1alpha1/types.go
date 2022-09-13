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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OpenstackClusterProviderSpec is the providerSpec for OpenStack in the cluster object
// +k8s:openapi-gen=true
type OpenstackClusterProviderSpec struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// NodeCIDR is the OpenStack Subnet to be created. Cluster actuator will create a
	// network, a subnet with NodeCIDR, and a router connected to this subnet.
	// If you leave this empty, no network will be created.
	NodeCIDR string `json:"nodeCidr,omitempty"`
	// DNSNameservers is the list of nameservers for OpenStack Subnet being created.
	DNSNameservers []string `json:"dnsNameservers,omitempty"`
	// ExternalNetworkID is the ID of an external OpenStack Network. This is necessary
	// to get public internet to the VMs.
	ExternalNetworkID string `json:"externalNetworkId,omitempty"`

	// ManagedSecurityGroups defines that kubernetes manages the OpenStack security groups
	// for now, that means that we'll create two security groups, one allowing SSH
	// and API access from everywhere, and another one that allows all traffic to/from
	// machines belonging to that group. In the future, we could make this more flexible.
	ManagedSecurityGroups bool `json:"managedSecurityGroups"`

	// Tags for all resources in cluster
	Tags []string `json:"tags,omitempty"`

	// Default: True. In case of server tag errors, set to False
	DisableServerTags bool `json:"disableServerTags,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OpenstackClusterProviderStatus contains the status fields
// relevant to OpenStack in the cluster object.
// +k8s:openapi-gen=true
type OpenstackClusterProviderStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Network contains all information about the created OpenStack Network.
	// It includes Subnets and Router.
	Network *Network `json:"network,omitempty"`

	// ControlPlaneSecurityGroups contains all the information about the OpenStack
	// Security Group that needs to be applied to control plane nodes.
	// TODO: Maybe instead of two properties, we add a property to the group?
	ControlPlaneSecurityGroup *SecurityGroup `json:"controlPlaneSecurityGroup,omitempty"`

	// GlobalSecurityGroup contains all the information about the OpenStack Security
	// Group that needs to be applied to all nodes, both control plane and worker nodes.
	GlobalSecurityGroup *SecurityGroup `json:"globalSecurityGroup,omitempty"`
}

// Network represents basic information about the associated OpenStach Neutron Network
type Network struct {
	Name string `json:"name"`
	ID   string `json:"id"`

	Subnet *Subnet `json:"subnet,omitempty"`
	Router *Router `json:"router,omitempty"`
}

// Subnet represents basic information about the associated OpenStack Neutron Subnet
type Subnet struct {
	Name string `json:"name"`
	ID   string `json:"id"`

	CIDR string `json:"cidr"`
}

// Router represents basic information about the associated OpenStack Neutron Router
type Router struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// SecurityGroup represents the basic information of the associated
// OpenStack Neutron Security Group.
type SecurityGroup struct {
	Name  string              `json:"name"`
	ID    string              `json:"id"`
	Rules []SecurityGroupRule `json:"rules"`
}

// SecurityGroupRule represent the basic information of the associated OpenStack
// Security Group Role.
type SecurityGroupRule struct {
	ID              string `json:"name"`
	Direction       string `json:"direction"`
	EtherType       string `json:"etherType"`
	SecurityGroupID string `json:"securityGroupID"`
	PortRangeMin    int    `json:"portRangeMin"`
	PortRangeMax    int    `json:"portRangeMax"`
	Protocol        string `json:"protocol"`
	RemoteGroupID   string `json:"remoteGroupID"`
	RemoteIPPrefix  string `json:"remoteIPPrefix"`
}

func init() {
	SchemeBuilder.Register(&OpenstackClusterProviderSpec{})
	SchemeBuilder.Register(&OpenstackClusterProviderStatus{})
}
