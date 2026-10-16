package routes

import (
	"context"
	"fmt"
	"testing"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

func TestPrepareSubnetNetwork(t *testing.T) {
	cases := []struct {
		name, network, gateway, start, end, subnetType, dns string
		wantError                                           bool
	}{
		{name: "IPv4", network: "192.0.2.0/24", subnetType: "internal"},
		{name: "IPv6 public", network: "2001:db8::/120", subnetType: "public"},
		{name: "IPv6 site", network: "2001:db8::/125", subnetType: "site"},
		{name: "IPv6 maximum pool", network: "2001:db8::/119", subnetType: "site"},
		{name: "IPv6 last address", network: "2001:db8::/125", start: "2001:db8::7", end: "2001:db8::7", subnetType: "site"},
		{name: "single gateway", network: "2001:db8::/125", start: "2001:db8::1", end: "2001:db8::1", subnetType: "site"},
		{name: "invalid CIDR", network: "invalid", wantError: true},
		{name: "IPv6 internal", network: "2001:db8::/120", subnetType: "internal", wantError: true},
		{name: "IPv6 omitted type", network: "2001:db8::/120", wantError: true},
		{name: "IPv6 vrrp", network: "2001:db8::/120", subnetType: "vrrp", wantError: true},
		{name: "IPv6 large", network: "2001:db8::/64", subnetType: "site", wantError: true},
		{name: "IPv6 count overflow", network: "::/0", subnetType: "site", wantError: true},
		{name: "IPv6 too small", network: "2001:db8::/126", subnetType: "site", wantError: true},
		{name: "mapped IPv6", network: "::ffff:192.0.2.0/120", subnetType: "site", wantError: true},
		{name: "wrong gateway family", network: "2001:db8::/120", gateway: "192.0.2.1", subnetType: "site", wantError: true},
		{name: "wrong start family", network: "2001:db8::/120", start: "192.0.2.2", subnetType: "site", wantError: true},
		{name: "gateway outside", network: "2001:db8::/120", gateway: "2001:db8:1::1", subnetType: "site", wantError: true},
		{name: "start outside", network: "2001:db8::/120", start: "2001:db8::100", subnetType: "site", wantError: true},
		{name: "end outside", network: "2001:db8::/120", end: "2001:db8::100", subnetType: "site", wantError: true},
		{name: "reversed", network: "2001:db8::/120", start: "2001:db8::5", end: "2001:db8::3", subnetType: "site", wantError: true},
		{name: "invalid IP", network: "2001:db8::/120", end: "invalid", subnetType: "site", wantError: true},
		{name: "invalid DNS", network: "2001:db8::/120", subnetType: "site", dns: "invalid", wantError: true},
		{name: "IPv4 explicit broadcast", network: "192.0.2.0/24", end: "192.0.2.255", subnetType: "public"},
		{name: "IPv4 explicit network address", network: "192.0.2.0/24", start: "192.0.2.0", subnetType: "public"},
		{name: "IPv4 DNS compatibility", network: "192.0.2.0/24", dns: "dns.example.com", subnetType: "public"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareSubnetNetwork(tc.network, tc.gateway, tc.start, tc.end, tc.subnetType, tc.dns)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestSubnetCreateAddresses(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.Subnet{}, &model.Address{}, &model.Router{}, &model.IpGroup{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := SetContextDB(context.Background(), db)
	ctx = (&MemberShip{UserID: 1, OrgID: 1, Role: model.Admin}).SetContext(ctx)
	cases := []struct {
		name, network, gateway, start, end, subnetType, ipType, netmask string
		want                                                            []string
	}{
		{"IPv4", "192.0.2.0/29", "", "", "", "public", "ipv4", "255.255.255.248", []string{"192.0.2.1/29", "192.0.2.2/29", "192.0.2.3/29", "192.0.2.4/29", "192.0.2.5/29", "192.0.2.6/29"}},
		{"IPv6 public", "2001:db8::/125", "", "", "", "public", "ipv6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff8", []string{"2001:db8::1/125", "2001:db8::2/125", "2001:db8::3/125", "2001:db8::4/125", "2001:db8::5/125", "2001:db8::6/125", "2001:db8::7/125"}},
		{"IPv6 gateway inside", "2001:db8::/125", "2001:0db8:0:0:0:0:0:3", "2001:db8::2", "2001:0db8:0:0:0:0:0:4", "site", "ipv6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff8", []string{"2001:db8::2/125", "2001:db8::3/125", "2001:db8::4/125"}},
		{"IPv6 gateway at end", "2001:db8::/125", "2001:db8::4", "2001:db8::2", "2001:db8::4", "site", "ipv6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff8", []string{"2001:db8::2/125", "2001:db8::3/125", "2001:db8::4/125"}},
		{"IPv6 single gateway", "2001:db8::/125", "2001:db8::3", "2001:db8::3", "2001:db8::3", "site", "ipv6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff8", []string{"2001:db8::3/125"}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subnet, err := subnetAdmin.Create(ctx, 100+i, fmt.Sprintf("subnet-%d", i), tc.network, tc.gateway, tc.start, tc.end, tc.subnetType, "", "", false, nil, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var addresses []model.Address
			if err = db.Where("subnet_id = ?", subnet.ID).Find(&addresses).Error; err != nil {
				t.Fatal(err)
			}
			if len(addresses) != len(tc.want) {
				t.Fatalf("got %d addresses, want %d", len(addresses), len(tc.want))
			}
			seen := map[string]bool{}
			for _, address := range addresses {
				if seen[address.Address] || address.Type != tc.ipType || address.Netmask != tc.netmask {
					t.Fatalf("invalid or duplicate address: %+v", address)
				}
				seen[address.Address] = true
			}
			for _, want := range tc.want {
				if !seen[want] {
					t.Errorf("missing address %s", want)
				}
			}
			if tc.ipType == "ipv6" {
				if err = subnetAdmin.Update(ctx, subnet.ID, subnet.Name, "internal", nil, 0); err == nil {
					t.Fatal("allowed changing an IPv6 subnet to internal")
				}
				if err = subnetAdmin.Update(ctx, subnet.ID, subnet.Name, "site", nil, 0); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
