package apis

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestSubnetPayloadAddressFamilies(t *testing.T) {
	for _, tc := range []struct {
		name, network, gateway, start, end string
		wantError                          bool
	}{
		{"IPv4", "192.0.2.0/24", "192.0.2.1", "192.0.2.2", "192.0.2.254", false},
		{"IPv6", "2001:db8::/120", "2001:db8::1", "2001:db8::2", "2001:db8::ff", false},
		{"IPv6 defaults", "2001:db8::/120", "", "", "", false},
		{"bad CIDR", "2001:db8::/129", "", "", "", true},
		{"bad IP", "2001:db8::/120", "invalid", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := &SubnetPayload{Name: "test-subnet", NetworkCIDR: tc.network, Gateway: tc.gateway, StartIP: tc.start, EndIP: tc.end}
			err := binding.Validator.ValidateStruct(payload)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected payload validation result: %v", err)
			}
		})
	}
}
