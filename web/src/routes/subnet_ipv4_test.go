package routes

import (
	"context"
	"fmt"
	"testing"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
)

func TestIPv4SubnetCompatibility(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.Subnet{}, &model.Address{}, &model.Router{}, &model.IpGroup{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := SetContextDB(context.Background(), db)
	ctx = (&MemberShip{UserID: 7, OrgID: 8, Role: model.Admin}).SetContext(ctx)
	cases := []struct {
		name, network, gateway, start, end, subnetType, dns string
		wantGateway, wantStart, wantEnd, wantMask           string
		wantCount                                           int
	}{
		{"default /23", "192.0.2.0/23", "", "", "", "public", "", "192.0.2.1/23", "192.0.2.2", "192.0.3.254", "255.255.254.0", 510},
		{"default /24", "192.0.2.0/24", "", "", "", "site", "", "192.0.2.1/24", "192.0.2.2", "192.0.2.254", "255.255.255.0", 254},
		{"default /25", "192.0.2.0/25", "", "", "", "internal", "", "192.0.2.1/25", "192.0.2.2", "192.0.2.126", "255.255.255.128", 126},
		{"default /26", "192.0.2.0/26", "", "", "", "vrrp", "", "192.0.2.1/26", "192.0.2.2", "192.0.2.62", "255.255.255.192", 62},
		{"default /27", "192.0.2.0/27", "", "", "", "", "", "192.0.2.1/27", "192.0.2.2", "192.0.2.30", "255.255.255.224", 30},
		{"default /28", "192.0.2.0/28", "", "", "", "public", "", "192.0.2.1/28", "192.0.2.2", "192.0.2.14", "255.255.255.240", 14},
		{"default /29", "192.0.2.0/29", "", "", "", "public", "", "192.0.2.1/29", "192.0.2.2", "192.0.2.6", "255.255.255.248", 6},
		{"gateway inside pool", "192.0.2.0/29", "192.0.2.3", "192.0.2.2", "192.0.2.5", "public", "8.8.8.8", "192.0.2.3/29", "192.0.2.2", "192.0.2.5", "255.255.255.248", 4},
		{"gateway at start", "192.0.2.0/29", "192.0.2.2", "192.0.2.2", "192.0.2.5", "site", "", "192.0.2.2/29", "192.0.2.3", "192.0.2.5", "255.255.255.248", 4},
		{"gateway at end", "192.0.2.0/29", "192.0.2.5", "192.0.2.2", "192.0.2.5", "public", "", "192.0.2.5/29", "192.0.2.2", "192.0.2.4", "255.255.255.248", 4},
		{"gateway outside pool", "192.0.2.0/29", "192.0.2.1", "192.0.2.3", "192.0.2.5", "internal", "", "192.0.2.1/29", "192.0.2.3", "192.0.2.5", "255.255.255.248", 4},
		{"single address", "192.0.2.0/29", "", "192.0.2.3", "192.0.2.3", "site", "", "192.0.2.1/29", "192.0.2.3", "192.0.2.3", "255.255.255.248", 2},
		{"explicit network and broadcast", "192.0.2.0/29", "", "192.0.2.0", "192.0.2.7", "public", "", "192.0.2.1/29", "192.0.2.0", "192.0.2.7", "255.255.255.248", 8},
		{"legacy DNS", "192.0.2.0/29", "", "", "", "public", "dns.example.com", "192.0.2.1/29", "192.0.2.2", "192.0.2.6", "255.255.255.248", 6},
		{"CIDR with host bits", "192.0.2.3/29", "", "", "", "public", "", "192.0.2.1/29", "192.0.2.2", "192.0.2.6", "255.255.255.248", 6},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subnet, err := subnetAdmin.Create(ctx, 200+i, fmt.Sprintf("ipv4-%d", i), tc.network, tc.gateway, tc.start, tc.end, tc.subnetType, tc.dns, "example.com", true, nil, nil, 10)
			if err != nil {
				t.Fatal(err)
			}
			wantType := tc.subnetType
			if wantType == "" {
				wantType = "internal"
			}
			if subnet.Network != tc.network || subnet.Gateway != tc.wantGateway || subnet.Start != tc.wantStart || subnet.End != tc.wantEnd || subnet.Netmask != tc.wantMask || subnet.Type != wantType || subnet.NameServer != tc.dns || subnet.DomainSearch != "example.com" || !subnet.Dhcp {
				t.Fatalf("IPv4 subnet fields changed: %+v", subnet)
			}
			var addresses []model.Address
			if err = db.Where("subnet_id = ?", subnet.ID).Order("id").Find(&addresses).Error; err != nil {
				t.Fatal(err)
			}
			if len(addresses) != tc.wantCount {
				t.Fatalf("got %d addresses, want %d", len(addresses), tc.wantCount)
			}
			seen := map[string]bool{}
			for _, address := range addresses {
				if seen[address.Address] || address.Type != "ipv4" || address.Netmask != tc.wantMask || address.Owner != 8 || address.Creater != 7 {
					t.Fatalf("IPv4 address fields changed: %+v", address)
				}
				seen[address.Address] = true
			}
			if addresses[len(addresses)-1].Address != tc.wantGateway {
				t.Fatal("gateway is no longer inserted last")
			}
			renamed := fmt.Sprintf("renamed-%d", i)
			if err = subnetAdmin.Update(ctx, subnet.ID, renamed, "internal", nil, 20); err != nil {
				t.Fatal(err)
			}
			var updated model.Subnet
			if err = db.First(&updated, subnet.ID).Error; err != nil {
				t.Fatal(err)
			}
			if updated.Name != renamed || updated.Type != "internal" || updated.Priority != 20 || updated.Gateway != tc.wantGateway {
				t.Fatalf("IPv4 metadata update changed: %+v", updated)
			}
		})
	}
	// Older stored network formats must still permit metadata-only updates.
	legacy := &model.Subnet{Name: "legacy", Network: "192.0.2.0", Type: "public"}
	if err = db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err = subnetAdmin.Update(ctx, legacy.ID, "legacy-renamed", "site", nil, 0); err != nil {
		t.Fatal(err)
	}
}
