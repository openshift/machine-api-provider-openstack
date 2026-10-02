package machine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servergroups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/optional"
)

const (
	testFlavorName1 = "m1.xlarge"
	testFlavorName2 = "m1.large"

	testFlavorID1 = "92f33707-6e04-4756-b470-6902f01289bb"
	testFlavorID2 = "1"
)

var testFlavors = map[string]string{
	testFlavorName1: testFlavorID1,
}

const (
	testImageName1 = "test-image-1"
	testImageName2 = "test-image-2"

	testImageID1 = "92f33707-6e04-4756-b470-6902f01289bb"
	testImageID2 = "f4dd1746-bba9-4932-be83-1b20d0a5adc9"
)

var testImages = map[string]string{
	testImageName1: testImageID1,
	testImageName2: testImageID2,
}

type testSubnetsGetter struct{}

func (testSubnetsGetter) GetSubnetsByFilter(opts subnets.ListOptsBuilder) ([]subnets.Subnet, error) {
	return []subnets.Subnet{{NetworkID: "fakeNetwork"}}, nil
}

func newSubnetsGetter() testSubnetsGetter {
	return testSubnetsGetter{}
}

type testInstanceService struct{}

var _ instanceService = &testInstanceService{}

func (testInstanceService) GetServerGroupsByName(ctx context.Context, name string) ([]servergroups.ServerGroup, error) {
	return []servergroups.ServerGroup{}, nil
}

func (testInstanceService) CreateServerGroup(ctx context.Context, name string) (*servergroups.ServerGroup, error) {
	servergroup := servergroups.ServerGroup{
		Name:     "fakeServerGroup",
		Policies: []string{"soft-anti-affinity"},
	}
	return &servergroup, nil
}

func (testInstanceService) GetFlavorID(ctx context.Context, flavorName string) (string, error) {
	f, ok := testFlavors[flavorName]
	if !ok {
		return "", &gophercloud.ErrResourceNotFound{Name: flavorName, ResourceType: "flavor"}
	}

	return f, nil
}

func (testInstanceService) GetImageID(ctx context.Context, imageName string) (string, error) {
	img, ok := testImages[imageName]
	if !ok {
		return "", gophercloud.ErrResourceNotFound{Name: imageName, ResourceType: "image"}
	}

	return img, nil
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
	type checkFunc func(*testing.T, *capov1.BindingProfile)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasOVSHWOffloadEnabled := func(want *bool) checkFunc {
		return func(t *testing.T, bindingProfile *capov1.BindingProfile) {
			assert.Equal(t, want, bindingProfile.OVSHWOffload, "unexpected OVSHWOffload in bindingProfile")
		}
	}
	hasTrustedVFEnabled := func(want *bool) checkFunc {
		return func(t *testing.T, bindingProfile *capov1.BindingProfile) {
			assert.Equal(t, want, bindingProfile.TrustedVF, "unexpected TrustedVF in bindingProfile")
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
				hasOVSHWOffloadEnabled(nil),
				hasTrustedVFEnabled(nil),
			),
		},
		{
			name: "portProfile with OVSHWOffload enabled",
			portProfile: map[string]string{
				"capabilities": "switchdev",
			},
			check: that(
				hasOVSHWOffloadEnabled(new(true)),
				hasTrustedVFEnabled(nil),
			),
		},
		{
			name: "portProfile with TrustedVF enabled",
			portProfile: map[string]string{
				"trusted": "true",
			},
			check: that(
				hasOVSHWOffloadEnabled(nil),
				hasTrustedVFEnabled(new(true)),
			),
		},
		{
			name: "portProfile with both options enabled",
			portProfile: map[string]string{
				"capabilities": "switchdev",
				"trusted":      "true",
			},
			check: that(
				hasOVSHWOffloadEnabled(new(true)),
				hasTrustedVFEnabled(new(true)),
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

func TestSecurityGroupParamToCapov1SecurityGroupParams(t *testing.T) {
	type checkFunc func(*testing.T, []capov1.SecurityGroupParam)
	type securityGroupFilterCheckFunc func(*testing.T, capov1.SecurityGroupParam)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasSecurityGroupFilters := func(want int) checkFunc {
		return func(t *testing.T, securityGroupParams []capov1.SecurityGroupParam) {
			assert.Equal(t, want, len(securityGroupParams), "unexpected count of securityGroupParams")
		}
	}

	securityGroupFilter := func(i int, fns ...securityGroupFilterCheckFunc) checkFunc {
		return func(t *testing.T, securityGroupParams []capov1.SecurityGroupParam) {
			if !assert.Less(t, i, len(securityGroupParams), "error checking securityGroupParams %d: no such securityGroupParams", i) {
				return
			}
			for _, check := range fns {
				check(t, securityGroupParams[i])
			}
		}
	}

	hasSecurityGroupUUID := func(want optional.String) securityGroupFilterCheckFunc {
		return func(t *testing.T, securityGroupParams capov1.SecurityGroupParam) {
			assert.Equal(t, want, securityGroupParams.ID, "expected securityGroupFilter to have UUID")
		}
	}

	hasProjectID := func(want string) securityGroupFilterCheckFunc {
		return func(t *testing.T, securityGroupParam capov1.SecurityGroupParam) {
			if !assert.NotNil(t, securityGroupParam.Filter, "expected securityGroupParam to have filter") {
				return
			}

			assert.Equal(t, want, securityGroupParam.Filter.ProjectID, "expected securityGroupFilter to have project ID")
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
				securityGroupFilter(0, hasSecurityGroupUUID(optionalString("c0f694ff-aabf-479f-8fa2-589696c03715"))),
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
				securityGroupFilter(0, hasSecurityGroupUUID(optionalString("c0f694ff-aabf-479f-8fa2-589696c03715"))),
				securityGroupFilter(1, hasSecurityGroupUUID(optionalString("c0f694ff-aabf-479f-8fa2-589696c03716"))),
				securityGroupFilter(2, hasSecurityGroupUUID(optionalString("c0f694ff-aabf-479f-8fa2-589696c03717"))),
			),
		},
		{
			name: "securityGroupParam with legacy tenantID params",
			securityGroupParams: []machinev1alpha1.SecurityGroupParam{
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03715",
					Filter: machinev1alpha1.SecurityGroupFilter{
						TenantID: "c9cf6e858743443387730c0d53f82407",
					},
				},
				{
					UUID: "c0f694ff-aabf-479f-8fa2-589696c03716",
					Filter: machinev1alpha1.SecurityGroupFilter{
						ProjectID: "832fbb3cc73d4be894d468ef2ef75a4f",
					},
				},
			},
			check: that(
				hasSecurityGroupFilters(2),
				securityGroupFilter(0, hasProjectID("c9cf6e858743443387730c0d53f82407")),
				securityGroupFilter(1, hasProjectID("832fbb3cc73d4be894d468ef2ef75a4f")),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			securityGroupParams := securityGroupParamToCapov1SecurityGroupParams(tc.securityGroupParams)
			for _, check := range tc.check {
				check(t, securityGroupParams)
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
			assert.Equal(t, want, len(ports), "expected ports")
		}
	}

	port := func(i int, fns ...portCheckFunc) checkFunc {
		return func(t *testing.T, ports []capov1.PortOpts) {
			if !assert.Less(t, i, len(ports), "error checking port %d: no such port", i) {
				return
			}
			for _, check := range fns {
				check(t, ports[i])
			}
		}
	}
	hasNetworkProjectID := func(want string) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if !assert.NotNil(t, port.Network.Filter, "expected port to have network filter") {
				return
			}

			assert.Equal(t, want, port.Network.Filter.ProjectID, "expected port to have ProjectID")
		}
	}
	hasTags := func(expected ...string) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			assert.Equal(t, expected, port.Tags, "expected port to have tags")
		}
	}
	hasFixedIPs := func(want int) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			assert.Equal(t, want, len(port.FixedIPs), "expected port to have %d FixedIPs", want)
		}
	}

	fixedIP := func(i int, fns ...fixedIPCheckFunc) portCheckFunc {
		return func(t *testing.T, port capov1.PortOpts) {
			if !assert.Less(t, i, len(port.FixedIPs), "error checking fixedIP %d: no such fixedIP", i) {
				return
			}
			for _, check := range fns {
				check(t, port.FixedIPs[i])
			}
		}
	}
	hasSubnetID := func(want optional.String) fixedIPCheckFunc {
		return func(t *testing.T, fixedIP capov1.FixedIP) {
			assert.Equal(t, want, fixedIP.Subnet.ID, "expected fixedIP to have Subnet ID")
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
				port(0, hasFixedIPs(1), fixedIP(0, hasSubnetID(optionalString("subnet-A-UUID"))), hasTags("uno")),
				port(1, hasFixedIPs(1), fixedIP(0, hasSubnetID(optionalString("subnet-B-UUID"))), hasTags("due")),
				port(2, hasFixedIPs(1), fixedIP(0, hasSubnetID(optionalString("subnet-C-UUID"))), hasTags("tre")),
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
					fixedIP(0, hasSubnetID(optionalString("subnet-A-UUID"))),
					fixedIP(1, hasSubnetID(optionalString("subnet-B-UUID"))),
					fixedIP(2, hasSubnetID(optionalString("subnet-C-UUID"))),
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
				PortSecurity:   new(true),
				Profile:        map[string]string{},
				SecurityGroups: nil,
				Tags:           []string{"foo", "bar"},
				Trunk:          new(false),
			},
			ignoreAddressPairs: true,
			expected: capov1.PortOpts{
				Description: nil,
				FixedIPs:    []capov1.FixedIP{},
				NameSuffix:  nil,
				Network: &capov1.NetworkParam{
					ID: optionalString("c3127c12-fd96-4ab5-a4e0-dc4a69634f3b"),
				},
				SecurityGroups: []capov1.SecurityGroupParam{},
				Tags:           []string{"foo", "bar"},
				Trunk:          new(false),
				ResolvedPortSpecFields: capov1.ResolvedPortSpecFields{
					AdminStateUp:       nil,
					EnablePortSecurity: new(true),
					MACAddress:         nil,
					Profile:            &capov1.BindingProfile{},
					VNICType:           nil,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, portOptsToCapov1PortOpts(&tt.input, tt.ignoreAddressPairs))
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
			assert.Equal(t, tt.want, securityGroupsToSecurityGroupParams(tt.securityGroups))
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
			},
		},
		{
			name: "with image uuid",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Image: testImageID1,
			},
			expected: &compute.InstanceSpec{
				ImageID: testImageID1,
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with image name",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Image: testImageName1,
			},
			expected: &compute.InstanceSpec{
				ImageID: testImageID1,
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with root volume source uuid",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				RootVolume: &machinev1alpha1.RootVolume{
					SourceUUID: testImageID2,
					Size:       10,
				},
			},
			expected: &compute.InstanceSpec{
				ImageID: testImageID2,
				RootVolume: &capov1.RootVolume{
					SizeGiB: 10,
				},
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with root volume source name",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				RootVolume: &machinev1alpha1.RootVolume{
					SourceUUID: testImageName2,
					Size:       10,
				},
			},
			expected: &compute.InstanceSpec{
				ImageID: testImageID2,
				RootVolume: &capov1.RootVolume{
					SizeGiB: 10,
				},
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with flavor uuid",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Flavor: testFlavorID1,
			},
			expected: &compute.InstanceSpec{
				FlavorID: testFlavorID1,
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with flavor name",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Flavor: testFlavorName1,
			},
			expected: &compute.InstanceSpec{
				FlavorID: testFlavorID1,
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
		{
			name: "with flavor id(non-uuid)",
			providerSpec: &machinev1alpha1.OpenstackProviderSpec{
				Flavor: testFlavorID2,
			},
			expected: &compute.InstanceSpec{
				FlavorID: testFlavorID2,
				Tags: []string{
					"cluster-api-provider-openstack",
					"-",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			bytes, err := json.Marshal(tt.providerSpec)
			if !assert.NoError(err, "Failed to marshal provider spec") {
				return
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

			actual, err := MachineToInstanceSpec(t.Context(),
				&machine,
				apiVIPs,
				ingressVIPs,
				userData,
				instanceService,
				ignoreAddressPairs,
			)
			if !assert.NoError(err, "Expected no error, found one: %v", err) {
				return
			}

			assert.Equal(tt.expected, actual, "unexpected result of MachineToInstanceSpec")
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
		assert.Equal(t, "", extractImageFromProviderSpec(&machinev1alpha1.OpenstackProviderSpec{}), "expected image")
	})
}

func TestExtractRootVolumeFromProviderSpec(t *testing.T) {
	t.Run("with a nil root volume", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("unexpected panic: %v", r)
			}
		}()
		assert.Equal(t, (*capov1.RootVolume)(nil), extractRootVolumeFromProviderSpec(&machinev1alpha1.OpenstackProviderSpec{}), "expected root volume")
	})
}
