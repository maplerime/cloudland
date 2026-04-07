/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"net"
)

// CIDROverlaps returns true if two CIDR ranges have any overlap
func CIDROverlaps(cidr1, cidr2 string) (overlap bool, err error) {
	_, net1, err := net.ParseCIDR(cidr1)
	if err != nil {
		return
	}
	_, net2, err := net.ParseCIDR(cidr2)
	if err != nil {
		return
	}

	if net1.Contains(net2.IP) || net2.Contains(net1.IP) {
		overlap = true
	}
	return
}
