package machine

import (
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-openstack/pkg/clients"
	"github.com/openshift/machine-api-provider-openstack/pkg/utils"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha7"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
)

type instanceService interface {
	GetServerGroupsByName(name string) ([]servergroups.ServerGroup, error)
	CreateServerGroup(name string) (*servergroups.ServerGroup, error)
}

// networkParamToCapov1PortOpts Converts a MAPO NetworkParams to an array of CAPO PortOpts
func networkParamToCapov1PortOpts(net *machinev1alpha1.NetworkParam, apiVIPs, ingressVIPs []string, trunk *bool, ignoreAddressPairs bool) []capov1.PortOpts {
	ports := []capov1.PortOpts{}

	addressPairs := []capov1.AddressPair{}
	if !(net.NoAllowedAddressPairs || ignoreAddressPairs) {
		for _, apiVIP := range apiVIPs {
			addressPairs = append(addressPairs, capov1.AddressPair{
				IPAddress: apiVIP,
			})
		}
		for _, ingressVIP := range ingressVIPs {
			addressPairs = append(addressPairs, capov1.AddressPair{
				IPAddress: ingressVIP,
			})
		}
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
		ID:          coalesce(net.UUID, net.Filter.ID),
		Name:        net.Filter.Name,
		Description: net.Filter.Description,
		ProjectID:   coalesce(net.Filter.ProjectID, net.Filter.TenantID),
		Tags:        net.Filter.Tags,
		TagsAny:     net.Filter.TagsAny,
		NotTags:     net.Filter.NotTags,
		NotTagsAny:  net.Filter.NotTagsAny,
	}

	tags := net.PortTags

	if network.ID == "" && (net.Filter == machinev1alpha1.Filter{}) {
		// Case: network is undefined and only has subnets
		// Create a port for each subnet
		for _, subnet := range net.Subnets {
			subnet.Filter.ID = coalesce(subnet.UUID, subnet.Filter.ID)

			fixedIP := []capov1.FixedIP{
				{
					Subnet: &capov1.SubnetFilter{
						Name:            subnet.Filter.Name,
						Description:     subnet.Filter.Description,
						ProjectID:       coalesce(subnet.Filter.ProjectID, subnet.Filter.TenantID),
						IPVersion:       subnet.Filter.IPVersion,
						GatewayIP:       subnet.Filter.GatewayIP,
						CIDR:            subnet.Filter.CIDR,
						IPv6AddressMode: subnet.Filter.IPv6AddressMode,
						IPv6RAMode:      subnet.Filter.IPv6RAMode,
						ID:              subnet.Filter.ID,
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
				Profile:             portProfileToCapov1BindingProfile(net.Profile),
			}

			if len(addressPairs) > 0 {
				port.AllowedAddressPairs = addressPairs
			}

			ports = append(ports, port)

		}
	} else {
		// Case: network and subnet are defined
		// Create a single port with an interface for each subnet
		fixedIPs := make([]capov1.FixedIP, len(net.Subnets))
		for i, subnet := range net.Subnets {
			fixedIPs[i] = capov1.FixedIP{
				Subnet: &capov1.SubnetFilter{
					Name:            subnet.Filter.Name,
					Description:     subnet.Filter.Description,
					ProjectID:       coalesce(subnet.Filter.ProjectID, subnet.Filter.TenantID),
					IPVersion:       subnet.Filter.IPVersion,
					GatewayIP:       subnet.Filter.GatewayIP,
					CIDR:            subnet.Filter.CIDR,
					IPv6AddressMode: subnet.Filter.IPv6AddressMode,
					IPv6RAMode:      subnet.Filter.IPv6RAMode,
					ID:              coalesce(subnet.UUID, subnet.Filter.ID),
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
			DisablePortSecurity: disablePortSecurity,
			VNICType:            net.VNICType,
			FixedIPs:            fixedIPs,
			Tags:                tags,
			Profile:             portProfileToCapov1BindingProfile(net.Profile),
		}

		if len(addressPairs) > 0 {
			port.AllowedAddressPairs = addressPairs
		}
		ports = append(ports, port)
	}

	return ports
}

// portOptsToCapov1PortOpts converts a MAPO PortOpts to a CAPO PortOpts
func portOptsToCapov1PortOpts(port *machinev1alpha1.PortOpts, ignoreAddressPairs bool) capov1.PortOpts {
	var portSecurityGroupParams []machinev1alpha1.SecurityGroupParam
	if port.SecurityGroups != nil {
		portSecurityGroupParams = securityGroupsToSecurityGroupParams(*port.SecurityGroups)
	}
	disablePortSecurity := port.PortSecurity
	if disablePortSecurity != nil {
		ps := !*disablePortSecurity
		disablePortSecurity = &ps
	}
	capoPort := capov1.PortOpts{
		AdminStateUp:         port.AdminStateUp,
		Description:          port.Description,
		DisablePortSecurity:  disablePortSecurity,
		FixedIPs:             make([]capov1.FixedIP, len(port.FixedIPs)),
		MACAddress:           port.MACAddress,
		NameSuffix:           port.NameSuffix,
		Network:              &capov1.NetworkFilter{ID: port.NetworkID},
		Profile:              portProfileToCapov1BindingProfile(port.Profile),
		SecurityGroupFilters: securityGroupParamToCapov1SecurityGroupFilter(portSecurityGroupParams),
		Trunk:                port.Trunk,
		VNICType:             port.VNICType,
	}

	if !ignoreAddressPairs {
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

	return capoPort
}

func extractDefaultTags(machine *machinev1beta1.Machine) []string {
	defaultTags := []string{
		"cluster-api-provider-openstack",
		utils.GetClusterNameWithNamespace(machine),
	}
	return defaultTags
}

func extractImageFromProviderSpec(providerSpec *machinev1alpha1.OpenstackProviderSpec) string {
	if providerSpec.RootVolume != nil {
		// TODO(dulek): Installer does not populate ps.Image when ps.RootVolume is set and will instead
		//              populate ps.RootVolume.SourceUUID. Moreover, according to the ClusterOSImage
		//              option definition this is always the name of the image and never the UUID.
		//              We should allow UUID at some point and this will need an update.
		return providerSpec.RootVolume.SourceUUID
	}
	return providerSpec.Image
}

func extractRootVolumeFromProviderSpec(providerSpec *machinev1alpha1.OpenstackProviderSpec) *capov1.RootVolume {
	if providerSpec.RootVolume == nil {
		return nil
	}

	return &capov1.RootVolume{
		Size:             providerSpec.RootVolume.Size,
		VolumeType:       providerSpec.RootVolume.VolumeType,
		AvailabilityZone: providerSpec.RootVolume.Zone,
	}
}

func securityGroupParamToCapov1SecurityGroupFilter(psSecurityGroups []machinev1alpha1.SecurityGroupParam) []capov1.SecurityGroupFilter {
	securityGroupFilters := make([]capov1.SecurityGroupFilter, len(psSecurityGroups))
	for i, secGrp := range psSecurityGroups {
		securityGroupFilters[i] = capov1.SecurityGroupFilter{
			ID:          secGrp.Filter.ID,
			Name:        secGrp.Filter.Name,
			Description: secGrp.Filter.Description,
			ProjectID:   secGrp.Filter.ProjectID,
			Tags:        secGrp.Filter.Tags,
			TagsAny:     secGrp.Filter.TagsAny,
			NotTags:     secGrp.Filter.NotTags,
			NotTagsAny:  secGrp.Filter.NotTagsAny,
		}
		if secGrp.UUID != "" {
			securityGroupFilters[i].ID = secGrp.UUID
		}
		if secGrp.Name != "" {
			securityGroupFilters[i].Name = secGrp.Name
		}
	}
	return securityGroupFilters
}

func securityGroupsToSecurityGroupParams(securityGroups []string) []machinev1alpha1.SecurityGroupParam {
	securityGroupsParams := make([]machinev1alpha1.SecurityGroupParam, len(securityGroups))
	for i, secGrp := range securityGroups {
		securityGroupsParams[i] = machinev1alpha1.SecurityGroupParam{
			Filter: machinev1alpha1.SecurityGroupFilter{
				ID: secGrp,
			},
		}
	}
	return securityGroupsParams
}

func portProfileToCapov1BindingProfile(portProfile map[string]string) capov1.BindingProfile {
	bindingProfile := capov1.BindingProfile{}
	for k, v := range portProfile {
		if k == "capabilities" {
			if strings.Contains(v, "switchdev") {
				bindingProfile.OVSHWOffload = true
			}
		}
		if k == "trusted" && v == "true" {
			bindingProfile.TrustedVF = true
		}
	}
	return bindingProfile
}

func MachineToInstanceSpec(machine *machinev1beta1.Machine, apiVIPs, ingressVIPs []string, userData string, instanceService instanceService, ignoreAddressPairs bool) (*compute.InstanceSpec, error) {
	ps, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	instanceSpec := compute.InstanceSpec{
		Name:           machine.Name,
		Image:          extractImageFromProviderSpec(ps),
		RootVolume:     extractRootVolumeFromProviderSpec(ps),
		Flavor:         ps.Flavor,
		SSHKeyName:     ps.KeyName,
		UserData:       userData,
		Metadata:       ps.ServerMetadata,
		Tags:           ps.Tags,
		ConfigDrive:    ps.ConfigDrive != nil && *ps.ConfigDrive,
		FailureDomain:  ps.AvailabilityZone,
		ServerGroupID:  ps.ServerGroupID,
		Trunk:          ps.Trunk,
		Ports:          createCAPOPorts(ps, apiVIPs, ingressVIPs, ignoreAddressPairs),
		SecurityGroups: securityGroupParamToCapov1SecurityGroupFilter(ps.SecurityGroups),
	}

	instanceSpec.Tags = append(instanceSpec.Tags, extractDefaultTags(machine)...)

	if ps.AdditionalBlockDevices != nil {
		var capoBDType capov1.BlockDeviceType
		var emptyStorage machinev1alpha1.BlockDeviceStorage
		instanceSpec.AdditionalBlockDevices = make([]capov1.AdditionalBlockDevice, len(ps.AdditionalBlockDevices))
		for i, blockDevice := range ps.AdditionalBlockDevices {
			if blockDevice.Storage == emptyStorage {
				return nil, fmt.Errorf("missing storage for additional block device")
			}
			if blockDevice.Storage.Type == machinev1alpha1.LocalBlockDevice {
				capoBDType = capov1.LocalBlockDevice
			} else if blockDevice.Storage.Type == machinev1alpha1.VolumeBlockDevice {
				capoBDType = capov1.VolumeBlockDevice
			} else {
				return nil, fmt.Errorf("unknown block device type: %s", blockDevice.Storage.Type)
			}
			instanceSpec.AdditionalBlockDevices[i] = capov1.AdditionalBlockDevice{
				Name:    blockDevice.Name,
				SizeGiB: blockDevice.SizeGiB,
				Storage: capov1.BlockDeviceStorage{Type: capoBDType},
			}
			if blockDevice.Storage.Volume != nil {
				instanceSpec.AdditionalBlockDevices[i].Storage.Volume = &capov1.BlockDeviceVolume{
					AvailabilityZone: blockDevice.Storage.Volume.AvailabilityZone,
					Type:             blockDevice.Storage.Volume.Type,
				}
			}
		}
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
				return nil, fmt.Errorf("error when creating a server group: %v", err)
			}
			instanceSpec.ServerGroupID = serverGroup.ID
		} else {
			return nil, fmt.Errorf("more than one server group of name %s exists", ps.ServerGroupName)
		}
	}

	return &instanceSpec, nil
}

func createCAPOPorts(ps *machinev1alpha1.OpenstackProviderSpec, apiVIPs, ingressVIPs []string, ignoreAddressPairs bool) []capov1.PortOpts {
	capoPorts := make([]capov1.PortOpts, 0, len(ps.Networks)+len(ps.Ports))

	// The order of the networks is important, first network is the one that will be used for kubelet when
	// the legacy cloud provider is used.
	for _, network := range ps.Networks {
		ports := networkParamToCapov1PortOpts(&network, apiVIPs, ingressVIPs, &ps.Trunk, ignoreAddressPairs)
		capoPorts = append(capoPorts, ports...)
	}

	for _, port := range ps.Ports {
		capoPort := portOptsToCapov1PortOpts(&port, ignoreAddressPairs)
		capoPorts = append(capoPorts, capoPort)
	}

	return capoPorts
}

// coalesce returns the first value that is not the empty string, or the empty
// string.
func coalesce(values ...string) string {
	for i := range values {
		if values[i] != "" {
			return values[i]
		}
	}
	return ""
}
