package routes

import (
	"testing"

	. "web/src/common"
	"web/src/model"
)

func TestValidateFloatingIPTarget(t *testing.T) {
	for _, tc := range []struct {
		public, private string
		typeName        ElasticType
		valid           bool
	}{
		{"192.0.2.2/24", "10.0.0.2/24", PublicFloating, true},
		{"2001:db8::2/120", "10.0.0.2/24", PublicFloating, true},
		{"2001:db8::2/120", "10.0.0.2/24", PublicSite, false},
		{"2001:db8::2/120", "10.0.0.2/24", PublicLoadBalancer, false},
		{"2001:db8::2/120", "fd00::2/64", PublicFloating, false},
		{"invalid", "10.0.0.2/24", PublicFloating, false},
	} {
		if err := validateFloatingIPTarget(tc.public, tc.private, string(tc.typeName)); (err == nil) != tc.valid {
			t.Errorf("%s -> %s (%s): %v", tc.public, tc.private, tc.typeName, err)
		}
	}
}

func TestSelectFloatingIPSubnets(t *testing.T) {
	v4 := &model.Subnet{Network: "192.0.2.0/24"}
	v6 := &model.Subnet{Network: "2001:db8::/120"}
	for _, tc := range []struct {
		name        string
		subnets     []*model.Subnet
		address     string
		explicit    bool
		site        []*model.Subnet
		lb          *model.LoadBalancer
		want        *model.Subnet
		wantAddress string
	}{
		{name: "automatic IPv4", subnets: []*model.Subnet{v6, v4}, want: v4},
		{name: "explicit IPv6", subnets: []*model.Subnet{v6}, explicit: true, want: v6},
		{name: "canonical IPv6", subnets: []*model.Subnet{v4, v6}, address: "2001:0db8:0:0:0:0:0:2", want: v6, wantAddress: "2001:db8::2"},
		{name: "mixed explicit pools", subnets: []*model.Subnet{v4, v6}, explicit: true},
		{name: "address family mismatch", subnets: []*model.Subnet{v4}, explicit: true, address: "2001:db8::2"},
		{name: "outside pool", subnets: []*model.Subnet{v6}, explicit: true, address: "2001:db9::2"},
		{name: "invalid address", subnets: []*model.Subnet{v4}, address: "invalid"},
		{name: "IPv6 load balancer", subnets: []*model.Subnet{v6}, explicit: true, lb: &model.LoadBalancer{}},
		{name: "IPv6 site", subnets: []*model.Subnet{v4}, site: []*model.Subnet{v6}},
		{name: "IPv6 plus IPv4 site", subnets: []*model.Subnet{v6}, explicit: true, site: []*model.Subnet{v4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, address, err := selectFloatingIPSubnets(tc.subnets, tc.address, tc.explicit, tc.site, tc.lb)
			if tc.want == nil {
				if err == nil {
					t.Fatal("expected rejection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0] != tc.want || address != tc.wantAddress {
				t.Fatalf("unexpected selection: %v, %q", got, address)
			}
		})
	}
}
