package common

import (
	"context"
	"testing"

	"web/src/model"
)

func TestIPv6PublicInterfaceRejectedBeforeAllocation(t *testing.T) {
	_, _, err := DerivePublicInterface(context.Background(), nil, nil, []*model.FloatingIp{{FipAddress: "2001:db8::2/120"}}, "", "", 0, 0, false)
	if err == nil {
		t.Fatal("IPv6 floating IP allowed as a public VM interface")
	}
	_, err = CreateInterface(context.Background(), &model.Subnet{Network: "2001:db8::/120"}, 0, 0, 0, 0, 0, "", "", "eth0", "native", nil, false)
	if err == nil {
		t.Fatal("IPv6 allowed as a native VM interface")
	}
}
