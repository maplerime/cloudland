/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("recover_loadbalancer", RecoverLoadbalancer)
}

func RecoverLoadbalancer(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| recover_loadbalancer.sh '0' '5'
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 1 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	hyperID64, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || hyperID64 < 0 {
		logger.Error("Invalid hypervisor ID", err)
		return
	}
	hyperID := int32(hyperID64)

	var loadBalancers []*model.LoadBalancer
	if argn > 1 {
		// Specific load balancer IDs provided
		lbList := strings.Split(args[2], " ")
		for _, lbIDStr := range lbList {
			lbID, err := strconv.ParseInt(lbIDStr, 10, 64)
			if err != nil {
				logger.Error("Invalid load balancer ID", err)
				continue
			}
			lb := &model.LoadBalancer{}
			err = db.Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Preload("Listeners").Preload("Listeners.Backends").Preload("FloatingIps").Where("id = ?", lbID).Take(lb).Error
			if err != nil {
				logger.Error("Failed to query load balancer", err)
				continue
			}
			loadBalancers = append(loadBalancers, lb)
		}
	} else {
		// Query all load balancers related to this hypervisor
		ifaces := []*model.Interface{}
		err = db.Where("hyper = ? AND type = 'vrrp'", hyperID).Find(&ifaces).Error
		if err != nil {
			logger.Error("Failed to query interfaces for hypervisor", err)
			return
		}

		vrrpIDs := make([]int64, 0)
		for _, iface := range ifaces {
			if iface.Device > 0 && iface.Type == "vrrp" {
				vrrpIDs = append(vrrpIDs, iface.Device)
			}
		}

		if len(vrrpIDs) == 0 {
			err = fmt.Errorf("No vrrp instances found for hypervisor %d", hyperID)
			logger.Error(err.Error())
			return
		}

		lbs := []*model.LoadBalancer{}
		err = db.Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Preload("Listeners").Preload("Listeners.Backends").Preload("FloatingIps").Where("vrrp_instance_id in (?)", vrrpIDs).Find(&lbs).Error
		if err != nil {
			logger.Error("Failed to query load balancers", err)
			return
		}
		loadBalancers = lbs
	}

	if len(loadBalancers) == 0 {
		status = "No load balancers to recover"
		return
	}

	for _, loadBalancer := range loadBalancers {
		// Recreate keepalived config (VRRP)
		if loadBalancer.Status != "available" {
			logger.Errorf("Load balancer %d is not available, status: %s", loadBalancer.ID, loadBalancer.Status)
			continue
		}

		routerID := loadBalancer.RouterID
		vrrpID := loadBalancer.VrrpInstanceID
		vrrpVlan := loadBalancer.VrrpInstance.VrrpSubnet.Vlan

		// Get floating IPs JSON config
		_, floatingIps, err := floatingIpAdmin.List(ctx, 0, -1, "", "", fmt.Sprintf("load_balancer_id = %d", loadBalancer.ID))
		if err != nil {
			logger.Error("Failed to list floating ips", err)
			continue
		}

		lbFloatingIpCfg := &LoadBalancerFloatingIpConfig{}
		for _, fip := range floatingIps {
			lbFloatingIpCfg.FloatingIps = append(lbFloatingIpCfg.FloatingIps, &LoadBalancerFloatingIp{
				Address:  fip.FipAddress,
				Vlan:     fip.Subnet.Vlan,
				Gateway:  fip.Subnet.Gateway,
				MarkID:   fip.ID,
				Inbound:  fip.Inbound,
				Outbound: fip.Outbound,
			})
		}
		for _, listener := range loadBalancer.Listeners {
			lbFloatingIpCfg.Ports = append(lbFloatingIpCfg.Ports, listener.Port)
		}
		jsonData, err := json.Marshal(lbFloatingIpCfg)
		if err != nil {
			logger.Errorf("Failed to marshal floating ip json data: %v", err)
			continue
		}

		// Get VRRP interfaces using common package function
		vrrpIface1, vrrpIface2, err := GetVrrpInterfaces(ctx, vrrpID)
		if err != nil {
			logger.Error("Failed to get vrrp interfaces", err)
			continue
		}

		// Create keepalived config on MASTER if hyper matches
		if vrrpIface1.Hyper == hyperID {
			control := fmt.Sprintf("inter=%d", vrrpIface1.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'MASTER'<<EOF\n%s\nEOF",
				routerID, vrrpID, vrrpVlan, vrrpIface1.Address.Address, vrrpIface1.MacAddr,
				vrrpIface2.Address.Address, vrrpIface2.MacAddr, jsonData)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Execute MASTER keepalived conf failed", err)
				continue
			}
			logger.Infof("Recovered MASTER keepalived config for load balancer %d on hyper %d", loadBalancer.ID, hyperID)
		}

		// Create keepalived config on BACKUP if hyper matches
		if vrrpIface2.Hyper == hyperID {
			control := fmt.Sprintf("inter=%d", vrrpIface2.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'BACKUP'<<EOF\n%s\nEOF",
				routerID, vrrpID, vrrpVlan, vrrpIface2.Address.Address, vrrpIface2.MacAddr,
				vrrpIface1.Address.Address, vrrpIface1.MacAddr, jsonData)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Execute BACKUP keepalived conf failed", err)
				continue
			}
			logger.Infof("Recovered BACKUP keepalived config for load balancer %d on hyper %d", loadBalancer.ID, hyperID)
		}

		// Recreate haproxy config (listeners and backends)
		listenerCfgs := []*ListenerConfig{}
		for _, listener := range loadBalancer.Listeners {
			if len(listener.Backends) > 0 {
				backendCfgs := []*BackendConfig{}
				for _, backend := range listener.Backends {
					backendCfgs = append(backendCfgs, &BackendConfig{
						BackendURL: backend.BackendAddr,
						Status:     backend.Status,
					})
				}
				listenerCfgs = append(listenerCfgs, &ListenerConfig{
					Name:     fmt.Sprintf("lb-%d-lsn-%d", loadBalancer.ID, listener.ID),
					Mode:     listener.Mode,
					Key:      listener.Key,
					Cert:     listener.Certificate,
					Port:     listener.Port,
					Backends: backendCfgs,
				})
			}
		}

		haproxyCfg := &LoadBalancerConfig{Listeners: listenerCfgs}
		for _, fip := range floatingIps {
			haproxyCfg.FloatingIps = append(haproxyCfg.FloatingIps, fip.FipAddress)
		}

		haproxyJsonData, err := json.Marshal(haproxyCfg)
		if err != nil {
			logger.Errorf("Failed to marshal haproxy json data: %v", err)
			continue
		}

		// Get hyper group for VRRP
		hyperGroup, _, _, err := GetVrrpHyperGroup(ctx, loadBalancer.VrrpInstance)
		if err != nil {
			logger.Error("Failed to get vrrp hyper group", err)
			continue
		}

		// Execute haproxy config creation on the hypervisor
		control := "toall=" + hyperGroup
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_haproxy_conf.sh '%d' '%d' '%d'<<EOF\n%s\nEOF",
			loadBalancer.RouterID, loadBalancer.ID, loadBalancer.VrrpInstance.ID, haproxyJsonData)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Create haproxy conf execution failed", err)
			continue
		}
		logger.Infof("Recovered haproxy config for load balancer %d on hyper %d", loadBalancer.ID, hyperID)
	}

	status = fmt.Sprintf("Successfully recovered %d load balancer(s) on hypervisor %d", len(loadBalancers), hyperID)
	return
}
