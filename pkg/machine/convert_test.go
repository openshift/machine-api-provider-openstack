package machine

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha7"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
)

type testSubnetsGetter struct{}

func (testSubnetsGetter) GetSubnetsByFilter(opts subnets.ListOptsBuilder) ([]subnets.Subnet, error) {
	return []subnets.Subnet{{NetworkID: "fakeNetwork"}}, nil
}

func newSubnetsGetter() testSubnetsGetter {
	return testSubnetsGetter{}
}

type testInstanceService struct{}

func (testInstanceService) GetServerGroupsByName(name string) ([]servergroups.ServerGroup, error) {
	return []servergroups.ServerGroup{}, nil
}

func (testInstanceService) CreateServerGroup(name string) (*servergroups.ServerGroup, error) {
	servergroup := servergroups.ServerGroup{
		Name:     "fakeServerGroup",
		Policies: []string{"soft-anti-affinity"},
	}
	return &servergroup, nil
}

func newInstanceService() testInstanceService {
	return testInstanceService{}
}

func newNetworkParam(options ...func(*machinev1alpha1.NetworkParam)) *machinev1alpha1.NetworkParam {
	var n machinev1alpha1.NetworkParam
	for _, apply := range options {
		apply(&n)
	}
	return &n
}

func withNetworkID(networkID string) func(*machinev1alpha1.NetworkParam) {
	return func(networkParam *machinev1alpha1.NetworkParam) {
		networkParam.UUID = networkID
	}
}

func withNetworkProjectID(projectID string) func(*machinev1alpha1.NetworkParam) {
	return func(networkParam *machinev1alpha1.NetworkParam) {
		networkParam.Filter.ProjectID = projectID
	}
}

func withNetworkTenantID(tenantID string) func(*machinev1alpha1.NetworkParam) {
	return func(networkParam *machinev1alpha1.NetworkParam) {
		networkParam.Filter.TenantID = tenantID
	}
}

func withSubnetParam(subnetParam machinev1alpha1.SubnetParam) func(*machinev1alpha1.NetworkParam) {
	return func(networkParam *machinev1alpha1.NetworkParam) {
		networkParam.Subnets = append(networkParam.Subnets, subnetParam)
	}
}

func TestPortProfileToCapov1BindingProfile(t *testing.T) {
	type checkFunc func(*testing.T, capov1.BindingProfile)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasOVSHWOffloadEnabled := func(want bool) checkFunc {
		return func(t *testing.T, bindingProfile capov1.BindingProfile) {
			if have := bindingProfile.OVSHWOffload; want != have {
				t.Errorf("expected bindingProfile to have OVSHWOffload %t, found %t", want, have)
			}
		}
	}
	hasTrustedVFEnabled := func(want bool) checkFunc {
		return func(t *testing.T, bindingProfile capov1.BindingProfile) {
			if have := bindingProfile.TrustedVF; want != have {
				t.Errorf("expected bindingProfile to have TrustedVF %t, found %t", want, have)
			}
		}
	}

	for _, tc := range [...]struct {
		name        string
		portProfile map[string]string
		check       []checkFunc
	}{
		{
			name: "portProfile with no options",
			portProfile: map[string]string{
				"foo": "bar",
			},
			check: that(
				hasOVSHWOffloadEnabled(false),
				hasTrustedVFEnabled(false),
			),
		},
		{
			name: "portProfile with OVSHWOffload enabled",
			portProfile: map[string]string{
				"capabilities": "switchdev",
			},
			check: that(
				hasOVSHWOffloadEnabled(true),
				hasTrustedVFEnabled(false),
			),
		},
		{
			name: "portProfile with TrustedVF enabled",
			portProfile: map[string]string{
				"trusted": "true",
			},
			check: that(
				hasOVSHWOffloadEnabled(false),
				hasTrustedVFEnabled(true),
			),
		},
		{
			name: "portProfile with both options enabled",
			portProfile: map[string]string{
				"capabilities": "switchdev",
				"trusted":      "true",
			},
			check: that(
				hasOVSHWOffloadEnabled(true),
				hasTrustedVFEnabled(true),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindingProfile := portProfileToCapov1BindingProfile(tc.portProfile)
			for _, check := range tc.check {
				check(t, bindingProfile)
			}
		})
	}
}

func TestSecurityGroupParamToCapov1SecurityGroupFilter(t *testing.T) {
	type checkFunc func(*testing.T, []capov1.SecurityGroupFilter)
	type securityGroupFilterCheckFunc func(*testing.T, capov1.SecurityGroupFilter)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasSecurityGroupFilters := func(want int) checkFunc {
		return func(t *testing.T, securityGroupFilters []capov1.SecurityGroupFilter) {
			if have := len(securityGroupFilters); want != have {
				t.Errorf("expected %d securityGroupFilters, found %d", want, have)
			}
		}
	}

	securityGroupFilter := func(i int, fns ...securityGroupFilterCheckFunc) checkFunc {
		return func(t *testing.T, securityGroupFilters []capov1.SecurityGroupFilter) {
			if len(securityGroupFilters) <= i {
				t.Errorf("error checking securityGroupFilter %d: no such securityGroupFilter", i)
				return
			}
			for _, check := range fns {
				check(t, securityGroupFilters[i])
			}
		}
	}

	hasSecurityGroupUUID := func(want string) securityGroupFilterCheckFunc {
		return func(t *testing.T, securityGroupFilter capov1.SecurityGroupFilter) {
			if have := securityGroupFilter.ID; want != have {
				t.Errorf("expected securityGroupFilter to have UUID %q, found %q", want, have)
			}
		}
	}

	for _, tc := range [...]struct {
		name                string
		securityGroupParams []machinev1alpha1.SecurityGroupParam
		check               []checkFunc
	}{
		{
			name: "securityGroupParam with one securityGroup ID",
			securityGroupParams: []machinev1alpha1.SecurityGroupParam{
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03715",
				},
			},
			check: that(
				hasSecurityGroupFilters(1),
				securityGroupFilter(0, hasSecurityGroupUUID("c0f694ff-aabf-479f-8fa2-589696c03715")),
			),
		},
		{
			name: "securityGroupParam with multiple securityGroup IDs",
			securityGroupParams: []machinev1alpha1.SecurityGroupParam{
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03715",
				},
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03716",
				},
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03717",
				},
			},
			check: that(
				hasSecurityGroupFilters(3),
				securityGroupFilter(0, hasSecurityGroupUUID("c0f694ff-aabf-479f-8fa2-589696c03715")),
				securityGroupFilter(1, hasSecurityGroupUUID("c0f694ff-aabf-479f-8fa2-589696c03716")),
				securityGroupFilter(2, hasSecurityGroupUUID("c0f694ff-aabf-479f-8fa2-589696c03717")),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			securityGroupFilters := securityGroupParamToCapov1SecurityGroupFilter(tc.securityGroupParams)
			for _, check := range tc.check {
				check(t, securityGroupFilters)
			}
		})
	}
}

func TestNetworkParamToCapov1PortOpt(t *testing.T) {
	type checkFunc func(*testing.T, []capov1.PortOpts)
	type portCheckFunc func(*testing.T, capov1.PortOpts)
	type fixedIPCheckFunc func(*testing.T, capov1.FixedIP)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasPorts := func(want int) checkFunc {
		return func(t *testing.T, ports []capov1.PortOpts) {
			if have := len(ports); want != have {
				t.Errorf("expected %d ports, found %d", want, have)
			}
		}
	}

	port := func(i int, fns ...portCheckFunc) checkFunc {
		return func(t *testing.T, ports []capov1.PortOpts) {
			if len(ports) <= i {
				t.Errorf("error checking port %d: no such port", i)
				return
			}
			for _, check := range fns {
				check(t, ports[i])
			}
		}
	}
	hasNetworkProjectID := func(want string) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if have := port.Network.ProjectID; want != have {
				t.Errorf("expected port to have ProjectID %q, found %q", want, have)
			}
		}
	}
	hasTags := func(expected ...string) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if want, have := len(expected), len(port.Tags); want != have {
				t.Errorf("expected port to have %d tags, found %d", want, have)
			}
			for _, want := range expected {
				var found bool
				for _, have := range port.Tags {
					if want == have {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected port tags to contain %q, not found", want)
				}
			}
			for _, have := range port.Tags {
				var found bool
				for _, want := range expected {
					if want == have {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("found unexpected tag %q", have)
				}
			}
		}
	}
	hasFixedIPs := func(want int) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if have := len(port.FixedIPs); want != have {
				t.Errorf("expected port to have %d FixedIPs, found %q", want, have)
			}
		}
	}

	fixedIP := func(i int, fns ...fixedIPCheckFunc) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if len(port.FixedIPs) <= i {
				t.Errorf("error checking fixedIP %d: no such fixedIP", i)
				return
			}
			for _, check := range fns {
				check(t, port.FixedIPs[i])
			}
		}
	}
	hasSubnetID := func(want string) fixedIPCheckFunc {
		return func(t *testing.T, fixedIP capov1.FixedIP) {
			if have := fixedIP.Subnet.ID; want != have {
				t.Errorf("expected fixedIP to have Subnet ID %q, found %q", want, have)
			}
		}
	}

	for _, tc := range [...]struct {
		name         string
		networkParam *machinev1alpha1.NetworkParam
		check        []checkFunc
	}{
		{
			name: "networkParam with one network ID",
			networkParam: newNetworkParam(
				withNetworkID("c0f694ff-aabf-479f-8fa2-589696c03715"),
				withNetworkProjectID("05245421-300f-4921-8b92-7a9b87fbe35a"),
			),
			check: that(
				hasPorts(1),
				port(0, hasNetworkProjectID("05245421-300f-4921-8b92-7a9b87fbe35a")),
			),
		},
		{
			name: "networkParam with one network ID, tenantID",
			networkParam: newNetworkParam(
				withNetworkID("c0f694ff-aabf-479f-8fa2-589696c03715"),
				withNetworkTenantID("50557a2a-8d31-43cd-9a2f-d8ccce1493ea"),
			),
			check: that(
				hasPorts(1),
				port(0, hasNetworkProjectID("50557a2a-8d31-43cd-9a2f-d8ccce1493ea")),
			),
		},
		{
			name: "networkParam with multiple subnets",
			networkParam: newNetworkParam(
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-A-UUID", PortTags: []string{"uno"}}),
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-B-UUID", PortTags: []string{"due"}}),
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-C-UUID", PortTags: []string{"tre"}}),
			),
			check: that(
				hasPorts(3),
				port(0, hasFixedIPs(1), fixedIP(0, hasSubnetID("subnet-A-UUID")), hasTags("uno")),
				port(1, hasFixedIPs(1), fixedIP(0, hasSubnetID("subnet-B-UUID")), hasTags("due")),
				port(2, hasFixedIPs(1), fixedIP(0, hasSubnetID("subnet-C-UUID")), hasTags("tre")),
			),
		},
		{
			name: "networkParam with networkID and multiple subnets",
			networkParam: newNetworkParam(
				withNetworkID("network-A-UUID"),
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-A-UUID", PortTags: []string{"uno"}}),
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-B-UUID", PortTags: []string{"due"}}),
				withSubnetParam(machinev1alpha1.SubnetParam{UUID: "subnet-C-UUID", PortTags: []string{"tre"}}),
			),
			check: that(
				hasPorts(1),
				port(0,
					hasFixedIPs(3),
					fixedIP(0, hasSubnetID("subnet-A-UUID")),
					fixedIP(1, hasSubnetID("subnet-B-UUID")),
					fixedIP(2, hasSubnetID("subnet-C-UUID")),
					hasTags("uno", "due", "tre"),
				),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			portOpts := networkParamToCapov1PortOpts(
				tc.networkParam,
				nil,
				nil,
				nil,
				false,
			)
			for _, check := range tc.check {
				check(t, portOpts)
			}
		})
	}
}

func TestPortOptsToCapov1PortOpts(t *testing.T) {
	tests := []struct {
		name               string
		input              machinev1alpha1.PortOpts
		ignoreAddressPairs bool
		expected           capov1.PortOpts
	}{
		{
			name: "minimal port opts",
			input: machinev1alpha1.PortOpts{
				FixedIPs:       nil,
				NetworkID:      "c3127c12-fd96-4ab5-a4e0-dc4a69634f3b",
				PortSecurity:   ptr.To(true),
				Profile:        map[string]string{},
				SecurityGroups: nil,
				Tags:           []string{"foo", "bar"},
				Trunk:          ptr.To(false),
			},
			ignoreAddressPairs: true,
			expected: capov1.PortOpts{
				AdminStateUp:         nil,
				Description:          "",
				DisablePortSecurity:  ptr.To(false),
				FixedIPs:             []capov1.FixedIP{},
				MACAddress:           "",
				NameSuffix:           "",
				Network:              &capov1.NetworkFilter{ID: "c3127c12-fd96-4ab5-a4e0-dc4a69634f3b"},
				Profile:              capov1.BindingProfile{},
				SecurityGroupFilters: []capov1.SecurityGroupFilter{},
				// OCPBUGS-48288: We should be setting tags
				// Tags:                 []string{"foo", "bar"},
				Trunk:    ptr.To(false),
				VNICType: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := portOptsToCapov1PortOpts(&tt.input, tt.ignoreAddressPairs); !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("portOptsToCapov1PortOpts() = %v, want %v", actual, tt.expected)
			}
		})
	}
}

func TestSecurityGroupsToSecurityGroupParams(t *testing.T) {
	tests := []struct {
		name           string
		securityGroups []string
		want           []machinev1alpha1.SecurityGroupParam
	}{
		{
			name:           "empty security groups",
			securityGroups: []string{},
			want:           []machinev1alpha1.SecurityGroupParam{},
		},
		{
			name:           "one security group",
			securityGroups: []string{"sg-1234567890"},
			want: []machinev1alpha1.SecurityGroupParam{
				{
					Filter: machinev1alpha1.SecurityGroupFilter{
						ID: "sg-1234567890",
					},
				},
			},
		},
		{
			name:           "multiple security groups",
			securityGroups: []string{"sg-1234567890", "sg-0987654321"},
			want: []machinev1alpha1.SecurityGroupParam{
				{
					Filter: machinev1alpha1.SecurityGroupFilter{
						ID: "sg-1234567890",
					},
				},
				{
					Filter: machinev1alpha1.SecurityGroupFilter{
						ID: "sg-0987654321",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := securityGroupsToSecurityGroupParams(tt.securityGroups); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("securityGroupsToSecurityGroupParams() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMachineToInstanceSpec(t *testing.T) {
	tests := []struct {
		name         string
		providerSpec *machinev1alpha1.OpenstackProviderSpec
		expected     *compute.InstanceSpec
	}{
		{
			name:         "minimal",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{},
			expected: &compute.InstanceSpec{
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
				Ports:          []capov1.PortOpts{},
				SecurityGroups: []capov1.SecurityGroupFilter{},
			},
		},
		{
			name: "with image",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Image: "92f33707-6e04-4756-b470-6902f01289bb",
			},
			expected: &compute.InstanceSpec{
				Image:          "92f33707-6e04-4756-b470-6902f01289bb",
				Ports:          []capov1.PortOpts{},
				SecurityGroups: []capov1.SecurityGroupFilter{},
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with root volume",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				RootVolume: &machinev1alpha1.RootVolume{
					SourceUUID: "f4dd1746-bba9-4932-be83-1b20d0a5adc9",
					Size:       10,
				},
			},
			expected: &compute.InstanceSpec{
				Image: "f4dd1746-bba9-4932-be83-1b20d0a5adc9",
				Ports: []capov1.PortOpts{},
				RootVolume: &capov1.RootVolume{
					Size:             10,
					VolumeType:       "",
					AvailabilityZone: "",
				},
				SecurityGroups: []capov1.SecurityGroupFilter{},
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bytes, err := json.Marshal(tt.providerSpec)
			if err != nil {
				t.Fatal("Failed to marshal provider spec")
			}

			machine := machinev1beta1.Machine{
				Spec: machinev1beta1.MachineSpec{
					ProviderSpec: machinev1beta1.ProviderSpec{
						Value: &runtime.RawExtension{
							Raw: bytes,
						},
					},
				},
			}
			apiVIPs := []string{}
			ingressVIPs := []string{}
			userData := ""
			instanceService := newInstanceService()
			ignoreAddressPairs := false

			actual, err := MachineToInstanceSpec(
				&machine,
				apiVIPs,
				ingressVIPs,
				userData,
				instanceService,
				ignoreAddressPairs,
			)
			if err != nil {
				t.Fatalf("Expected no error, found one: %v", err)
			}
			if !reflect.DeepEqual(*actual, *tt.expected) {
				t.Errorf("MachineToInstanceSpec() = %#v, want %#v", *actual, *tt.expected)
				if !reflect.DeepEqual(actual.Name, tt.expected.Name) {
					t.Errorf("Mismatched Name, expected %s, got %s", tt.expected.Name, actual.Name)
				}
				if !reflect.DeepEqual(actual.Image, tt.expected.Image) {
					t.Errorf("Mismatched Image, expected %s, got %s", tt.expected.Image, actual.Image)
				}
				if !reflect.DeepEqual(actual.ImageUUID, tt.expected.ImageUUID) {
					t.Errorf("Mismatched ImageUUID, expected %s, got %s", tt.expected.ImageUUID, actual.ImageUUID)
				}
				if !reflect.DeepEqual(actual.Flavor, tt.expected.Flavor) {
					t.Errorf("Mismatched Flavor, expected %s, got %s", tt.expected.Flavor, actual.Flavor)
				}
				if !reflect.DeepEqual(actual.SSHKeyName, tt.expected.SSHKeyName) {
					t.Errorf("Mismatched SSHKeyName, expected %s, got %s", tt.expected.SSHKeyName, actual.SSHKeyName)
				}
				if !reflect.DeepEqual(actual.UserData, tt.expected.UserData) {
					t.Errorf("Mismatched UserData, expected %s, got %s", tt.expected.UserData, actual.UserData)
				}
				if !reflect.DeepEqual(actual.Metadata, tt.expected.Metadata) {
					t.Errorf("Mismatched Metadata, expected %#v, got %#v", tt.expected.Metadata, actual.Metadata)
				}
				if !reflect.DeepEqual(actual.ConfigDrive, tt.expected.ConfigDrive) {
					t.Errorf("Mismatched ConfigDrive, expected %t, got %t", tt.expected.ConfigDrive, actual.ConfigDrive)
				}
				if !reflect.DeepEqual(actual.FailureDomain, tt.expected.FailureDomain) {
					t.Errorf("Mismatched FailureDomain, expected %s, got %s", tt.expected.FailureDomain, actual.FailureDomain)
				}
				if !reflect.DeepEqual(actual.RootVolume, tt.expected.RootVolume) {
					t.Errorf("Mismatched RootVolume, expected %#v, got %#v", tt.expected.RootVolume, actual.RootVolume)
				}
				if !reflect.DeepEqual(actual.AdditionalBlockDevices, tt.expected.AdditionalBlockDevices) {
					t.Errorf("Mismatched AdditionalBlockDevices, expected %#v, got %#v", tt.expected.AdditionalBlockDevices, actual.AdditionalBlockDevices)
				}
				if !reflect.DeepEqual(actual.ServerGroupID, tt.expected.ServerGroupID) {
					t.Errorf("Mismatched ServerGroupID, expected %s, got %s", tt.expected.ServerGroupID, actual.ServerGroupID)
				}
				if !reflect.DeepEqual(actual.Trunk, tt.expected.Trunk) {
					t.Errorf("Mismatched Trunk, expected %t, got %t", tt.expected.Trunk, actual.Trunk)
				}
				if !reflect.DeepEqual(actual.Tags, tt.expected.Tags) {
					t.Errorf("Mismatched Tags, expected %#v, got %#v", tt.expected.Tags, actual.Tags)
				}
				if !reflect.DeepEqual(actual.SecurityGroups, tt.expected.SecurityGroups) {
					t.Errorf("Mismatched SecurityGroups, expected %#v, got %#v", tt.expected.SecurityGroups, actual.SecurityGroups)
				}
				if !reflect.DeepEqual(actual.Ports, tt.expected.Ports) {
					t.Errorf("Mismatched Ports, expected %#v, got %#v", tt.expected.Ports, actual.Ports)
				}
			}
		})
	}
}

func TestExtractImageFromProviderSpec(t *testing.T) {
	t.Run("with a nil root volume", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("unexpected panic: %v", r)
			}
		}()
		if expected, actual := "", extractImageFromProviderSpec(&machinev1alpha1.OpenstackProviderSpec{}); expected != actual {
			t.Errorf("expected image to be %q, got %q", expected, actual)
		}
	})
}

func TestExtractRootVolumeFromProviderSpec(t *testing.T) {
	t.Run("with a nil root volume", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("unexpected panic: %v", r)
			}
		}()
		if expected, actual := (*capov1.RootVolume)(nil), extractRootVolumeFromProviderSpec(&machinev1alpha1.OpenstackProviderSpec{}); expected != actual {
			t.Errorf("expected root volume to be %q, got %q", expected, actual)
		}
	})
}
