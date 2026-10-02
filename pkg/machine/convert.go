package machine

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servergroups"

	configv1 "github.com/openshift/api/config/v1"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-openstack/pkg/clients"
	"github.com/openshift/machine-api-provider-openstack/pkg/utils"

	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/optional"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type instanceService interface {
	GetServerGroupsByName(ctx context.Context, name string) ([]servergroups.ServerGroup, error)
	CreateServerGroup(ctx context.Context, name string) (*servergroups.ServerGroup, error)

	GetFlavorID(ctx context.Context, flavorName string) (string, error)
	GetImageID(ctx context.Context, imageName string) (string, error)
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

	var network *capov1.NetworkParam

	if (net.UUID != "" || net.Filter != machinev1alpha1.Filter{}) {
		network = &capov1.NetworkParam{
			ID: optionalString(coalesce(net.UUID, net.Filter.ID)),
			Filter: &capov1.NetworkFilter{
				Name:        net.Filter.Name,
				Description: net.Filter.Description,
				ProjectID:   coalesce(net.Filter.ProjectID, net.Filter.TenantID),
				FilterByNeutronTags: capov1.FilterByNeutronTags{
					Tags:       toNeutronTags(net.Filter.Tags),
					TagsAny:    toNeutronTags(net.Filter.TagsAny),
					NotTags:    toNeutronTags(net.Filter.NotTags),
					NotTagsAny: toNeutronTags(net.Filter.NotTagsAny),
				},
			},
		}
	}

	tags := net.PortTags

	if network == nil {
		// Case: network is undefined and only has subnets
		// Create a port for each subnet
		for _, subnet := range net.Subnets {
			subnet.Filter.ID = coalesce(subnet.UUID, subnet.Filter.ID)

			fixedIP := []capov1.FixedIP{
				{
					Subnet: &capov1.SubnetParam{
						ID: optionalString(subnet.Filter.ID),
						Filter: &capov1.SubnetFilter{
							Name:            subnet.Filter.Name,
							Description:     subnet.Filter.Description,
							ProjectID:       coalesce(subnet.Filter.ProjectID, subnet.Filter.TenantID),
							IPVersion:       int32(subnet.Filter.IPVersion),
							GatewayIP:       subnet.Filter.GatewayIP,
							CIDR:            subnet.Filter.CIDR,
							IPv6AddressMode: subnet.Filter.IPv6AddressMode,
							IPv6RAMode:      subnet.Filter.IPv6RAMode,
							FilterByNeutronTags: capov1.FilterByNeutronTags{
								Tags:       toNeutronTags(subnet.Filter.Tags),
								TagsAny:    toNeutronTags(subnet.Filter.TagsAny),
								NotTags:    toNeutronTags(subnet.Filter.NotTags),
								NotTagsAny: toNeutronTags(subnet.Filter.NotTagsAny),
							},
						},
					},
				},
			}

			portTags := append(tags, subnet.PortTags...)

			port := capov1.PortOpts{
				Trunk:    trunk,
				FixedIPs: fixedIP,
				Tags:     portTags,

				ResolvedPortSpecFields: capov1.ResolvedPortSpecFields{
					EnablePortSecurity: optional.Bool(net.PortSecurity),
					VNICType:           optionalString(net.VNICType),
					Profile:            portProfileToCapov1BindingProfile(net.Profile),
				},
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
				Subnet: &capov1.SubnetParam{
					ID: optionalString(coalesce(subnet.UUID, subnet.Filter.ID)),
					Filter: &capov1.SubnetFilter{
						Name:            subnet.Filter.Name,
						Description:     subnet.Filter.Description,
						ProjectID:       coalesce(subnet.Filter.ProjectID, subnet.Filter.TenantID),
						IPVersion:       int32(subnet.Filter.IPVersion),
						GatewayIP:       subnet.Filter.GatewayIP,
						CIDR:            subnet.Filter.CIDR,
						IPv6AddressMode: subnet.Filter.IPv6AddressMode,
						IPv6RAMode:      subnet.Filter.IPv6RAMode,
						FilterByNeutronTags: capov1.FilterByNeutronTags{
							Tags:       toNeutronTags(subnet.Filter.Tags),
							TagsAny:    toNeutronTags(subnet.Filter.TagsAny),
							NotTags:    toNeutronTags(subnet.Filter.NotTags),
							NotTagsAny: toNeutronTags(subnet.Filter.NotTagsAny),
						},
					},
				},
			}
			tags = append(tags, subnet.PortTags...)
		}

		port := capov1.PortOpts{
			Network:  network,
			Trunk:    trunk,
			FixedIPs: fixedIPs,
			Tags:     tags,

			ResolvedPortSpecFields: capov1.ResolvedPortSpecFields{
				AllowedAddressPairs: addressPairs,
				EnablePortSecurity:  optional.Bool(net.PortSecurity),
				VNICType:            optionalString(net.VNICType),
				Profile:             portProfileToCapov1BindingProfile(net.Profile),
			},
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

	capoPort := capov1.PortOpts{
		Description:    optionalString(port.Description),
		FixedIPs:       make([]capov1.FixedIP, len(port.FixedIPs)),
		NameSuffix:     optionalString(port.NameSuffix),
		Network:        &capov1.NetworkParam{ID: &port.NetworkID},
		SecurityGroups: securityGroupParamToCapov1SecurityGroupParams(portSecurityGroupParams),
		Tags:           port.Tags,
		Trunk:          port.Trunk,

		ResolvedPortSpecFields: capov1.ResolvedPortSpecFields{
			AdminStateUp:       port.AdminStateUp,
			EnablePortSecurity: optional.Bool(port.PortSecurity),
			MACAddress:         optionalString(port.MACAddress),
			Profile:            portProfileToCapov1BindingProfile(port.Profile),
			VNICType:           optionalString(port.VNICType),
		},
	}

	if !ignoreAddressPairs {
		capoPort.AllowedAddressPairs = make([]capov1.AddressPair, len(port.AllowedAddressPairs))
		for addrPairIndex, addrPair := range port.AllowedAddressPairs {
			capoPort.AllowedAddressPairs[addrPairIndex] = capov1.AddressPair{
				IPAddress:  addrPair.IPAddress,
				MACAddress: &addrPair.MACAddress,
			}
		}
	}

	for fixedIPindex, fixedIP := range port.FixedIPs {
		capoPort.FixedIPs[fixedIPindex] = capov1.FixedIP{
			Subnet:    &capov1.SubnetParam{ID: optionalString(fixedIP.SubnetID)},
			IPAddress: optionalString(fixedIP.IPAddress),
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

func volumeAvailabiblityZone(az string) *capov1.VolumeAvailabilityZone {
	if az != "" {
		return &capov1.VolumeAvailabilityZone{
			From: capov1.VolumeAZFromName,
			Name: new(capov1.VolumeAZName(az)),
		}
	}

	return nil
}

func extractRootVolumeFromProviderSpec(providerSpec *machinev1alpha1.OpenstackProviderSpec) *capov1.RootVolume {
	if providerSpec.RootVolume == nil {
		return nil
	}

	return &capov1.RootVolume{
		SizeGiB: int32(providerSpec.RootVolume.Size),
		BlockDeviceVolume: capov1.BlockDeviceVolume{
			Type:             providerSpec.RootVolume.VolumeType,
			AvailabilityZone: volumeAvailabiblityZone(providerSpec.RootVolume.Zone),
		},
	}
}

func securityGroupParamToCapov1SecurityGroupParams(psSecurityGroups []machinev1alpha1.SecurityGroupParam) []capov1.SecurityGroupParam {
	securityGroupParams := make([]capov1.SecurityGroupParam, len(psSecurityGroups))
	for i, secGrp := range psSecurityGroups {
		securityGroupParams[i] = capov1.SecurityGroupParam{
			ID: optionalString(secGrp.Filter.ID),

			Filter: &capov1.SecurityGroupFilter{
				Name:        secGrp.Filter.Name,
				Description: secGrp.Filter.Description,
				ProjectID:   coalesce(secGrp.Filter.ProjectID, secGrp.Filter.TenantID),
				FilterByNeutronTags: capov1.FilterByNeutronTags{
					Tags:       toNeutronTags(secGrp.Filter.Tags),
					TagsAny:    toNeutronTags(secGrp.Filter.TagsAny),
					NotTags:    toNeutronTags(secGrp.Filter.NotTags),
					NotTagsAny: toNeutronTags(secGrp.Filter.NotTagsAny),
				},
			},
		}
		if secGrp.UUID != "" {
			securityGroupParams[i].ID = optionalString(secGrp.UUID)
		}
		if secGrp.Name != "" {
			securityGroupParams[i].Filter.Name = secGrp.Name
		}
	}
	return securityGroupParams
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

func portProfileToCapov1BindingProfile(portProfile map[string]string) *capov1.BindingProfile {
	bindingProfile := capov1.BindingProfile{}

	for k, v := range portProfile {
		if k == "capabilities" {
			if strings.Contains(v, "switchdev") {
				bindingProfile.OVSHWOffload = new(true)
			}
		}
		if k == "trusted" && v == "true" {
			bindingProfile.TrustedVF = new(true)
		}
	}
	return &bindingProfile
}

func MachineToInstanceSpec(ctx context.Context, machine *machinev1beta1.Machine, apiVIPs, ingressVIPs []string, userData string, instanceService instanceService, ignoreAddressPairs bool) (*compute.InstanceSpec, error) {
	ps, err := clients.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	// Handle cases that image and flavor are provided with name or id.
	var imageID, flavorID string

	imageName := extractImageFromProviderSpec(ps)

	if imageName != "" {
		if uuidRegex.MatchString(imageName) {
			imageID = imageName
		} else {
			imageID, err = instanceService.GetImageID(ctx, imageName)
			if err != nil {
				return nil, fmt.Errorf("get image id for image name %q: %w", imageName, err)
			}
		}
	}

	// Flavor ID can be non-uuid
	if ps.Flavor != "" {
		if uuidRegex.MatchString(ps.Flavor) {
			flavorID = ps.Flavor
		} else {
			flavorID, err = instanceService.GetFlavorID(ctx, ps.Flavor)
			if err != nil {
				isNotFound := func(err error) bool {
					if _, ok := err.(gophercloud.ErrResourceNotFound); ok {
						return true
					}
					if _, ok := err.(*gophercloud.ErrResourceNotFound); ok {
						return true
					}

					return gophercloud.ResponseCodeIs(err, http.StatusNotFound)
				}

				if !isNotFound(err) {
					return nil, fmt.Errorf("get flavor id for flavor name %q: %w", ps.Flavor, err)
				}

				flavorID = ps.Flavor
			}
		}
	}

	instanceSpec := compute.InstanceSpec{
		Name:          machine.Name,
		ImageID:       imageID,
		RootVolume:    extractRootVolumeFromProviderSpec(ps),
		FlavorID:      flavorID,
		SSHKeyName:    ps.KeyName,
		UserData:      userData,
		Metadata:      ps.ServerMetadata,
		Tags:          ps.Tags,
		ConfigDrive:   ps.ConfigDrive != nil && *ps.ConfigDrive,
		FailureDomain: ps.AvailabilityZone,
		ServerGroupID: ps.ServerGroupID,
		Trunk:         ps.Trunk,
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
				SizeGiB: int32(blockDevice.SizeGiB),
				Storage: capov1.BlockDeviceStorage{Type: capoBDType},
			}
			if blockDevice.Storage.Volume != nil {
				instanceSpec.AdditionalBlockDevices[i].Storage.Volume = &capov1.BlockDeviceVolume{
					AvailabilityZone: volumeAvailabiblityZone(blockDevice.Storage.Volume.AvailabilityZone),
					Type:             blockDevice.Storage.Volume.Type,
				}
			}
		}
	}

	if ps.ServerGroupName != "" && ps.ServerGroupID == "" {
		// We assume that all the hard cases are covered by validation so here it's a matter of checking
		// for existence of server group and creating it if it doesn't exist.
		serverGroups, err := instanceService.GetServerGroupsByName(ctx, ps.ServerGroupName)
		if err != nil {
			return nil, err
		}
		if len(serverGroups) == 1 {
			instanceSpec.ServerGroupID = serverGroups[0].ID
		} else if len(serverGroups) == 0 {
			serverGroup, err := instanceService.CreateServerGroup(ctx, ps.ServerGroupName)
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

func createCAPOResolvedPortSpecs(ps *machinev1alpha1.OpenstackProviderSpec, apiVIPs, ingressVIPs []string, ignoreAddressPairs bool, machine *machinev1.Machine, networkService *networking.Service) ([]capov1.ResolvedPortSpec, error) {
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

	return networkService.ConstructPorts(
		capoPorts,
		securityGroupParamToCapov1SecurityGroupParams(ps.SecurityGroups),
		ps.Trunk,
		machine.Labels[machinev1.MachineClusterIDLabel],
		machine.Name,
		nil,
		nil,
		ps.Tags,
	)
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

func toNeutronTags(filterTags string) []capov1.NeutronTag {
	if filterTags == "" {
		return nil
	}
	tags := strings.Split(filterTags, ",")

	neutronTags := make([]capov1.NeutronTag, 0, len(tags))
	for _, s := range tags {
		neutronTags = append(neutronTags, capov1.NeutronTag(s))
	}

	return neutronTags
}

func optionalString(s string) optional.String {
	if s == "" {
		return nil
	}

	return optional.String(new(s))
}

func shouldIgnoreAddressPairs(clusterInfra *configv1.Infrastructure) bool {
	var ignoreAddressPairs bool = false
	if clusterInfra.Status.PlatformStatus.OpenStack.LoadBalancer != nil && clusterInfra.Status.PlatformStatus.OpenStack.LoadBalancer.Type == configv1.LoadBalancerTypeUserManaged {
		// If the load balancer type is managed by the user, we don't want to create address pairs because the
		// API & Ingress VIPs are not managed by the cluster.
		ignoreAddressPairs = true
	}

	return ignoreAddressPairs
}
