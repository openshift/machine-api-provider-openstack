package v1alpha1

import (
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	infrav1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
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

func (ps OpenstackProviderSpec) toMachineSpec() infrav1.OpenStackMachineSpec {
	machineSpec := infrav1.OpenStackMachineSpec{
		CloudName:      ps.CloudName,
		Flavor:         ps.Flavor,
		Image:          ps.Image,
		SSHKeyName:     ps.KeyName,
		Networks:       make([]infrav1.NetworkParam, len(ps.Networks)),
		Ports:          make([]infrav1.PortOpts, len(ps.Ports)),
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
	for i, port := range ps.Ports {
		machineSpec.Ports[i] = infrav1.PortOpts{
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
			machineSpec.Ports[i].FixedIPs[fixedIPindex] = infrav1.FixedIP(fixedIP)
		}

		for addrPairIndex, addrPair := range port.AllowedAddressPairs {
			machineSpec.Ports[i].AllowedAddressPairs[addrPairIndex] = infrav1.AddressPair(addrPair)
		}
	}

	// TODO: close upstream/downstream feature gap or depricate feature in favor of ports interface: port tags, port security
	for i, network := range ps.Networks {
		machineSpec.Networks[i] = infrav1.NetworkParam{
			UUID:    network.UUID,
			FixedIP: network.FixedIp,
			Filter:  infrav1.Filter(network.Filter),
			Subnets: make([]infrav1.SubnetParam, len(network.Subnets)),
		}
		for subnetIndex, subnet := range network.Subnets {
			machineSpec.Networks[i].Subnets[subnetIndex] = infrav1.SubnetParam{
				UUID:   subnet.UUID,
				Filter: infrav1.SubnetFilter(subnet.Filter),
			}
		}
	}

	return machineSpec
}

func NewOpenStackMachine(machine *machinev1.Machine) (*infrav1.OpenStackMachine, error) {
	providerSpec, err := MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	return &infrav1.OpenStackMachine{
		ObjectMeta: v1.ObjectMeta{
			Name: machine.Name,
		},
		Spec: providerSpec.toMachineSpec(),
	}, nil
}
