package common

import (
	"context"
	"testing"

	"web/src/model"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

func TestInstanceNetworkFamilies(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := SetContextDB(context.Background(), db)
	for _, tc := range []struct{ address, mask, gateway, family, defaultIP string }{
		{"192.0.2.10/24", "255.255.255.0", "192.0.2.1/24", "ipv4", "0.0.0.0"},
		{"2001:db8::10/120", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00", "2001:db8::1/120", "ipv6", "::"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			subnet := &model.Subnet{Network: tc.address, Netmask: tc.mask, Gateway: tc.gateway}
			iface := &model.Interface{Name: "eth0", PrimaryIf: true, Address: &model.Address{Address: tc.address, Subnet: subnet},
				SecondAddresses: []*model.Address{{Address: "2001:db8:1::20/120", Subnet: &model.Subnet{Netmask: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00"}}}}
			inst := &model.Instance{Image: &model.Image{OSCode: "linux"}, Interfaces: []*model.Interface{iface,
				{Name: "eth1", Address: &model.Address{Address: "192.0.2.20/24", Subnet: &model.Subnet{Netmask: "255.255.255.0"}}}}}
			networks, extras, err := GetInstanceNetworks(ctx, inst, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(networks) != 3 || len(extras) != 1 {
				t.Fatalf("networks=%+v extras=%v", networks, extras)
			}
			primary := networks[0]
			if primary.Type != tc.family || primary.Netmask != tc.mask || len(primary.Routes) != 1 || primary.Link != "eth0" {
				t.Fatalf("invalid primary network: %+v", primary)
			}
			route := primary.Routes[0]
			if route.Network != tc.defaultIP || route.Netmask != tc.defaultIP || IPAddressType(route.Gateway) != tc.family {
				t.Fatalf("invalid default route: %+v", route)
			}
			if networks[1].Type != "ipv6" || networks[2].Type != "ipv4" || len(networks[1].Routes) != 0 || len(networks[2].Routes) != 0 {
				t.Fatalf("invalid secondary networks: %+v", networks)
			}
		})
	}
}

func TestIPv6InterfaceRejectsInternalSubnet(t *testing.T) {
	iface, err := CreateInterface(context.Background(), &model.Subnet{Network: "2001:db8::/120", Type: "internal"}, 0, 0, 0, 0, 0, "", "", "eth0", "instance", nil, false)
	if err == nil || iface != nil {
		t.Fatal("IPv6 internal interface must be rejected before allocation")
	}
}
