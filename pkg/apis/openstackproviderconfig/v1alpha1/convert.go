package v1alpha1

import (
	machinev1 "github.com/openshift/api/machine/v1beta1"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

func (cps OpenstackClusterProviderSpec) toClusterSpec() capov1.OpenStackClusterSpec {
	return capov1.OpenStackClusterSpec{
		NodeCIDR:              cps.NodeCIDR,
		DNSNameservers:        cps.DNSNameservers,
		ExternalNetworkID:     cps.ExternalNetworkID,
		ManagedSecurityGroups: cps.ManagedSecurityGroups,
		Tags:                  cps.Tags,
	}
}

func (cps OpenstackClusterProviderStatus) toClusterStatus() capov1.OpenStackClusterStatus {
	clusterStatus := capov1.OpenStackClusterStatus{Ready: true}

	if cps.Network != nil {
		clusterStatus.Network = &capov1.Network{
			Name: cps.Network.Name,
			ID:   cps.Network.ID,
		}
		if cps.Network.Subnet != nil {
			subnet := cps.Network.Subnet
			clusterStatus.Network.Subnet = &capov1.Subnet{
				Name: subnet.Name,
				ID:   subnet.ID,
				CIDR: subnet.CIDR,
			}
		}
		if cps.Network.Router != nil {
			router := cps.Network.Router
			clusterStatus.Network.Router = &capov1.Router{
				Name: router.Name,
				ID:   router.ID,
			}
		}
	}
	return clusterStatus
}

func NewOpenStackCluster(providerSpec OpenstackClusterProviderSpec, providerStatus OpenstackClusterProviderStatus) capov1.OpenStackCluster {
	return capov1.OpenStackCluster{
		ObjectMeta: providerSpec.ObjectMeta,

		Spec:   providerSpec.toClusterSpec(),
		Status: providerStatus.toClusterStatus(),
	}
}

func (ps OpenstackProviderSpec) toMachineSpec() capov1.OpenStackMachineSpec {
	machineSpec := capov1.OpenStackMachineSpec{
		CloudName:      ps.CloudName,
		Flavor:         ps.Flavor,
		Image:          ps.Image,
		SSHKeyName:     ps.KeyName,
		Networks:       make([]capov1.NetworkParam, len(ps.Networks)),
		Ports:          make([]capov1.PortOpts, len(ps.Ports)),
		FloatingIP:     ps.FloatingIP,
		SecurityGroups: make([]capov1.SecurityGroupParam, len(ps.SecurityGroups)),
		Trunk:          ps.Trunk,
		Tags:           ps.Tags,
		ServerMetadata: ps.ServerMetadata,
		ConfigDrive:    ps.ConfigDrive,
		ServerGroupID:  ps.ServerGroupID,
		IdentityRef: &capov1.OpenStackIdentityReference{
			Kind: "secret",
			Name: ps.CloudsSecret.Name,
		},
	}

	// TODO: close upstream/downstream feature gap: zones
	if ps.RootVolume != nil {
		machineSpec.RootVolume = &capov1.RootVolume{
			SourceType: ps.RootVolume.SourceType,
			SourceUUID: ps.RootVolume.SourceUUID,
			DeviceType: ps.RootVolume.DeviceType,
			Size:       ps.RootVolume.Size,
		}
	}

	for i, secGrp := range ps.SecurityGroups {
		machineSpec.SecurityGroups[i] = capov1.SecurityGroupParam{
			UUID:   secGrp.UUID,
			Name:   secGrp.Name,
			Filter: capov1.SecurityGroupFilter(secGrp.Filter),
		}
	}

	// TODO: close upstream/downstream feature gap: port security
	for i, port := range ps.Ports {
		machineSpec.Ports[i] = capov1.PortOpts{
			NetworkID:           port.NetworkID,
			NameSuffix:          port.NameSuffix,
			Description:         port.Description,
			AdminStateUp:        port.AdminStateUp,
			MACAddress:          port.MACAddress,
			TenantID:            port.TenantID,
			FixedIPs:            make([]capov1.FixedIP, len(port.FixedIPs)),
			ProjectID:           port.ProjectID,
			SecurityGroups:      port.SecurityGroups,
			AllowedAddressPairs: make([]capov1.AddressPair, len(port.AllowedAddressPairs)),
			HostID:              port.HostID,
			VNICType:            port.VNICType,
		}

		for fixedIPindex, fixedIP := range port.FixedIPs {
			machineSpec.Ports[i].FixedIPs[fixedIPindex] = capov1.FixedIP(fixedIP)
		}

		for addrPairIndex, addrPair := range port.AllowedAddressPairs {
			machineSpec.Ports[i].AllowedAddressPairs[addrPairIndex] = capov1.AddressPair(addrPair)
		}
	}

	// TODO: close upstream/downstream feature gap or depricate feature in favor of ports interface: port tags, port security
	for i, network := range ps.Networks {
		machineSpec.Networks[i] = capov1.NetworkParam{
			UUID:    network.UUID,
			FixedIP: network.FixedIp,
			Filter:  capov1.Filter(network.Filter),
			Subnets: make([]capov1.SubnetParam, len(network.Subnets)),
		}
		for subnetIndex, subnet := range network.Subnets {
			machineSpec.Networks[i].Subnets[subnetIndex] = capov1.SubnetParam{
				UUID:   subnet.UUID,
				Filter: capov1.SubnetFilter(subnet.Filter),
			}
		}
	}

	return machineSpec
}

func NewOpenStackMachine(machine *machinev1.Machine) (*capov1.OpenStackMachine, error) {
	providerSpec, err := MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	osMachine := &capov1.OpenStackMachine{
		ObjectMeta: machine.ObjectMeta,
		Spec:       providerSpec.toMachineSpec(),
	}

	// if machine api master label exists, add v1beta control plane label to the node
	// TODO(egarcia): fix the go mods so that we can track cluster-api@main and import this
	if osMachine.ObjectMeta.Labels["machine.openshift.io/cluster-api-machine-role"] == "master" {
		osMachine.ObjectMeta.Labels["cluster.x-k8s.io/control-plane"] = ""
	}

	return osMachine, nil
}
