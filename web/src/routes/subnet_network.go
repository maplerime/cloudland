package routes

import (
	"bytes"
	"fmt"
	"net"

	. "web/src/common"

	"github.com/apparentlymart/go-cidr/cidr"
)

type subnetNetwork struct {
	gateway net.IP
	start   net.IP
	end     net.IP
	prefix  int
	netmask string
	ipType  string
}

func validateSubnetType(ip net.IP, subnetType string) error {
	if ip != nil && ip.To4() == nil && subnetType != "public" && subnetType != "site" {
		return NewCLError(ErrInvalidParameter, "IPv6 subnets must be public or site", nil)
	}
	return nil
}

// prepareSubnetNetwork validates the complete pool before any addresses are written.
func prepareSubnetNetwork(network, gateway, start, end, subnetType, dns string) (*subnetNetwork, error) {
	ip, ipNet, err := net.ParseCIDR(network)
	if err != nil {
		return nil, NewCLError(ErrInvalidCIDR, "Invalid CIDR", err)
	}
	prefix, bits := ipNet.Mask.Size()
	if bits == 128 && ip.To4() != nil {
		return nil, NewCLError(ErrInvalidCIDR, "IPv4-mapped IPv6 CIDRs are not supported", nil)
	}
	if err = validateSubnetType(ip, subnetType); err != nil {
		return nil, err
	}
	// Check host bits instead of counting in uint64, which overflows for large IPv6 networks.
	if bits-prefix < 3 || bits-prefix > 9 {
		return nil, NewCLError(ErrCIDRTooBig, "Network/mask must have between 5 and 1000 addresses", nil)
	}
	if bits == 128 && dns != "" && net.ParseIP(dns) == nil {
		return nil, NewCLError(ErrInvalidParameter, "Name server must be a valid IP address", nil)
	}
	first, last := cidr.AddressRange(ipNet)
	pool := &subnetNetwork{prefix: prefix, netmask: net.IP(ipNet.Mask).String(), ipType: "ipv4"}
	if bits == 128 {
		pool.ipType = "ipv6"
	}
	parseAddress := func(value, label string, fallback net.IP) (net.IP, error) {
		address := fallback
		if value != "" {
			address = net.ParseIP(value)
		}
		if address == nil || (address.To4() != nil) != (bits == 32) || !ipNet.Contains(address) {
			return nil, NewCLError(ErrInvalidParameter, fmt.Sprintf("%s must be an IP address in the subnet with the same address family", label), nil)
		}
		if bits == 32 {
			address = address.To4()
		} else {
			address = address.To16()
		}
		return address, nil
	}
	pool.gateway, err = parseAddress(gateway, "Gateway", cidr.Inc(first))
	if err != nil {
		return nil, err
	}
	pool.start, err = parseAddress(start, "Start", cidr.Inc(first))
	if err != nil {
		return nil, err
	}
	defaultEnd := last
	if bits == 32 {
		defaultEnd = cidr.Dec(last)
	}
	pool.end, err = parseAddress(end, "End", defaultEnd)
	if err != nil {
		return nil, err
	}
	if bytes.Compare(pool.start, pool.end) > 0 {
		return nil, NewCLError(ErrInvalidParameter, "Start IP must not be greater than end IP", nil)
	}
	return pool, nil
}
