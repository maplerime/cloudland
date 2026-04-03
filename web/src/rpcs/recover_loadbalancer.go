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
	logger.Infof("Starting RecoverLoadbalancer with args: %v", args)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 2 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	hyperID64, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || hyperID64 < 0 {
		logger.Errorf("Invalid hypervisor ID: %s, err: %v", args[1], err)
		return
	}
	hyperID := int32(hyperID64)
	logger.Infof("Recovering load balancers for hypervisor ID: %d, argn: %d", hyperID, argn)

	var loadBalancers []*model.LoadBalancer
	if argn > 2 {
		// Specific load balancer IDs provided
		logger.Infof("Specific load balancer IDs provided: %s", args[2])
		lbList := strings.Split(args[2], " ")
		for _, lbIDStr := range lbList {
			lbID, err := strconv.ParseInt(lbIDStr, 10, 64)
			if err != nil {
				logger.Errorf("Invalid load balancer ID: %s, err: %v", lbIDStr, err)
				continue
			}
			logger.Debugf("Querying load balancer ID: %d", lbID)
			lb := &model.LoadBalancer{}
			err = db.Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Preload("Listeners").Preload("Listeners.Backends").Preload("FloatingIps").Where("id = ?", lbID).Take(lb).Error
			if err != nil {
				logger.Errorf("Failed to query load balancer %d: %v", lbID, err)
				continue
			}
			logger.Infof("Found load balancer %d for recovery", lbID)
			loadBalancers = append(loadBalancers, lb)
		}
	} else {
		// Query all load balancers related to this hypervisor
		logger.Infof("Querying all load balancers for hypervisor %d", hyperID)
		ifaces := []*model.Interface{}
		err = db.Where("hyper = ? AND type = 'vrrp'", hyperID).Find(&ifaces).Error
		if err != nil {
			logger.Errorf("Failed to query interfaces for hypervisor %d: %v", hyperID, err)
			return
		}
		logger.Infof("Found %d VRRP interfaces on hypervisor %d", len(ifaces), hyperID)

		vrrpIDs := make([]int64, 0)
		for _, iface := range ifaces {
			if iface.Device > 0 && iface.Type == "vrrp" {
				vrrpIDs = append(vrrpIDs, iface.Device)
			}
		}
		logger.Infof("Extracted %d VRRP instance IDs: %v", len(vrrpIDs), vrrpIDs)

		if len(vrrpIDs) == 0 {
			err = fmt.Errorf("No vrrp instances found for hypervisor %d", hyperID)
			logger.Error(err.Error())
			return
		}

		lbs := []*model.LoadBalancer{}
		err = db.Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Preload("Listeners").Preload("Listeners.Backends").Preload("FloatingIps").Preload("FloatingIps.Subnet").Where("vrrp_instance_id in (?)", vrrpIDs).Find(&lbs).Error
		if err != nil {
			logger.Errorf("Failed to query load balancers for VRRP IDs %v: %v", vrrpIDs, err)
			return
		}
		logger.Infof("Found %d load balancers for recovery", len(lbs))
		loadBalancers = lbs
	}

	if len(loadBalancers) == 0 {
		logger.Infof("No load balancers to recover for hypervisor %d", hyperID)
		status = "No load balancers to recover"
		return
	}

	for _, loadBalancer := range loadBalancers {
		logger.Infof("===== Starting recovery for load balancer %d =====", loadBalancer.ID)
		// Recreate keepalived config (VRRP)
		if loadBalancer.Status != "available" {
			logger.Errorf("Load balancer %d is not available, status: %s, skipping", loadBalancer.ID, loadBalancer.Status)
			continue
		}
		logger.Infof("Load balancer %d status: %s, starting recovery", loadBalancer.ID, loadBalancer.Status)

		routerID := loadBalancer.RouterID
		vrrpID := loadBalancer.VrrpInstanceID
		vrrpVlan := loadBalancer.VrrpInstance.VrrpSubnet.Vlan
		logger.Debugf("LB %d - routerID: %d, vrrpID: %d, vrrpVlan: %d", loadBalancer.ID, routerID, vrrpID, vrrpVlan)

		floatingIps := loadBalancer.FloatingIps
		logger.Infof("LB %d - Found %d floating IPs - %+v", loadBalancer.ID, len(floatingIps), floatingIps)

		// Build floating IP JSON config for keepalived
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
			logger.Infof("LB %d - lbFloatingIpCfg.FloatingIps %v", loadBalancer.ID, lbFloatingIpCfg.FloatingIps)
		}
		for _, listener := range loadBalancer.Listeners {
			lbFloatingIpCfg.Ports = append(lbFloatingIpCfg.Ports, listener.Port)
		}
		jsonData, err := json.Marshal(lbFloatingIpCfg)
		if err != nil {
			logger.Errorf("LB %d - Failed to marshal floating ip json data: %v", loadBalancer.ID, err)
			continue
		}
		logger.Debugf("LB %d - Floating IP config JSON: %s", loadBalancer.ID, string(jsonData))

		// Get VRRP interfaces using common package function
		vrrpIface1, vrrpIface2, err := GetVrrpInterfaces(ctx, vrrpID)
		if err != nil {
			logger.Errorf("LB %d - Failed to get VRRP interfaces: %v", loadBalancer.ID, err)
			continue
		}
		logger.Infof("LB %d - VRRP iface1 hyper: %d, iface2 hyper: %d", loadBalancer.ID, vrrpIface1.Hyper, vrrpIface2.Hyper)

		// Set VRRP IP on MASTER if hyper matches
		if vrrpIface1.Hyper == hyperID {
			logger.Infof("LB %d - Setting VRRP IP for MASTER on hyper %d", loadBalancer.ID, hyperID)
			control := fmt.Sprintf("inter=%d", vrrpIface1.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/set_vrrp_ip.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'MASTER'",
				routerID, vrrpID, vrrpVlan, vrrpIface1.MacAddr, vrrpIface1.Address.Address,
				vrrpIface2.MacAddr, vrrpIface2.Address.Address)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Errorf("LB %d - Set VRRP IP for MASTER failed: %v", loadBalancer.ID, err)
				continue
			}
			logger.Infof("LB %d - Successfully set VRRP IP for MASTER on hyper %d", loadBalancer.ID, hyperID)
			logger.Infof("LB %d - Creating MASTER keepalived config on hyper %d", loadBalancer.ID, hyperID)
			control = fmt.Sprintf("inter=%d", vrrpIface1.Hyper)
			command = fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'MASTER'<<EOF\n%s\nEOF",
				routerID, vrrpID, vrrpVlan, vrrpIface1.Address.Address, vrrpIface1.MacAddr,
				vrrpIface2.Address.Address, vrrpIface2.MacAddr, jsonData)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Errorf("LB %d - Execute MASTER keepalived conf failed: %v", loadBalancer.ID, err)
				continue
			}
		}

		// Set VRRP IP on BACKUP if hyper matches
		if vrrpIface2.Hyper == hyperID {
			logger.Infof("LB %d - Setting VRRP IP for BACKUP on hyper %d", loadBalancer.ID, hyperID)
			control := fmt.Sprintf("inter=%d", vrrpIface2.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/set_vrrp_ip.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'BACKUP'",
				routerID, vrrpID, vrrpVlan, vrrpIface2.MacAddr, vrrpIface2.Address.Address,
				vrrpIface1.MacAddr, vrrpIface1.Address.Address)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Errorf("LB %d - Set VRRP IP for BACKUP failed: %v", loadBalancer.ID, err)
				continue
			}
			logger.Infof("LB %d - Successfully set VRRP IP for BACKUP on hyper %d", loadBalancer.ID, hyperID)
			logger.Infof("LB %d - Creating BACKUP keepalived config on hyper %d", loadBalancer.ID, hyperID)
			control = fmt.Sprintf("inter=%d", vrrpIface2.Hyper)
			command = fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'BACKUP'<<EOF\n%s\nEOF",
				routerID, vrrpID, vrrpVlan, vrrpIface2.Address.Address, vrrpIface2.MacAddr,
				vrrpIface1.Address.Address, vrrpIface1.MacAddr, jsonData)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Errorf("LB %d - Execute BACKUP keepalived conf failed: %v", loadBalancer.ID, err)
				continue
			}
			logger.Infof("LB %d - Successfully recovered BACKUP keepalived config on hyper %d", loadBalancer.ID, hyperID)
		}

		// Recreate haproxy config (listeners and backends)
		logger.Infof("LB %d - Building haproxy config", loadBalancer.ID)
		listenerCfgs := []*ListenerConfig{}
		for _, listener := range loadBalancer.Listeners {
			if len(listener.Backends) > 0 {
				logger.Debugf("LB %d - Listener %d has %d backends", loadBalancer.ID, listener.ID, len(listener.Backends))
				backendCfgs := []*BackendConfig{}
				for _, backend := range listener.Backends {
					logger.Debugf("LB %d - Backend %d: %s, status: %s", loadBalancer.ID, backend.ID, backend.BackendAddr, backend.Status)
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
		logger.Infof("LB %d - Built %d listener configs", loadBalancer.ID, len(listenerCfgs))

		haproxyCfg := &LoadBalancerConfig{Listeners: listenerCfgs}
		for _, fip := range floatingIps {
			haproxyCfg.FloatingIps = append(haproxyCfg.FloatingIps, fip.FipAddress)
		}
		logger.Infof("LB %d - Haproxy config includes %d floating IPs", loadBalancer.ID, len(haproxyCfg.FloatingIps))

		haproxyJsonData, err := json.Marshal(haproxyCfg)
		if err != nil {
			logger.Errorf("LB %d - Failed to marshal haproxy json data: %v", loadBalancer.ID, err)
			continue
		}
		logger.Debugf("LB %d - Haproxy config JSON: %s", loadBalancer.ID, string(haproxyJsonData))

		// Execute haproxy config creation on the hypervisor
		logger.Infof("LB %d - Executing haproxy config creation on hyper %d", loadBalancer.ID, hyperID)
		control := fmt.Sprintf("inter=%d", hyperID)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_haproxy_conf.sh '%d' '%d' '%d'<<EOF\n%s\nEOF",
			loadBalancer.RouterID, loadBalancer.ID, loadBalancer.VrrpInstance.ID, haproxyJsonData)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Errorf("LB %d - Create haproxy conf execution failed: %v", loadBalancer.ID, err)
			continue
		}
		logger.Infof("LB %d - Successfully recovered haproxy config on hyper %d", loadBalancer.ID, hyperID)

		// Recreate floating IP network configuration
		logger.Infof("LB %d - Starting floating IP network configuration recovery for %d floating IPs", loadBalancer.ID, len(floatingIps))
		for _, fip := range floatingIps {
			logger.Infof("LB %d - Recovering floating IP %d (%s) on hyper %d", loadBalancer.ID, fip.ID, fip.FipAddress, hyperID)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_lb_floating.sh '%d' '%s' '%s' '%d' '%d' '%d' '%d'",
				loadBalancer.RouterID, fip.FipAddress, fip.Subnet.Gateway, fip.Subnet.Vlan, fip.ID, fip.Inbound, fip.Outbound)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Errorf("LB %d - Execute create_lb_floating.sh failed for floating IP %d: %v", loadBalancer.ID, fip.ID, err)
				continue
			}
			logger.Infof("LB %d - Successfully recovered floating IP %d (%s) on hyper %d", loadBalancer.ID, fip.ID, fip.FipAddress, hyperID)
		}
		logger.Infof("===== Completed recovery for load balancer %d =====", loadBalancer.ID)
	}

	status = fmt.Sprintf("Successfully recovered %d load balancer(s) on hypervisor %d", len(loadBalancers), hyperID)
	logger.Infof("===== RecoverLoadbalancer completed: %s =====", status)
	return
}
