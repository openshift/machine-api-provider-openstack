package machine

import (
	"testing"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha5"
)

type testSubnetsGetter struct{}

func (testSubnetsGetter) GetSubnetsByFilter(opts subnets.ListOptsBuilder) ([]subnets.Subnet, error) {
	return []subnets.Subnet{{NetworkID: "fakeNetwork"}}, nil
}

func newSubnetsGetter() testSubnetsGetter {
	return testSubnetsGetter{}
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

func TestNetworkParamToCapov1PortOpt(t *testing.T) {
	type checkFunc func(*testing.T, []capov1.PortOpts, error)
	type portCheckFunc func(*testing.T, capov1.PortOpts)
	type fixedIPCheckFunc func(*testing.T, capov1.FixedIP)

	that := func(fns ...checkFunc) []checkFunc { return fns }
	hasPorts := func(want int) checkFunc {
		return func(t *testing.T, ports []capov1.PortOpts, _ error) {
			if have := len(ports); want != have {
				t.Errorf("expected %d ports, found %d", want, have)
			}
		}
	}
	noErrors := func(t *testing.T, _ []capov1.PortOpts, err error) {
		if err != nil {
			t.Errorf("expected no error, found one: %v", err)
		}
	}

	port := func(i int, fns ...portCheckFunc) checkFunc {
		return func(t *testing.T, ports []capov1.PortOpts, _ error) {
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
				noErrors,
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
				noErrors,
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
				noErrors,
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
				noErrors,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			portOpts, err := networkParamToCapov1PortOpt(
				tc.networkParam,
				nil,
				nil,
				nil,
				newSubnetsGetter(),
				false,
			)
			for _, check := range tc.check {
				check(t, portOpts, err)
			}
		})
	}
}
