package apis

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestInterfaceIPAddressBinding(t *testing.T) {
	for _, tc := range []struct {
		address string
		invalid bool
	}{
		{"", false}, {"192.0.2.10", false}, {"2001:db8::10", false},
		{"2001:0db8:0000:0000:0000:0000:0000:0010", false},
		{"invalid", true}, {"192.0.2.999", true}, {"2001:db8::gg", true},
	} {
		payload := &InterfacePayload{}
		body, _ := json.Marshal(map[string]string{"ip_address": tc.address})
		if err := json.Unmarshal(body, payload); err != nil {
			t.Fatal(err)
		}
		err := binding.Validator.ValidateStruct(payload)
		if (err != nil) != tc.invalid {
			t.Errorf("address=%q error=%v", tc.address, err)
		}
		if payload.IpAddress != tc.address {
			t.Fatal("ip_address JSON binding lost the address")
		}
	}
}

func TestIPv6SecurityRuleBinding(t *testing.T) {
	for _, cidr := range []string{"192.0.2.0/24", "2001:db8::/64", "::/0"} {
		payload := &SecurityRulePayload{RemoteCIDR: cidr, Direction: "ingress", Protocol: "tcp", PortMin: 22, PortMax: 22}
		if err := binding.Validator.ValidateStruct(payload); err != nil {
			t.Errorf("%s: %v", cidr, err)
		}
	}
}
