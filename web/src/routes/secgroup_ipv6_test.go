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

func TestLegacyDefaultIPv6Rules(t *testing.T) {
	rules := []*model.SecurityRule{
		{RemoteIp: "0.0.0.0/0", Protocol: "tcp", PortMin: 2222, PortMax: 2222},
		{RemoteIp: "192.0.2.0/24", Protocol: "tcp", PortMin: 22, PortMax: 22},
	}
	got := legacyDefaultIPv6Rules(rules)
	if len(got) != 1 || got[0].PortMin != 2222 {
		t.Fatalf("unexpected default permissions: %+v", got)
	}
	rules = append(rules, &model.SecurityRule{RemoteIp: "2001:db8::/64", IpVersion: "ipv6", Protocol: "tcp"})
	if got := legacyDefaultIPv6Rules(rules); len(got) != 0 {
		t.Fatal("existing IPv6 policy must be preserved")
	}
}

func TestNewAccountAndVPCDefaultSecurityRules(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AutoMigrate(&model.User{}, &model.Organization{}, &model.Member{}, &model.Router{},
		&model.Subnet{}, &model.Address{}, &model.Interface{}, &model.SecurityGroup{}, &model.SecurityRule{}).Error; err != nil {
		t.Fatal(err)
	}
	user := &model.User{Username: "ipv6-default-owner"}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	ctx := SetContextDB(context.Background(), db)
	ctx = (&MemberShip{UserID: user.ID, OrgID: 1, OrgName: "admin", Role: model.Admin}).SetContext(ctx)
	org, err := orgAdmin.Create(ctx, "ipv6-default-account", user.Username, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.First(org, org.ID).Error; err != nil {
		t.Fatal(err)
	}
	if org.DefaultSG == 0 {
		t.Fatal("account default security group missing")
	}
	ctx = (&MemberShip{UserID: user.ID, OrgID: org.ID, Role: model.Owner}).SetContext(ctx)
	router, err := routerAdmin.Create(ctx, "ipv6-default-vpc")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		id       int64
		families int
	}{{"account", org.DefaultSG, 2}, {"VPC", router.DefaultSG, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var rules []*model.SecurityRule
			if err := db.Where("secgroup = ?", tc.id).Find(&rules).Error; err != nil {
				t.Fatal(err)
			}
			if len(rules) != 7*tc.families {
				t.Fatalf("got %d default rules, want %d", len(rules), 7*tc.families)
			}
			actual := map[string]bool{}
			for _, rule := range rules {
				actual[fmt.Sprintf("%s %s %s %s %d %d", rule.IpVersion, rule.RemoteIp, rule.Direction, rule.Protocol, rule.PortMin, rule.PortMax)] = true
			}
			for _, family := range []struct {
				version, remote string
				dhcp            int
			}{{"ipv4", "0.0.0.0/0", 68}, {"ipv6", "::/0", 546}} {
				if tc.families == 1 && family.version == "ipv6" {
					continue
				}
				for _, permission := range []string{"egress tcp 1 65535", "egress udp 1 65535", "ingress tcp 22 22", "ingress tcp 3389 3389", fmt.Sprintf("ingress udp %d %d", family.dhcp, family.dhcp), "egress icmp -1 -1", "ingress icmp -1 -1"} {
					key := family.version + " " + family.remote + " " + permission
					if !actual[key] {
						t.Errorf("missing rule: %s", key)
					}
				}
			}
			// Reusing the default must preserve its rules, including deletions.
			if tc.name == "account" {
				if err := db.Where("secgroup = ? AND ip_version = ? AND protocol = ? AND direction = ? AND port_min = ?", tc.id, "ipv6", "tcp", "ingress", 22).Delete(&model.SecurityRule{}).Error; err != nil {
					t.Fatal(err)
				}
				sg, err := secgroupAdmin.GetDefaultSecgroup(ctx)
				if err != nil || sg.ID != tc.id {
					t.Fatalf("reusing default: %v", err)
				}
				iface := &model.Interface{Address: &model.Address{Address: "fd42:6c61:6e64:1::10/120"}, SecurityGroups: []*model.SecurityGroup{sg}}
				if err := secgroupAdmin.AllowInstanceLoginPort(ctx, 22, iface); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.Model(&model.SecurityRule{}).Where("secgroup = ?", tc.id).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 13 {
					t.Fatalf("modified policy was reset: %d rules", count)
				}
			} else {
				// Legacy IPv6 bootstrapping must never populate VPC groups.
				sg := &model.SecurityGroup{}
				if err := db.First(sg, tc.id).Error; err != nil {
					t.Fatal(err)
				}
				iface := &model.Interface{Address: &model.Address{Address: "fd42:6c61:6e64:1::10/120"}, SecurityGroups: []*model.SecurityGroup{sg}}
				if err := secgroupAdmin.AllowInstanceLoginPort(ctx, 22, iface); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.Model(&model.SecurityRule{}).Where("secgroup = ?", tc.id).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 7 {
					t.Fatalf("VPC default was populated with IPv6 rules: %d", count)
				}
			}
		})
	}
}
