package v1alpha1

import (
	"fmt"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
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

func (filter SubnetFilter) getNetworkID(networkService *networking.Service) (string, error) {
	listOpts := subnets.ListOpts(filter)
	subnets, err := networkService.GetSubnetsByFilter(listOpts)
	if err != nil {
		return "", err
	}

	if len(subnets) != 1 {
		return "", fmt.Errorf("subnet query must return only 1 subnet")
	}
	return subnets[0].NetworkID, nil
}

// Converts NetworkParams to capov1 portOpts
func (net NetworkParam) toCapov1PortOpt(apiVIP, ingressVIP string, trunk *bool, networkService *networking.Service) ([]capov1.PortOpts, error) {
	ports := []capov1.PortOpts{}
	addressPairs := []capov1.AddressPair{}
	if !net.NoAllowedAddressPairs {
		addressPairs = []capov1.AddressPair{
			{
				IPAddress: apiVIP,
			},
			{
				IPAddress: ingressVIP,
			},
		}
	}

	portSecurity := net.PortSecurity
	if net.PortSecurity != nil {
		ps := *portSecurity
		ps = !ps
		portSecurity = &ps
	}

	network := capov1.NetworkFilter{
		ID:          net.UUID,
		Name:        net.Filter.Name,
		Description: net.Filter.Description,
		ProjectID:   net.Filter.ProjectID,
		Tags:        net.Filter.Tags,
		TagsAny:     net.Filter.TagsAny,
		NotTags:     net.Filter.NotTags,
		NotTagsAny:  net.Filter.NotTagsAny,
	}
	if network.ID == "" {
		network.ID = net.Filter.ID
	}

	tags := net.PortTags

	if network.ID == "" && (net.Filter == Filter{}) {
		// Case: network is undefined and only has subnets
		// Create a port for each subnet
		for _, subnet := range net.Subnets {
			if subnet.Filter.ID == "" {
				subnet.Filter.ID = subnet.UUID
			}

			subnetID := subnet.UUID
			if subnetID == "" {
				subnetID = subnet.Filter.ID
			}

			fixedIP := []capov1.FixedIP{
				{
					Subnet: &capov1.SubnetFilter{
						Name:            subnet.Filter.Name,
						Description:     subnet.Filter.Description,
						ProjectID:       subnet.Filter.ProjectID,
						IPVersion:       subnet.Filter.IPVersion,
						GatewayIP:       subnet.Filter.GatewayIP,
						CIDR:            subnet.Filter.CIDR,
						IPv6AddressMode: subnet.Filter.IPv6AddressMode,
						IPv6RAMode:      subnet.Filter.IPv6RAMode,
						ID:              subnetID,
						Tags:            subnet.Filter.Tags,
						TagsAny:         subnet.Filter.TagsAny,
						NotTags:         subnet.Filter.NotTags,
						NotTagsAny:      subnet.Filter.NotTagsAny,
					},
				},
			}

			portTags := append(tags, subnet.PortTags...)

			port := capov1.PortOpts{
				Network:             &network,
				AllowedAddressPairs: addressPairs,
				Trunk:               trunk,
				DisablePortSecurity: portSecurity,
				VNICType:            net.VNICType,
				FixedIPs:            fixedIP,
				Tags:                portTags,
			}

			// Fetch the UUID of the network subnet is attached to or the conversion will fail
			netID, err := subnet.Filter.getNetworkID(networkService)
			if err != nil {
				return []capov1.PortOpts{}, nil
			}

			port.Network.ID = netID
			ports = append(ports, port)

		}
	} else {
		// Case: network and subnet are defined
		// Create a single port with an interface for each subnet
		fixedIPs := make([]capov1.FixedIP, len(net.Subnets))
		for i, subnet := range net.Subnets {
			id := subnet.UUID
			if id == "" {
				id = subnet.Filter.ID
			}

			fixedIPs[i] = capov1.FixedIP{
				Subnet: &capov1.SubnetFilter{
					Name:            subnet.Filter.Name,
					Description:     subnet.Filter.Description,
					ProjectID:       subnet.Filter.ProjectID,
					IPVersion:       subnet.Filter.IPVersion,
					GatewayIP:       subnet.Filter.GatewayIP,
					CIDR:            subnet.Filter.CIDR,
					IPv6AddressMode: subnet.Filter.IPv6AddressMode,
					IPv6RAMode:      subnet.Filter.IPv6RAMode,
					ID:              id,
					Tags:            subnet.Filter.Tags,
					TagsAny:         subnet.Filter.TagsAny,
					NotTags:         subnet.Filter.NotTags,
					NotTagsAny:      subnet.Filter.NotTagsAny,
				},
			}
			tags = append(tags, subnet.PortTags...)
		}

		port := capov1.PortOpts{
			Network:             &network,
			AllowedAddressPairs: addressPairs,
			Trunk:               trunk,
			DisablePortSecurity: portSecurity,
			VNICType:            net.VNICType,
			FixedIPs:            fixedIPs,
			Tags:                tags,
		}

		ports = append(ports, port)
	}

	return ports, nil
}

func (ps OpenstackProviderSpec) toMachineSpec(apiVIP, ingressVIP string, networkService *networking.Service) (capov1.OpenStackMachineSpec, error) {
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

	for i, port := range ps.Ports {
		machineSpec.Ports[i] = capov1.PortOpts{
			Network:             &capov1.NetworkFilter{ID: port.NetworkID},
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
			machineSpec.Ports[i].FixedIPs[fixedIPindex] = capov1.FixedIP{
				Subnet:    &capov1.SubnetFilter{ID: fixedIP.SubnetID},
				IPAddress: fixedIP.IPAddress,
			}
		}

		for addrPairIndex, addrPair := range port.AllowedAddressPairs {
			machineSpec.Ports[i].AllowedAddressPairs[addrPairIndex] = capov1.AddressPair(addrPair)
		}
	}

	portList := []capov1.PortOpts{}
	for _, network := range ps.Networks {
		ports, err := network.toCapov1PortOpt(apiVIP, ingressVIP, &ps.Trunk, networkService)
		if err != nil {
			return capov1.OpenStackMachineSpec{}, err
		}
		portList = append(portList, ports...)
	}

	machineSpec.Ports = append(machineSpec.Ports, portList...)

	return machineSpec, nil
}

func NewOpenStackMachine(machine *machinev1.Machine, apiVIP, ingressVIP string, networkService *networking.Service) (*capov1.OpenStackMachine, error) {
	providerSpec, err := MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	machineSpec, err := providerSpec.toMachineSpec(apiVIP, ingressVIP, networkService)
	if err != nil {
		return nil, err
	}

	osMachine := &capov1.OpenStackMachine{
		ObjectMeta: machine.ObjectMeta,
		Spec:       machineSpec,
	}

	// if machine api master label exists, add v1beta control plane label to the node
	if osMachine.ObjectMeta.Labels["machine.openshift.io/cluster-api-machine-role"] == "master" {
		osMachine.ObjectMeta.Labels["cluster.x-k8s.io/control-plane"] = ""
	}

	return osMachine, nil
}
