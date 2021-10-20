package v1alpha1

import (
	"fmt"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"

	v1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	infrav1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
)

func (cps OpenstackClusterProviderSpec) toClusterSpec() infrav1.OpenStackClusterSpec {
	return infrav1.OpenStackClusterSpec{
		NodeCIDR:              cps.NodeCIDR,
		DNSNameservers:        cps.DNSNameservers,
		ExternalNetworkID:     cps.ExternalNetworkID,
		ManagedSecurityGroups: cps.ManagedSecurityGroups,
		Tags:                  cps.Tags,
	}
}

func (cps OpenstackClusterProviderStatus) toClusterStatus() infrav1.OpenStackClusterStatus {
	clusterStatus := infrav1.OpenStackClusterStatus{Ready: true}

	if cps.Network != nil {
		clusterStatus.Network = &infrav1.Network{
			Name: cps.Network.Name,
			ID:   cps.Network.ID,
		}
		if cps.Network.Subnet != nil {
			subnet := cps.Network.Subnet
			clusterStatus.Network.Subnet = &infrav1.Subnet{
				Name: subnet.Name,
				ID:   subnet.ID,
				CIDR: subnet.CIDR,
			}
		}
		if cps.Network.Router != nil {
			router := cps.Network.Router
			clusterStatus.Network.Router = &infrav1.Router{
				Name: router.Name,
				ID:   router.ID,
			}
		}
	}
	return clusterStatus
}

func NewOpenStackCluster(providerSpec OpenstackClusterProviderSpec, providerStatus OpenstackClusterProviderStatus) infrav1.OpenStackCluster {
	return infrav1.OpenStackCluster{
		ObjectMeta: providerSpec.ObjectMeta,

		Spec:   providerSpec.toClusterSpec(),
		Status: providerStatus.toClusterStatus(),
	}
}

func (ps OpenstackProviderSpec) toMachineSpec(networkService *networking.Service, clusterInfra *v1.Infrastructure) infrav1.OpenStackMachineSpec {
	machineSpec := infrav1.OpenStackMachineSpec{
		CloudName:      ps.CloudName,
		Flavor:         ps.Flavor,
		Image:          ps.Image,
		SSHKeyName:     ps.KeyName,
		Ports:          []infrav1.PortOpts{},
		FloatingIP:     ps.FloatingIP,
		SecurityGroups: make([]infrav1.SecurityGroupParam, len(ps.SecurityGroups)),
		Trunk:          ps.Trunk,
		Tags:           ps.Tags,
		ServerMetadata: ps.ServerMetadata,
		ConfigDrive:    ps.ConfigDrive,
		ServerGroupID:  ps.ServerGroupID,
		IdentityRef: &infrav1.OpenStackIdentityReference{
			Kind: "secret",
			Name: ps.CloudsSecret.Name,
		},
	}

	// TODO: close upstream/downstream feature gap: zones
	if ps.RootVolume != nil {
		machineSpec.RootVolume = &infrav1.RootVolume{
			SourceType: ps.RootVolume.SourceType,
			SourceUUID: ps.RootVolume.SourceUUID,
			DeviceType: ps.RootVolume.DeviceType,
			Size:       ps.RootVolume.Size,
		}
	}

	for i, secGrp := range ps.SecurityGroups {
		machineSpec.SecurityGroups[i] = infrav1.SecurityGroupParam{
			UUID:   secGrp.UUID,
			Name:   secGrp.Name,
			Filter: infrav1.SecurityGroupFilter(secGrp.Filter),
		}
	}

	// TODO: close upstream/downstream feature gap: port security
	for _, port := range ps.Ports {
		portOpt := infrav1.PortOpts{
			NetworkID:           port.NetworkID,
			NameSuffix:          port.NameSuffix,
			Description:         port.Description,
			AdminStateUp:        port.AdminStateUp,
			MACAddress:          port.MACAddress,
			TenantID:            port.TenantID,
			FixedIPs:            make([]infrav1.FixedIP, len(port.FixedIPs)),
			ProjectID:           port.ProjectID,
			SecurityGroups:      port.SecurityGroups,
			AllowedAddressPairs: make([]infrav1.AddressPair, len(port.AllowedAddressPairs)),
			HostID:              port.HostID,
			VNICType:            port.VNICType,
		}

		for fixedIPindex, fixedIP := range port.FixedIPs {
			portOpt.FixedIPs[fixedIPindex] = infrav1.FixedIP(fixedIP)
		}

		for addrPairIndex, addrPair := range port.AllowedAddressPairs {
			portOpt.AllowedAddressPairs[addrPairIndex] = infrav1.AddressPair(addrPair)
		}

		machineSpec.Ports = append(machineSpec.Ports, portOpt)
	}

	// TODO: close upstream/downstream feature gap or depricate feature in favor of ports interface: port tags, port security
	for _, network := range ps.Networks {
		ports, err := network.toInfrav1Port(networkService, clusterInfra)
		if err != nil {
			panic(err)
		}

		if ports != nil {
			machineSpec.Ports = append(machineSpec.Ports, ports...)
		}
	}

	return machineSpec
}

// TODO(egarcia): this is an inefficient mess, fix upstream and remove as soon as possible
func (networkParam *NetworkParam) toInfrav1Port(networkService *networking.Service, clusterInfra *v1.Infrastructure) ([]infrav1.PortOpts, error) {
	allowedAddressPairs := []infrav1.AddressPair{}
	if !networkParam.NoAllowedAddressPairs {
		allowedAddressPairs = []infrav1.AddressPair{
			{IPAddress: clusterInfra.Status.PlatformStatus.OpenStack.APIServerInternalIP},
			{IPAddress: clusterInfra.Status.PlatformStatus.OpenStack.NodeDNSIP},
			{IPAddress: clusterInfra.Status.PlatformStatus.OpenStack.IngressIP},
		}
	}

	netOpts := networks.ListOpts(networkParam.Filter)
	netOpts.ID = networkParam.UUID
	networkList, err := networkService.GetNetworksByFilter(netOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch network when converting to port: %v", err)
	}

	networkIDs := map[string]*infrav1.PortOpts{}
	for _, network := range networkList {
		networkIDs[network.ID] = &infrav1.PortOpts{
			NetworkID:           network.ID,
			NameSuffix:          utilrand.String(6),
			VNICType:            networkParam.VNICType,
			AllowedAddressPairs: allowedAddressPairs,
		}

		port := networkIDs[network.ID]

		// Set Port Security
		if networkParam.PortSecurity != nil {
			//TODO(egarcia): there has to be a better way to do this :(
			flag := *networkParam.PortSecurity
			flag = !flag
			port.DisablePortSecurity = &flag
		}

		port.FixedIPs = append(port.FixedIPs, infrav1.FixedIP{
			IPAddress: networkParam.FixedIp,
		})
	}

	for _, subnetParam := range networkParam.Subnets {
		subnetOpts := subnets.ListOpts(subnetParam.Filter)
		subnetOpts.ID = subnetParam.UUID
		subnetOpts.NetworkID = networkParam.UUID
		subnetList, err := networkService.GetSubnetsByFilter(subnetOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch subnet when converting to port: %v", err)
		}

		for _, subnet := range subnetList {
			port, ok := networkIDs[subnet.NetworkID]
			if ok {
				port.FixedIPs = append(port.FixedIPs, infrav1.FixedIP{
					SubnetID: subnet.ID,
				})
				var portSecurity *bool
				if subnetParam.PortSecurity != nil {
					portSecurity = subnetParam.PortSecurity
				} else if networkParam.PortSecurity != nil {
					portSecurity = subnetParam.PortSecurity
				}
				if portSecurity != nil {
					flag := *portSecurity
					flag = !flag
					port.DisablePortSecurity = &flag
				}
			} else {
				return nil, fmt.Errorf("networkParam for network %s contains subnet %s that is not part of network")
			}
		}
	}

	portOptsList := []infrav1.PortOpts{}
	for _, value := range networkIDs {
		portOptsList = append(portOptsList, *value)
	}
	return portOptsList, nil
}

func NewOpenStackMachine(machine *machinev1.Machine, networkService *networking.Service, clusterInfra *v1.Infrastructure) (*infrav1.OpenStackMachine, error) {
	providerSpec, err := MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	osMachine := &infrav1.OpenStackMachine{
		ObjectMeta: machine.ObjectMeta,
		Spec:       providerSpec.toMachineSpec(networkService, clusterInfra),
	}

	// if machine api master label exists, add v1beta control plane label to the node
	// TODO(egarcia): fix the go mods so that we can track cluster-api@main and import this
	if osMachine.ObjectMeta.Labels["machine.openshift.io/cluster-api-machine-role"] == "master" {
		osMachine.ObjectMeta.Labels["cluster.x-k8s.io/control-plane"] = ""
	}

	return osMachine, nil
}
