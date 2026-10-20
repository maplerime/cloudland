package common

import (
	"net"
	"strings"
)

// IPAddressType also accepts addresses stored with a CIDR suffix.
func IPAddressType(address string) string {
	ip := net.ParseIP(strings.SplitN(address, "/", 2)[0])
	if ip != nil && ip.To4() == nil {
		return "ipv6"
	}
	return "ipv4"
}
