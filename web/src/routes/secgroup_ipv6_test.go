package routes

import (
	"testing"
	"web/src/model"
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
