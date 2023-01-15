package machine

import (
	"fmt"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-openstack/pkg/clients"
	"github.com/openshift/machine-api-provider-openstack/pkg/utils"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha5"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
)

// Looks up a subnet in openstack and gets the ID of the network its attached to
func getNetworkID(filter *machinev1alpha1.SubnetFilter, networkService *networking.Service) (string, error) {
	listOpts := subnets.ListOpts{
		Name:            filter.Name,
		Description:     filter.Description,
		TenantID:        filter.TenantID,
		ProjectID:       filter.ProjectID,
		IPVersion:       filter.IPVersion,
		GatewayIP:       filter.GatewayIP,
		CIDR:            filter.CIDR,
		IPv6AddressMode: filter.IPv6AddressMode,
		IPv6RAMode:      filter.IPv6RAMode,
		ID:              filter.ID,
		SubnetPoolID:    filter.SubnetPoolID,
		Tags:            filter.Tags,
		TagsAny:         filter.TagsAny,
		NotTags:         filter.NotTags,
		NotTagsAny:      filter.NotTagsAny,
	}
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
func networkParamToCapov1PortOpt(net *machinev1alpha1.NetworkParam, apiVIP, ingressVIP string, trunk *bool, networkService *networking.Service) ([]capov1.PortOpts, error) {
	ports := []capov1.PortOpts{}

	addressPairs := []capov1.AddressPair{}
	if !net.NoAllowedAddressPairs && apiVIP != "" {
		addressPairs = append(addressPairs, capov1.AddressPair{
			IPAddress: apiVIP,
		})
	}
	if !net.NoAllowedAddressPairs && ingressVIP != "" {
		addressPairs = append(addressPairs, capov1.AddressPair{
			IPAddress: ingressVIP,
		})
	}

	// Flip the value of port security if not nil
	// must preserve 3 use cases:
	//   nil: openstack default
	//   true: set explicitly to true
	//   false: set explicitly to false
	disablePortSecurity := net.PortSecurity
	if net.PortSecurity != nil {
		ps := !*disablePortSecurity
		disablePortSecurity = &ps
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

	if network.ID == "" && (net.Filter == machinev1alpha1.Filter{}) {
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
				Trunk:               trunk,
				DisablePortSecurity: disablePortSecurity,
				VNICType:            net.VNICType,
				FixedIPs:            fixedIP,
				Tags:                portTags,
				Profile:             net.Profile,
			}

			if len(addressPairs) > 0 {
				port.AllowedAddressPairs = addressPairs
			}

			// Fetch the UUID of the network subnet is attached to or the conversion will fail
			// NOTE: limited to returning only 1 result, which deviates from CAPO api
			// but resolves a lot of problems created by the previous api
			netID, err := getNetworkID(&subnet.Filter, networkService)
			if err != nil {
				return []capov1.PortOpts{}, err
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
			Trunk:               trunk,
			DisablePortSecurity: disablePortSecurity,
			VNICType:            net.VNICType,
			FixedIPs:            fixedIPs,
			Tags:                tags,
		}

		if len(addressPairs) > 0 {
			port.AllowedAddressPairs = addressPairs
		}

		ports = append(ports, port)
	}

	return ports, nil
}

func injectDefaultTags(instanceSpec *compute.InstanceSpec, machine *machinev1beta1.Machine) {
	defaultTags := []string{
		"cluster-api-provider-openstack",
		utils.GetClusterNameWithNamespace(machine),
	}
	instanceSpec.Tags = append(instanceSpec.Tags, defaultTags...)
}

func MachineToInstanceSpec(machine *machinev1beta1.Machine, apiVIP, ingressVIP, userData string, networkService *networking.Service, instanceService *clients.InstanceService) (*compute.InstanceSpec, error) {
	ps, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	instanceSpec := compute.InstanceSpec{
		Name:           machine.Name,
		Image:          ps.Image,
		Flavor:         ps.Flavor,
		SSHKeyName:     ps.KeyName,
		UserData:       userData,
		Metadata:       ps.ServerMetadata,
		Tags:           ps.Tags,
		ConfigDrive:    ps.ConfigDrive != nil && *ps.ConfigDrive,
		FailureDomain:  ps.AvailabilityZone,
		ServerGroupID:  ps.ServerGroupID,
		Trunk:          ps.Trunk,
		Ports:          make([]capov1.PortOpts, 0, len(ps.Ports)+len(ps.Networks)),
		SecurityGroups: make([]capov1.SecurityGroupParam, len(ps.SecurityGroups)),
	}

	injectDefaultTags(&instanceSpec, machine)

	if ps.RootVolume != nil {
		instanceSpec.RootVolume = &capov1.RootVolume{
			Size:             ps.RootVolume.Size,
			VolumeType:       ps.RootVolume.VolumeType,
			AvailabilityZone: ps.RootVolume.Zone,
		}

		// TODO(dulek): Installer does not populate ps.Image when ps.RootVolume is set and will instead
		//              populate ps.RootVolume.SourceUUID. Moreover, according to the ClusterOSImage
		//              option definition this is always the name of the image and never the UUID.
		//              We should allow UUID at some point and this will need an update.
		instanceSpec.Image = ps.RootVolume.SourceUUID
	}

	if ps.ServerGroupName != "" && ps.ServerGroupID == "" {
		// We assume that all the hard cases are covered by validation so here it's a matter of checking
		// for existence of server group and creating it if it doesn't exist.
		serverGroups, err := instanceService.GetServerGroupsByName(ps.ServerGroupName)
		if err != nil {
			return nil, err
		}
		if len(serverGroups) == 1 {
			instanceSpec.ServerGroupID = serverGroups[0].ID
		} else if len(serverGroups) == 0 {
			serverGroup, err := instanceService.CreateServerGroup(ps.ServerGroupName)
			if err != nil {
				return nil, fmt.Errorf("Error when creating a server group: %v", err)
			}
			instanceSpec.ServerGroupID = serverGroup.ID
		} else {
			return nil, fmt.Errorf("More than one server group of name %s exists", ps.ServerGroupName)
		}
	}

	for i, secGrp := range ps.SecurityGroups {
		instanceSpec.SecurityGroups[i] = capov1.SecurityGroupParam{
			UUID: secGrp.UUID,
			Name: secGrp.Name,
			Filter: capov1.SecurityGroupFilter{
				ID:          secGrp.Filter.ID,
				Name:        secGrp.Filter.Name,
				Description: secGrp.Filter.Description,
				TenantID:    secGrp.Filter.TenantID,
				ProjectID:   secGrp.Filter.ProjectID,
				Tags:        secGrp.Filter.Tags,
				TagsAny:     secGrp.Filter.TagsAny,
				NotTags:     secGrp.Filter.NotTags,
				NotTagsAny:  secGrp.Filter.NotTagsAny,
			},
		}
	}

	// The order of the networks is important, first network is the one that will be used for kubelet when
	// the legacy cloud provider is used. Once we switch to using CCM by default, the order won't matter.
	for _, network := range ps.Networks {
		ports, err := networkParamToCapov1PortOpt(&network, apiVIP, ingressVIP, &ps.Trunk, networkService)
		if err != nil {
			return nil, err
		}
		instanceSpec.Ports = append(instanceSpec.Ports, ports...)
	}

	for _, port := range ps.Ports {
		capoPort := capov1.PortOpts{
			Network:        &capov1.NetworkFilter{ID: port.NetworkID},
			NameSuffix:     port.NameSuffix,
			Description:    port.Description,
			AdminStateUp:   port.AdminStateUp,
			MACAddress:     port.MACAddress,
			TenantID:       port.TenantID,
			FixedIPs:       make([]capov1.FixedIP, len(port.FixedIPs)),
			ProjectID:      port.ProjectID,
			SecurityGroups: port.SecurityGroups,
			VNICType:       port.VNICType,
			Profile:        port.Profile,
			Trunk:          port.Trunk,
		}

		if len(port.AllowedAddressPairs) > 0 {
			capoPort.AllowedAddressPairs = make([]capov1.AddressPair, len(port.AllowedAddressPairs))
			for addrPairIndex, addrPair := range port.AllowedAddressPairs {
				capoPort.AllowedAddressPairs[addrPairIndex] = capov1.AddressPair(addrPair)
			}
		}

		for fixedIPindex, fixedIP := range port.FixedIPs {
			capoPort.FixedIPs[fixedIPindex] = capov1.FixedIP{
				Subnet:    &capov1.SubnetFilter{ID: fixedIP.SubnetID},
				IPAddress: fixedIP.IPAddress,
			}
		}

		instanceSpec.Ports = append(instanceSpec.Ports, capoPort)
	}

	return &instanceSpec, nil
}
