/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

// BackendConfig represents a backend server configuration
type BackendConfig struct {
	BackendURL string `json:"backend_url"`
	Status     string `json:"status"`
	SSL        bool   `json:"ssl"`
}

// ListenerConfig represents a listener configuration with its backends
type ListenerConfig struct {
	Name     string           `json:"name"`
	Mode     string           `json:"mode"`
	Key      string           `json:"key"`
	Cert     string           `json:"cert"`
	Port     int32            `json:"port"`
	Backends []*BackendConfig `json:"backends"`
}

// LoadBalancerConfig represents the full load balancer configuration
type LoadBalancerConfig struct {
	Listeners   []*ListenerConfig `json:"listeners"`
	FloatingIps []string          `json:"floating_ips"`
}

// LoadBalancerFloatingIp represents a floating IP for load balancer
type LoadBalancerFloatingIp struct {
	Address  string `json:"address"`
	Vlan     int64  `json:"vlan"`
	Gateway  string `json:"gateway"`
	MarkID   int64  `json:"mark_id"`
	Inbound  int32  `json:"inbound"`
	Outbound int32  `json:"outbound"`
}

// LoadBalancerFloatingIpConfig represents floating IP configuration for load balancer
type LoadBalancerFloatingIpConfig struct {
	FloatingIps []*LoadBalancerFloatingIp `json:"floating_ips"`
	Ports       []int32                   `json:"ports"`
}
