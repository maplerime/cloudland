package routes

import (
	"fmt"
	"net"
	"strings"

	. "web/src/common"
	"web/src/model"
)

func validateFloatingIPTarget(publicAddress, privateAddress, publicType string) error {
	publicIP := net.ParseIP(strings.Split(publicAddress, "/")[0])
	privateIP := net.ParseIP(strings.Split(privateAddress, "/")[0])
	if publicIP == nil || privateIP == nil || privateIP.To4() == nil {
		return fmt.Errorf("floating IP requires a valid IPv4 instance target")
	}
	if publicIP.To4() == nil && publicType != string(PublicFloating) {
		return fmt.Errorf("IPv6 currently supports only namespace floating IPs")
	}
	return nil
}

// selectFloatingIPSubnets keeps automatic allocation on IPv4 unless the caller
// requests IPv6 by address or explicitly selects an IPv6 public subnet.
func selectFloatingIPSubnets(subnets []*model.Subnet, address string, explicit bool, siteSubnets []*model.Subnet, loadBalancer *model.LoadBalancer) ([]*model.Subnet, string, error) {
	family := 4
	if address != "" {
		ip := net.ParseIP(address)
		if ip == nil {
			return nil, "", fmt.Errorf("public IP must be a valid IP address")
		}
		address = ip.String()
		if ip.To4() == nil {
			family = 6
		}
	} else if explicit && len(subnets) > 0 {
		ip, _, err := net.ParseCIDR(subnets[0].Network)
		if err != nil {
			return nil, "", fmt.Errorf("invalid public subnet network: %w", err)
		}
		if ip.To4() == nil {
			family = 6
		}
	}
	if family == 6 && (len(siteSubnets) > 0 || loadBalancer != nil) {
		return nil, "", fmt.Errorf("IPv6 currently supports only namespace floating IPs")
	}
	for _, subnet := range siteSubnets {
		ip, _, err := net.ParseCIDR(subnet.Network)
		if err != nil || ip.To4() == nil {
			return nil, "", fmt.Errorf("IPv6 site floating IPs are not supported")
		}
	}
	selected := make([]*model.Subnet, 0, len(subnets))
	for _, subnet := range subnets {
		ip, network, err := net.ParseCIDR(subnet.Network)
		if err != nil {
			return nil, "", fmt.Errorf("invalid public subnet network: %w", err)
		}
		isIPv6 := ip.To4() == nil
		if isIPv6 != (family == 6) {
			if explicit {
				return nil, "", fmt.Errorf("public IP and public subnets must use the same address family")
			}
			continue
		}
		if address != "" && !network.Contains(net.ParseIP(address)) {
			continue
		}
		selected = append(selected, subnet)
	}
	if len(selected) == 0 {
		return nil, "", fmt.Errorf("no matching IPv%d public subnets available", family)
	}
	return selected, address, nil
}
