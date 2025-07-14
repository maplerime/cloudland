/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"net"
	"net/http"
	"strconv"
	"strings"
	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
	"web/src/utils/encrpt"

	"github.com/gin-gonic/gin"
	"github.com/go-macaron/session"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"
)

var (
	instanceAdmin = &InstanceAdmin{}
	instanceView  = &InstanceView{}
)

const MaxmumSnapshot = 96

type InstanceAdmin struct{}

type InstanceView struct{}

type ExecutionCommand struct {
	Control string
	Command string
}

type NetworkLink struct {
	MacAddr string `json:"ethernet_mac_address"`
	Mtu     uint   `json:"mtu"`
	ID      string `json:"id"`
	Type    string `json:"type,omitempty"`
}

type VolumeInfo struct {
	ID      int64  `json:"id"`
	UUID    string `json:"uuid"`
	Device  string `json:"device"`
	Booting bool   `json:"booting"`
}

type InstanceData struct {
	Userdata   string             `json:"userdata"`
	DNS        string             `json:"dns"`
	Vlans      []*VlanInfo        `json:"vlans"`
	Networks   []*InstanceNetwork `json:"networks"`
	Links      []*NetworkLink     `json:"links"`
	Volumes    []*VolumeInfo      `json:"volumes"`
	Keys       []string           `json:"keys"`
	RootPasswd string             `json:"root_passwd"`
	LoginPort  int                `json:"login_port"`
	OSCode     string             `json:"os_code"`
}

type InstancesData struct {
	Instances []*model.Instance `json:"instancedata"`
	IsAdmin   bool              `json:"is_admin"`
}

func (a *InstanceAdmin) GetHyperGroup(ctx context.Context, zoneID int64, skipHyper int32) (hyperGroup string, err error) {
	ctx, db := GetContextDB(ctx)
	hypers := []*model.Hyper{}
	where := fmt.Sprintf("zone_id = %d and status = 1 and hostid <> %d", zoneID, skipHyper)
	if err = db.Where(where).Find(&hypers).Error; err != nil {
		logger.Error("Hypers query failed", err)
		return
	}
	if len(hypers) == 0 {
		logger.Error("No qualified hypervisor")
		return
	}
	hyperGroup = fmt.Sprintf("group-zone-%d", zoneID)
	for i, h := range hypers {
		if i == 0 {
			hyperGroup = fmt.Sprintf("%s:%d", hyperGroup, h.Hostid)
		} else {
			hyperGroup = fmt.Sprintf("%s,%d", hyperGroup, h.Hostid)
		}
	}
	return
}

func (a *InstanceAdmin) Create(ctx context.Context, count int, prefix, userdata string, image *model.Image,
	zone *model.Zone, routerID int64, primaryIface *InterfaceInfo, secondaryIfaces []*InterfaceInfo,
	keys []*model.Key, rootPasswd string, loginPort, hyperID int, cpu int32, memory int32, disk int32, nestedEnable bool, poolID string) (instances []*model.Instance, err error) {
	logger.Debugf("Create %d instances with image %s, zone %s, router %d, primary interface %v, secondary interfaces %v, keys %v, root password %s, hyper %d, cpu %d, memory %d, disk %d, nestedEnable %t, poolID %s",
		count, image.Name, zone.Name, routerID, primaryIface, secondaryIfaces, keys, "********", hyperID, cpu, memory, disk, nestedEnable, poolID)
	if count > 1 && len(primaryIface.PublicIps) > 0 {
		err = fmt.Errorf("Public addresses are not allowed to set when count > 1")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	if image.Status != "available" {
		err = fmt.Errorf("Image status not available")
		logger.Error("Image status not available")
		return
	}
	if image.Size > int64(disk)*1024*1024*1024 {
		err = fmt.Errorf("Flavor disk size is not enough for the image")
		logger.Error(err)
		return
	}
	zoneID := zone.ID
	if hyperID >= 0 {
		hyper := &model.Hyper{}
		err = db.Where("hostid = ?", hyperID).Take(hyper).Error
		if err != nil {
			logger.Error("Failed to query hypervisor", err)
			return
		}
		if hyper.ZoneID != zone.ID {
			logger.Errorf("Hypervisor %v is not in zone %d, %v", hyper, zoneID, err)
			err = fmt.Errorf("Hypervisor is not in this zone")
			return
		}
	}
	if loginPort <= 0 {
		if image.OSCode == "linux" {
			loginPort = 22
		} else if image.OSCode == "windows" {
			loginPort = 3389
		}
	}
	hyperGroup, err := a.GetHyperGroup(ctx, zoneID, -1)
	if err != nil {
		logger.Error("No valid hypervisor", err)
		return
	}
	passwdLogin := false
	if rootPasswd != "" {
		passwdLogin = true
		logger.Debug("Root password login enabled")
	}

	driver := GetVolumeDriver()
	if driver != "local" {
		defaultPoolID := viper.GetString("volume.default_wds_pool_id")
		if poolID == "" {
			poolID = defaultPoolID
		}
		if poolID != defaultPoolID {
			err = db.Where("image_id = ? and pool_id = ? and status = ?", image.ID, poolID, model.StorageStatusSynced).First(&model.ImageStorage{}).Error
			if err != nil {
				logger.Errorf("Failed to query image storage %d, %v", image.ID, err)
				err = fmt.Errorf("Image storage not found")
				return
			}
		}
		logger.Debugf("Using volume driver %s with pool ID %s", driver, poolID)
	}

	execCommands := []*ExecutionCommand{}
	i := 0
	hostname := prefix
	for i < count {
		if count > 1 {
			hostname = fmt.Sprintf("%s-%d", prefix, i+1)
		}
		total := 0

		if driver == "local" {
			if err = db.Unscoped().Model(&model.Instance{}).Where("image_id = ?", image.ID).Count(&total).Error; err != nil {
				logger.Error("Failed to query total instances with the image", err)
				return
			}
		} else {
			if err = db.Model(&model.Instance{}).
				Unscoped().
				Joins("LEFT JOIN volumes b ON instances.id = b.instance_id AND b.booting = ?", true).
				Where(fmt.Sprintf("b.path like '%%%s%%'", poolID)).
				Where("instances.image_id = ?", image.ID).
				Count(&total).Error; err != nil {
				logger.Error("Failed to count instances with volumes matching pool_id", err)
			}
		}
		snapshot := total/MaxmumSnapshot + 1 // Same snapshot reference can not be over 128, so use 96 here
		instance := &model.Instance{
			Model:       model.Model{Creater: memberShip.UserID},
			Owner:       memberShip.OrgID,
			Hostname:    hostname,
			ImageID:     image.ID,
			Snapshot:    int64(snapshot),
			Keys:        keys,
			PasswdLogin: passwdLogin,
			LoginPort:   int32(loginPort),
			Userdata:    userdata,
			Status:      "pending",
			ZoneID:      zoneID,
			RouterID:    routerID,
			Cpu:         cpu,
			Memory:      memory,
			Disk:        disk,
		}
		err = db.Create(instance).Error
		if err != nil {
			logger.Error("DB create instance failed", err)
			return
		}
		instance.Image = image
		instance.Zone = zone
		var bootVolume *model.Volume
		imagePrefix := fmt.Sprintf("image-%d-%s", image.ID, strings.Split(image.UUID, "-")[0])
		// boot volume name format: instance-15-boot-volume-10
		bootVolume, err = volumeAdmin.CreateVolume(ctx, fmt.Sprintf("instance-%d-boot-volume", instance.ID), instance.Disk, instance.ID, true, 0, 0, 0, 0, "")
		if err != nil {
			logger.Error("Failed to create boot volume", err)
			return
		}
		metadata := ""
		var ifaces []*model.Interface
		// cloud-init does not support set encrypted password for windows
		// so we only encrypt the password for linux and others
		instancePasswd := rootPasswd
		if rootPasswd != "" && image.OSCode != "windows" {
			instancePasswd, err = encrpt.Mkpasswd(rootPasswd, "sha512")
			if err != nil {
				logger.Errorf("Failed to encrypt admin password, %v", err)
				return
			}
		}
		ifaces, metadata, err = a.buildMetadata(ctx, primaryIface, secondaryIfaces, instancePasswd, loginPort, keys, instance, userdata, routerID, zoneID, "")
		if err != nil {
			logger.Error("Build instance metadata failed", err)
			return
		}
		instance.Interfaces = ifaces
		rcNeeded := fmt.Sprintf("cpu=%d memory=%d disk=%d network=%d", instance.Cpu, instance.Memory*1024, instance.Disk*1024*1024, 0)
		control := "select=" + hyperGroup + " " + rcNeeded
		if i == 0 && hyperID >= 0 {
			control = fmt.Sprintf("inter=%d %s", hyperID, rcNeeded)
		}
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/launch_vm.sh '%d' '%s.%s' '%t' '%d' '%s' '%d' '%d' '%d' '%d' '%t' '%s' '%s'<<EOF\n%s\nEOF", instance.ID, imagePrefix, image.Format, image.QAEnabled, snapshot, hostname, instance.Cpu, instance.Memory, instance.Disk, bootVolume.ID, nestedEnable, image.BootLoader, poolID, base64.StdEncoding.EncodeToString([]byte(metadata)))
		execCommands = append(execCommands, &ExecutionCommand{
			Control: control,
			Command: command,
		})
		instances = append(instances, instance)
		i++
	}
	a.executeCommandList(ctx, execCommands)
	return
}

func (a *InstanceAdmin) executeCommandList(ctx context.Context, cmdList []*ExecutionCommand) {
	var err error
	for _, cmd := range cmdList {
		err = HyperExecute(ctx, cmd.Control, cmd.Command)
		if err != nil {
			logger.Error("Command execution failed", err)
		}
	}
	return
}

func (a *InstanceAdmin) ChangeInstanceStatus(ctx context.Context, instance *model.Instance, action string) (err error) {
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/action_vm.sh '%d' '%s'", instance.ID, action)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete vm command execution failed", err)
		return
	}
	return
}

func (a *InstanceAdmin) Update(ctx context.Context, instance *model.Instance, flavor *model.Flavor, hostname string, action PowerAction, hyperID int) (err error) {
	if instance.Status == "migrating" {
		err = fmt.Errorf("Instance is not in a valid state")
		return
	}
	memberShip := GetMemberShip(ctx)
	permit, err := memberShip.CheckOwner(model.Writer, "instances", instance.ID)
	if err != nil {
		logger.Error("Failed to check owner")
		return
	}
	if !permit {
		logger.Error("Not authorized to delete the instance")
		err = fmt.Errorf("Not authorized")
		return
	}

	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if hyperID != int(instance.Hyper) {
		permit, err = memberShip.CheckAdmin(model.Admin, "instances", instance.ID)
		if !permit {
			logger.Error("Not authorized to migrate VM")
			err = fmt.Errorf("Not authorized to migrate VM")
			return
		}
		// TODO: migrate VM
	}
	if flavor != nil && flavor.ID != instance.FlavorID {
		if instance.Status == "running" {
			err = fmt.Errorf("Instance must be shutdown first before resize")
			logger.Error(err)
			return
		}
		if flavor.Disk < instance.Flavor.Disk {
			err = fmt.Errorf("Disk(s) can not be resized to smaller size")
			logger.Error("Disk(s) can not be resized to smaller size")
			return
		}
		cpu := flavor.Cpu - instance.Flavor.Cpu
		if cpu < 0 {
			cpu = 0
		}
		memory := flavor.Memory - instance.Flavor.Memory
		if memory < 0 {
			memory = 0
		}
		disk := flavor.Disk - instance.Flavor.Disk + flavor.Ephemeral - instance.Flavor.Ephemeral
		control := fmt.Sprintf("inter=%d cpu=%d memory=%d disk=%d network=%d", instance.Hyper, cpu, memory, disk, 0)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/resize_vm.sh '%d' '%d' '%d' '%d' '%d' '%d' '%d'", instance.ID, flavor.Cpu, flavor.Memory, flavor.Disk, flavor.Swap, flavor.Ephemeral, disk)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Resize vm command execution failed", err)
			return
		}
		instance.FlavorID = flavor.ID
		instance.Flavor = flavor
	}
	if instance.Hostname != hostname {
		instance.Hostname = hostname
	}
	if err = db.Model(instance).Updates(instance).Error; err != nil {
		logger.Error("Failed to save instance", err)
		return
	}
	if string(action) != "" {
		err = instanceAdmin.ChangeInstanceStatus(ctx, instance, string(action))
		if err != nil {
			logger.Error("action vm command execution failed", err)
			return
		}
	}
	return
}

func (a *InstanceAdmin) Reinstall(ctx context.Context, instance *model.Instance, image *model.Image, rootPasswd string, keys []*model.Key, cpu int32, memory int32, disk int32, loginPort int) (err error) {
	logger.Debugf("Reinstall instance %d with image %d, cpu %d, memory %d, disk %d", instance.ID, image.ID, cpu, memory, disk)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit, err := memberShip.CheckOwner(model.Writer, "instances", instance.ID)
	if err != nil {
		logger.Error("Failed to check owner")
		return
	}
	if !permit {
		logger.Error("Not authorized to reinstall the instance")
		err = fmt.Errorf("Not authorized")
		return
	}
	var bootVolume *model.Volume
	for _, volume := range instance.Volumes {
		if volume.Booting {
			bootVolume = volume
			break
		}
	}
	if bootVolume == nil {
		logger.Error("Instance has no boot volume")
		err = fmt.Errorf("Corrupted instance")
		return
	}
	imagePrefix := fmt.Sprintf("image-%d-%s", image.ID, strings.Split(image.UUID, "-")[0])
	driver := GetVolumeDriver()
	poolID := bootVolume.GetVolumePoolID()
	defaultPoolID := viper.GetString("volume.default_wds_pool_id")
	total := 0
	if driver == "local" {
		if err = db.Unscoped().Model(&model.Instance{}).Where("image_id = ?", image.ID).Count(&total).Error; err != nil {
			logger.Error("Failed to query total instances with the image", err)
			return
		}
	} else {
		if poolID != defaultPoolID {
			err = db.Where("image_id = ? and pool_id = ? and status = ?", image.ID, poolID, model.StorageStatusSynced).First(&model.ImageStorage{}).Error
			if err != nil {
				logger.Errorf("Failed to query image storage %d, %v", image.ID, err)
				err = fmt.Errorf("Image storage not found")
				return
			}
		}
		if err = db.Model(&model.Instance{}).
			Unscoped().
			Joins("LEFT JOIN volumes b ON instances.id = b.instance_id AND b.booting = ?", true).
			Where(fmt.Sprintf("b.path like '%%%s%%'", poolID)).
			Where("instances.image_id = ?", image.ID).
			Count(&total).Error; err != nil {
			logger.Error("Failed to count instances with volumes matching pool_id", err)
		}
	}
	if image.Size > int64(disk)*1024*1024*1024 {
		err = fmt.Errorf("Flavor disk size is not enough for the image")
		logger.Error(err)
		return
	}

	// change vm status to reinstalling
	if loginPort <= 0 {
		switch instance.LoginPort {
		case 22, 3389:
			if image.OSCode == "windows" {
				loginPort = 3389
			} else {
				loginPort = 22
			}
		default:
			loginPort = int(instance.LoginPort)
		}
	}
	logger.Debugf("Login Port is: %d", loginPort)

	// update security group rules
	if loginPort != int(instance.LoginPort) {
		for _, iface := range instance.Interfaces {
			err = secgroupAdmin.RemovePortForInterfaceSecgroups(ctx, instance.LoginPort, iface)
			if err != nil {
				logger.Errorf("Failed to remove security rule", err)
			}
			err = secgroupAdmin.AllowPortForInterfaceSecgroups(ctx, int32(loginPort), iface)
			if err != nil {
				logger.Errorf("Failed to create security rule", err)
				return
			}
		}
	}

	passwdLogin := false
	if rootPasswd != "" {
		passwdLogin = true
		logger.Debug("Root password login enabled")
	}
	instance.Status = "reinstalling"
	instance.LoginPort = int32(loginPort)
	instance.PasswdLogin = passwdLogin
	instance.ImageID = image.ID
	instance.Image = image
	instance.Cpu = cpu
	instance.Memory = memory
	instance.Disk = disk
	instance.Keys = keys
	if err = db.Save(&instance).Error; err != nil {
		logger.Error("Failed to save instance", err)
		return
	}
	err = db.Model(&model.Instance{}).Where("id = ?", instance.ID).Updates(map[string]interface{}{
		"flavor_id": 0,
	}).Error
	if err != nil {
		logger.Error("Failed to save instance", err)
		return
	}
	if err = db.Model(&instance).Association("Keys").Replace(keys).Error; err != nil {
		logger.Errorf("Failed to update keys association: %v", err)
		return
	}

	// change volume status to reinstalling
	bootVolume.Status = "reinstalling"
	bootVolume.Size = disk
	if err = db.Save(&bootVolume).Error; err != nil {
		logger.Error("Failed to save volume", err)
		return
	}

	// rebuild metadata
	instancePasswd := rootPasswd
	if rootPasswd != "" && image.OSCode != "windows" {
		instancePasswd, err = encrpt.Mkpasswd(rootPasswd, "sha512")
		if err != nil {
			logger.Errorf("Failed to encrypt admin password, %v", err)
			return
		}
	}
	metadata, err := a.GetMetadata(ctx, instance, instancePasswd)
	if err != nil {
		logger.Error("Failed to get instance metadata", err)
		return
	}

	snapshot := total/MaxmumSnapshot + 1 // Same snapshot reference can not be over 128, so use 96 here
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/reinstall_vm.sh '%d' '%s.%s' '%d' '%d' '%s' '%s' '%d' '%d' '%d' '%s'<<EOF\n%s\nEOF", instance.ID, imagePrefix, image.Format, snapshot, bootVolume.ID, poolID, bootVolume.GetOriginVolumeID(), cpu, memory, disk, instance.Hostname, base64.StdEncoding.EncodeToString([]byte(metadata)))
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Reinstall remote exec failed", err)
		return
	}
	return
}

func (a *InstanceAdmin) SetUserPassword(ctx context.Context, id int64, user, password string) (err error) {
	logger.Debugf("Set password for user %s of instance %d", user, id)
	ctx, db := GetContextDB(ctx)
	instance := &model.Instance{Model: model.Model{ID: id}}
	if err = db.Preload("Image").Take(instance).Error; err != nil {
		logger.Error("Failed to get instance ", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit, err := memberShip.CheckOwner(model.Writer, "instances", instance.ID)
	if err != nil {
		logger.Error("Failed to check owner")
		return
	}
	if !permit {
		logger.Error("Not authorized to set password for the instance")
		err = fmt.Errorf("Not authorized")
		return
	}
	if !instance.Image.QAEnabled {
		err = fmt.Errorf("Guest Agent is not enabled for the image of instance")
		logger.Error(err)
		return
	}
	if instance.Status != "running" {
		err = fmt.Errorf("Instance must be running")
		logger.Error(err)
		return
	}
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/set_user_passwd.sh '%d' '%s' '%s'", instance.ID, user, password)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Set password command execution failed", err)
		return
	}
	return
}

func (a *InstanceAdmin) deleteInterfaces(ctx context.Context, instance *model.Instance) (err error) {
	ctx, db := GetContextDB(ctx)
	for _, iface := range instance.Interfaces {
		err = a.deleteInterface(ctx, iface)
		if err != nil {
			logger.Error("Failed to delete interface", err)
			err = nil
			return
		}
		err = db.Model(&model.Subnet{}).Where("interface = ?", iface.ID).Updates(map[string]interface{}{
			"interface": 0}).Error
		if err != nil {
			logger.Error("Failed to update subnet", err)
			return
		}
	}
	return
}

func (a *InstanceAdmin) deleteInterface(ctx context.Context, iface *model.Interface) (err error) {
	err = DeleteInterface(ctx, iface)
	if err != nil {
		logger.Error("Failed to create interface")
		return
	}
	vlan := iface.Address.Subnet.Vlan
	control := ""
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/del_host.sh '%d' '%s' '%s'", vlan, iface.MacAddr, iface.Address.Address)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete interface failed")
		return
	}
	return
}

func (a *InstanceAdmin) createInterface(ctx context.Context, ifaceInfo *InterfaceInfo, instance *model.Instance, ifname string) (iface *model.Interface, ifaceSubnet *model.Subnet, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)

	if len(ifaceInfo.PublicIps) > 0 {
		iface, ifaceSubnet, err = DerivePublicInterface(ctx, instance, nil, ifaceInfo.PublicIps)
		if err != nil {
			logger.Error("Failed to derive primary interface", err)
			return
		}
	} else {
		subnets := ifaceInfo.Subnets
		address := ifaceInfo.IpAddress
		count := ifaceInfo.Count - 1
		mac := ifaceInfo.MacAddress
		inbound := ifaceInfo.Inbound
		outbound := ifaceInfo.Outbound
		secgroups := ifaceInfo.SecurityGroups
		allowSpoofing := ifaceInfo.AllowSpoofing
		for i, subnet := range subnets {
			if subnet.Type == "site" {
				logger.Error("Not allowed to create interface in site subnet")
				err = fmt.Errorf("Bad request")
				return
			}
			if iface == nil {
				iface, err = CreateInterface(ctx, subnet, instance.ID, memberShip.OrgID, instance.Hyper, inbound, outbound, address, mac, ifname, "instance", secgroups, allowSpoofing)
				if err == nil {
					ifaceSubnet = subnets[i]
					if subnet.Type == "public" {
						_, err = floatingIpAdmin.createDummyFloatingIp(ctx, instance, iface.Address.Address)
						if err != nil {
							logger.Error("DB failed to create dummy floating ip", err)
							return
						}
					}
					break
				} else {
					logger.Errorf("Allocate address interface from subnet %s--%s/%s failed, %v", subnet.Name, subnet.Network, subnet.Netmask, err)
				}
			}
		}
		if iface == nil {
			err = fmt.Errorf("Failed to create interface")
			return
		}
		err = interfaceAdmin.allocateSecondAddresses(ctx, instance, iface, subnets, count)
		if err != nil {
			return
		}
	}
	siteSubnets := ifaceInfo.SiteSubnets
	for _, site := range siteSubnets {
		err = db.Model(site).Updates(map[string]interface{}{"interface": iface.ID}).Error
		if err != nil {
			logger.Error("Failed to update interface", err)
			return
		}
		iface.SiteSubnets = append(iface.SiteSubnets, site)
	}
	return
}

func (a *InstanceAdmin) buildMetadata(ctx context.Context, primaryIface *InterfaceInfo, secondaryIfaces []*InterfaceInfo,
	rootPasswd string, loginPort int, keys []*model.Key, instance *model.Instance, userdata string, routerID, zoneID int64,
	service string) (interfaces []*model.Interface, metadata string, err error) {
	if rootPasswd == "" {
		logger.Debugf("Build instance metadata with primaryIface: %v, secondaryIfaces: %+v, login_port: %d, keys: %+v, instance: %+v, userdata: %s, routerID: %d, zoneID: %d, service: %s",
			primaryIface, secondaryIfaces, loginPort, keys, instance, userdata, routerID, zoneID, service)
	} else {
		logger.Debugf("Build instance metadata with primaryIface: %v, secondaryIfaces: %+v, login_port: %d, keys: %+v, instance: %+v, userdata: %s, routerID: %d, zoneID: %d, service: %s, root password: %s",
			primaryIface, secondaryIfaces, loginPort, keys, instance, userdata, routerID, zoneID, service, "******")
	}
	vlans := []*VlanInfo{}
	instNetworks := []*InstanceNetwork{}
	instLinks := []*NetworkLink{}
	primaryIP := primaryIface.IpAddress
	inbound := primaryIface.Inbound
	outbound := primaryIface.Outbound

	iface, primary, err := a.createInterface(ctx, primaryIface, instance, "eth0")
	if err != nil {
		return
	}
	vlan := iface.Address.Subnet.Vlan
	interfaces = append(interfaces, iface)
	var moreAddresses []string
	instNetworks, moreAddresses, err = GetInstanceNetworks(ctx, instance, iface, 0)
	if err != nil {
		logger.Errorf("Failed to get instance networks, %v", err)
		return
	}
	instLinks = append(instLinks, &NetworkLink{MacAddr: iface.MacAddr, Mtu: uint(iface.Mtu), ID: iface.Name, Type: "phy"})
	err = secgroupAdmin.AllowPortForInterfaceSecgroups(ctx, int32(loginPort), iface)
	if err != nil {
		logger.Error("Failed to allow login port for interface security groups ", err)
		return
	}
	securityData, err := GetSecurityData(ctx, iface.SecurityGroups)
	if err != nil {
		logger.Error("Get security data for interface failed", err)
		return
	}
	vlans = append(vlans, &VlanInfo{
		Device:        "eth0",
		Vlan:          vlan,
		Inbound:       inbound,
		Outbound:      outbound,
		AllowSpoofing: iface.AllowSpoofing,
		Gateway:       primary.Gateway,
		Router:        primary.RouterID,
		IpAddr:        iface.Address.Address,
		MacAddr:       iface.MacAddr,
		SecRules:      securityData,
		MoreAddresses: moreAddresses,
	})
	for i, ifaceInfo := range secondaryIfaces {
		var subnet *model.Subnet
		ifname := fmt.Sprintf("eth%d", i+1)
		inbound = ifaceInfo.Inbound
		outbound = ifaceInfo.Outbound
		iface, subnet, err = a.createInterface(ctx, ifaceInfo, instance, ifname)
		if err != nil {
			logger.Errorf("Allocate address for secondary subnet %s--%s/%s failed, %v", subnet.Name, subnet.Network, subnet.Netmask, err)
			return
		}
		interfaces = append(interfaces, iface)
		instNetworks, moreAddresses, err = GetInstanceNetworks(ctx, instance, iface, i+1)
		if err != nil {
			logger.Errorf("Failed to get instance networks, %v", err)
			return
		}
		instLinks = append(instLinks, &NetworkLink{MacAddr: iface.MacAddr, Mtu: uint(iface.Mtu), ID: iface.Name, Type: "phy"})
		err = secgroupAdmin.AllowPortForInterfaceSecgroups(ctx, int32(loginPort), iface)
		if err != nil {
			logger.Error("Failed to allow login port for interface security groups ", err)
			return
		}
		securityData, err = GetSecurityData(ctx, iface.SecurityGroups)
		if err != nil {
			logger.Error("Get security data for interface failed", err)
			return
		}
		vlans = append(vlans, &VlanInfo{
			Device:        ifname,
			Vlan:          subnet.Vlan,
			Inbound:       inbound,
			Outbound:      outbound,
			AllowSpoofing: iface.AllowSpoofing,
			Gateway:       subnet.Gateway,
			Router:        subnet.RouterID,
			IpAddr:        iface.Address.Address,
			MacAddr:       iface.MacAddr,
			SecRules:      securityData,
			MoreAddresses: moreAddresses,
		})
	}
	var instKeys []string
	for _, key := range keys {
		instKeys = append(instKeys, key.PublicKey)
	}
	dns := primary.NameServer
	if dns == primaryIP {
		dns = ""
	}
	instData := &InstanceData{
		Userdata:   userdata,
		DNS:        dns,
		Vlans:      vlans,
		Networks:   instNetworks,
		Links:      instLinks,
		Keys:       instKeys,
		RootPasswd: rootPasswd,
		LoginPort:  loginPort,
		OSCode:     GetImageOSCode(ctx, instance),
	}
	jsonData, err := json.Marshal(instData)
	if err != nil {
		logger.Errorf("Failed to marshal instance json data, %v", err)
		return
	}
	return interfaces, string(jsonData), nil
}

func (a *InstanceAdmin) GetMetadata(ctx context.Context, instance *model.Instance, rootPasswd string) (metadata string, err error) {
	vlans := []*VlanInfo{}
	instLinks := []*NetworkLink{}
	volumes := []*VolumeInfo{}
	instNetworks := []*InstanceNetwork{}
	var moreAddresses []string
	var instKeys []string
	for _, key := range instance.Keys {
		instKeys = append(instKeys, key.PublicKey)
	}
	for _, volume := range instance.Volumes {
		volumes = append(volumes, &VolumeInfo{
			ID:      volume.ID,
			UUID:    volume.GetOriginVolumeID(),
			Device:  volume.Target,
			Booting: volume.Booting,
		})
	}
	dns := ""
	for i, iface := range instance.Interfaces {
		subnet := iface.Address.Subnet
		if iface.PrimaryIf {
			dns = subnet.NameServer
		}
		instNetworks, moreAddresses, err = GetInstanceNetworks(ctx, instance, iface, i)
		if err != nil {
			logger.Errorf("Failed to get instance networks, %v", err)
			return
		}
		instLinks = append(instLinks, &NetworkLink{MacAddr: iface.MacAddr, Mtu: uint(iface.Mtu), ID: iface.Name, Type: "phy"})
		vlans = append(vlans, &VlanInfo{
			Device:        iface.Name,
			Vlan:          subnet.Vlan,
			Inbound:       iface.Inbound,
			Outbound:      iface.Outbound,
			AllowSpoofing: iface.AllowSpoofing,
			Gateway:       subnet.Gateway,
			Router:        subnet.RouterID,
			IpAddr:        iface.Address.Address,
			MacAddr:       iface.MacAddr,
			MoreAddresses: moreAddresses,
		})
	}
	instData := &InstanceData{
		Userdata:   instance.Userdata,
		DNS:        dns,
		Vlans:      vlans,
		Networks:   instNetworks,
		Links:      instLinks,
		Volumes:    volumes,
		Keys:       instKeys,
		RootPasswd: rootPasswd,
		LoginPort:  int(instance.LoginPort),
		OSCode:     GetImageOSCode(ctx, instance),
	}
	jsonData, err := json.Marshal(instData)
	if err != nil {
		logger.Errorf("Failed to marshal instance json data, %v", err)
		return
	}
	return string(jsonData), nil
}

func (a *InstanceAdmin) Delete(ctx context.Context, instance *model.Instance) (err error) {
	if instance.Status == "migrating" {
		err = fmt.Errorf("Instance is not in a valid state")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, instance.Owner)
	if !permit {
		logger.Error("Not authorized to delete the instance")
		err = fmt.Errorf("Not authorized")
		return
	}
	var moreAddresses []string
	for _, iface := range instance.Interfaces {
		err = secgroupAdmin.RemovePortForInterfaceSecgroups(ctx, instance.LoginPort, iface)
		if err != nil {
			logger.Error("Ignore the failure of removing login port for interface security groups ", err)
		}
		if iface.PrimaryIf {
			_, moreAddresses, err = GetInstanceNetworks(ctx, instance, iface, 0)
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
			for _, site := range iface.SiteSubnets {
				err = db.Model(site).Updates(map[string]interface{}{"interface": 0}).Error
				if err != nil {
					logger.Error("Failed to update interface", err)
				}
			}
		}
	}
	if err = db.Preload("Group").Where("instance_id = ?", instance.ID).Order("updated_at").Find(&instance.FloatingIps).Error; err != nil {
		logger.Errorf("Failed to query floating ip(s), %v", err)
		return
	}
	if instance.FloatingIps != nil {
		for _, fip := range instance.FloatingIps {
			fip.Instance = instance
			err = floatingIpAdmin.Detach(ctx, fip)
			if err != nil {
				logger.Errorf("Failed to detach floating ip, %v", err)
				return
			}
		}
		instance.FloatingIps = nil
	}
	if err = db.Where("instance_id = ?", instance.ID).Find(&instance.Volumes).Error; err != nil {
		logger.Errorf("Failed to query floating ip(s), %v", err)
		return
	}
	bootVolumeUUID := ""
	if instance.Volumes != nil {
		for _, volume := range instance.Volumes {
			if volume.Booting {
				bootVolumeUUID = volume.GetOriginVolumeID()
				// delete the boot volume directly
				if err = db.Delete(volume).Error; err != nil {
					logger.Error("DB: delete volume failed", err)
					return
				}
			} else {
				_, err = volumeAdmin.Update(ctx, volume.ID, "", 0)
				if err != nil {
					logger.Error("Failed to detach volume, %v", err)
					return
				}
			}
		}
		instance.Volumes = nil
	}
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	if instance.Hyper == -1 {
		control = "toall="
	}
	moreAddrsJson, err := json.Marshal(moreAddresses)
	if err != nil {
		logger.Errorf("Failed to marshal sites info, %v", err)
		return
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_vm.sh '%d' '%d' '%s'<<EOF\n%s\nEOF", instance.ID, instance.RouterID, bootVolumeUUID, moreAddrsJson)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete vm command execution failed ", err)
		return
	}
	instance.Status = "deleting"
	err = db.Model(instance).Updates(instance).Error
	if err != nil {
		logger.Errorf("Failed to mark vm as deleting ", err)
		return
	}
	return
}

func (a *InstanceAdmin) Get(ctx context.Context, id int64) (instance *model.Instance, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid instance ID: %d", id)
		logger.Error(err)
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	instance = &model.Instance{Model: model.Model{ID: id}}
	if err = db.Preload("Volumes").Preload("Image").Preload("Zone").Preload("Flavor").Preload("Keys").Where(where).Take(instance).Error; err != nil {
		logger.Errorf("Failed to query instance, %v", err)
		return
	}

	if err = db.Preload("Group").Preload("Subnet").Where("instance_id = ?", instance.ID).Order("updated_at").Find(&instance.FloatingIps).Error; err != nil {
		logger.Errorf("Failed to query floating ip(s), %v", err)
		return
	}
	if err = db.Preload("SiteSubnets").Preload("SiteSubnets.Group").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Where("instance = ?", instance.ID).Find(&instance.Interfaces).Error; err != nil {
		logger.Errorf("Failed to query interfaces %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, instance.Owner)
	if !permit {
		logger.Error("Not authorized to read the instance")
		err = fmt.Errorf("Not authorized")
		return
	}
	permit = memberShip.CheckPermission(model.Admin)
	if permit {
		instance.OwnerInfo = &model.Organization{Model: model.Model{ID: instance.Owner}}
		if err = db.Take(instance.OwnerInfo).Error; err != nil {
			logger.Error("Failed to query owner info", err)
			return
		}
	}

	return
}

func (a *InstanceAdmin) GetInstanceByUUID(ctx context.Context, uuID string) (instance *model.Instance, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	instance = &model.Instance{}
	if err = db.Preload("Volumes").Preload("Image").Preload("Zone").Preload("Flavor").Preload("Keys").Where(where).Where("uuid = ?", uuID).Take(instance).Error; err != nil {
		logger.Errorf("Failed to query instance, %v", err)
		return
	}

	if err = db.Preload("Group").Preload("Subnet").Where("instance_id = ?", instance.ID).Order("updated_at").Find(&instance.FloatingIps).Error; err != nil {
		logger.Errorf("Failed to query floating ip(s), %v", err)
		return
	}
	if err = db.Preload("SiteSubnets").Preload("SiteSubnets.Group").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Where("instance = ?", instance.ID).Find(&instance.Interfaces).Error; err != nil {
		logger.Errorf("Failed to query interfaces %v", err)
		return
	}
	if instance.RouterID > 0 {
		instance.Router = &model.Router{Model: model.Model{ID: instance.RouterID}}
		if err = db.Take(instance.Router).Error; err != nil {
			logger.Errorf("Failed to query floating ip(s), %v", err)
			return
		}
	}
	permit := memberShip.ValidateOwner(model.Reader, instance.Owner)
	if !permit {
		logger.Error("Not authorized to read the instance")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func GetDBIndexByInstanceUUID(c *gin.Context, uuid string) (int, error) {
	db := DB()

	var instance model.Instance
	if err := db.Model(&model.Instance{}).
		Select("id").
		Where("uuid = ?", uuid).
		First(&instance).Error; err != nil {

		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Instance not found"})
			fmt.Printf("Instance not found: %s\n", uuid)
			return -1, fmt.Errorf("instance %s not found: %w", uuid, err)
		}
		logger.Error("Database error for UUID %s: %v", uuid, err)
		return -1, fmt.Errorf("database error: %v", err)
	}

	return int(instance.ID), nil
}

func GetInstanceUUIDByDomain(ctx context.Context, domain string) (string, error) {
	// Parse domain format, example: inst-12345 -> ID=12345
	if !strings.HasPrefix(domain, "inst-") {
		return "", fmt.Errorf("invalid domain format, must start with 'inst-'")
	}

	idStr := strings.TrimPrefix(domain, "inst-")
	instanceID, err := strconv.Atoi(idStr)
	if err != nil {
		logger.Error("Domain conversion failed domain=%s error=%v", domain, err)
		return "", fmt.Errorf("invalid instance ID in domain format")
	}

	var instance model.Instance
	db := DB()
	if err := db.Where("id = ?", instanceID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Instance not found domain=%s id=%d", domain, instanceID)
			return "", fmt.Errorf("instance not found")
		}
		logger.Error("Database query failed domain=%s error=%v", domain, err)
		return "", fmt.Errorf("database error")
	}

	return instance.UUID, nil
}

func (a *InstanceAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, instances []*model.Instance, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = fmt.Errorf("Not authorized")
		return
	}
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	logger.Debugf("The query in admin console is %s", query)

	where := memberShip.GetWhere()
	instances = []*model.Instance{}
	if err = db.Model(&model.Instance{}).Where(where).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Volumes").Preload("Image").Preload("Zone").Preload("Flavor").Preload("Keys").Where(where).Where(query).Find(&instances).Error; err != nil {
		logger.Errorf("Failed to query instance(s), %v", err)
		return
	}
	db = db.Offset(0).Limit(-1)
	for _, instance := range instances {
		if err = db.Preload("SiteSubnets").Preload("SiteSubnets.Group").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
			return db.Order("addresses.updated_at")
		}).Preload("SecondAddresses.Subnet").Where("instance = ?", instance.ID).Find(&instance.Interfaces).Error; err != nil {
			logger.Errorf("Failed to query interfaces %v", err)
			return
		}

		if err = db.Preload("Group").Preload("Subnet").Order("updated_at").Where("instance_id = ?", instance.ID).Find(&instance.FloatingIps).Error; err != nil {
			logger.Errorf("Failed to query floating ip(s), %v", err)
			return
		}

		if instance.RouterID > 0 {
			instance.Router = &model.Router{Model: model.Model{ID: instance.RouterID}}
			if err = db.Take(instance.Router).Error; err != nil {
				logger.Errorf("Failed to query floating ip(s), %v", err)
				return
			}
		}
		permit := memberShip.CheckPermission(model.Admin)
		if permit {
			instance.OwnerInfo = &model.Organization{Model: model.Model{ID: instance.Owner}}
			if err = db.Take(instance.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				return
			}
		}
	}

	return
}

func (v *InstanceView) List(c *macaron.Context, store session.Store) {
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	hostname := c.QueryTrim("hostname")
	router_id := c.QueryTrim("router_id")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	queryStr := c.QueryTrim("q")
	query := queryStr
	if query != "" {
		query = fmt.Sprintf("hostname like '%%%s%%'", queryStr)
	}
	if router_id != "" {
		routerID, err := strconv.Atoi(router_id)
		if err != nil {
			logger.Debugf("Error to convert router_id to integer: %+v ", err)
		}
		query = fmt.Sprintf("router_id = %d", routerID)
	}

	total, instances, err := instanceAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Instances"] = instances
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = queryStr
	c.Data["HostName"] = hostname
	c.HTML(200, "instances")
}

func (v *InstanceView) UpdateTable(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	_, instances, err := instanceAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	var jsonData *InstancesData
	jsonData = &InstancesData{
		Instances: instances,
		IsAdmin:   memberShip.CheckPermission(model.Admin),
	}

	c.JSON(200, jsonData)
	return
}

func (v *InstanceView) Status(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["Instance"] = instance
	logger.Debugf("Instance status %+v", instance)
	c.HTML(200, "instances_status")
}

func (v *InstanceView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = instanceAdmin.Delete(ctx, instance)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "instances",
	})
	return
}

func (v *InstanceView) New(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	images := []*model.Image{}
	if err := db.Find(&images).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	flavors := []*model.Flavor{}
	if err := db.Find(&flavors).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, subnets, err := subnetAdmin.List(ctx, 0, -1, "", "", "interface = 0")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	filter := fmt.Sprintf("instance_id = 0 AND type = '%s'", PublicFloating)
	_, floatingIps, err := floatingIpAdmin.List(c.Req.Context(), 0, -1, "", "", filter)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, secgroups, err := secgroupAdmin.List(ctx, 0, -1, "", "")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, keys, err := keyAdmin.List(ctx, 0, -1, "", "")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	hypers := []*model.Hyper{}
	err = db.Where("hostid >= 0").Find(&hypers).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	zones := []*model.Zone{}
	err = db.Find(&zones).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pools := []*model.Dictionary{}
	err = db.Where("category = ?", "storage_pool").Find(&pools).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["HostName"] = c.QueryTrim("hostname")
	c.Data["Images"] = images
	c.Data["Flavors"] = flavors
	c.Data["Subnets"] = subnets
	c.Data["PublicIps"] = floatingIps
	c.Data["SecurityGroups"] = secgroups
	c.Data["Keys"] = keys
	c.Data["Hypers"] = hypers
	c.Data["Zones"] = zones
	c.Data["Pools"] = pools
	c.HTML(200, "instances_new")
}

func (v *InstanceView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	db := DB()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)

	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "instances", int64(instanceID))
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance := &model.Instance{Model: model.Model{ID: int64(instanceID)}}
	if err = db.Preload("Interfaces").Take(instance).Error; err != nil {
		logger.Error("Instance query failed", err)
		return
	}
	if err = db.Where("instance_id = ?", instanceID).Order("updated_at").Find(&instance.FloatingIps).Error; err != nil {
		logger.Errorf("Failed to query floating ip(s), %v", err)
		return
	}
	_, subnets, err := subnetAdmin.List(ctx, 0, -1, "", "", "interface = 0")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	for _, iface := range instance.Interfaces {
		for i, subnet := range subnets {
			if iface == nil || iface.Address == nil {
				continue
			}
			if subnet.ID == iface.Address.SubnetID {
				subnets = append(subnets[:i], subnets[i+1:]...)
				break
			}
		}
	}
	_, flavors, err := flavorAdmin.List(ctx, 0, -1, "", "")
	if err := db.Find(&flavors).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["Instance"] = instance
	c.Data["Subnets"] = subnets
	c.Data["Flavors"] = flavors

	flag := c.QueryTrim("flag")
	logger.Debugf("Edit instance %s with flag %s", id, flag)
	if flag == "ChangeHostname" {
		c.HTML(200, "instances_hostname")
	} else if flag == "ChangeStatus" {
		if c.QueryTrim("action") != "" {
			ctx := c.Req.Context()
			instanceID64, vmError := strconv.ParseInt(id, 10, 64)
			if vmError != nil {
				logger.Error("Change String to int64 failed", err)
				return
			}
			instance, vmError = instanceAdmin.Get(ctx, instanceID64)
			if vmError != nil {
				logger.Error("Get instance failed", err)
				return
			}
			vmError = instanceAdmin.ChangeInstanceStatus(ctx, instance, c.QueryTrim("action"))
			if vmError != nil {
				logger.Error("Instance action command execution failed", err)
				return
			}
			redirectTo := "../instances"
			c.Redirect(redirectTo)
		} else {
			c.HTML(200, "instances_status")
		}
	} else if flag == "MigrateInstance" {
		c.HTML(200, "instances_migrate")
	} else if flag == "ResizeInstance" {
		c.HTML(200, "instances_size")
	} else {
		c.HTML(200, "instances_patch")
	}
}

func (v *InstanceView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../instances"
	instanceID := c.ParamsInt64("id")
	flavorID := c.QueryInt64("flavor")
	hostname := c.QueryTrim("hostname")
	hyperID := c.QueryInt("hyper")
	action := c.QueryTrim("action")
	instance, err := instanceAdmin.Get(ctx, instanceID)
	if err != nil {
		logger.Error("Invalid instance", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var flavor *model.Flavor
	if flavorID > 0 {
		flavor, err = flavorAdmin.Get(ctx, flavorID)
		if err != nil {
			logger.Error("Invalid flavor", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	err = instanceAdmin.Update(c.Req.Context(), instance, flavor, hostname, PowerAction(action), hyperID)
	if err != nil {
		logger.Errorf("update instance failed, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *InstanceView) SetUserPassword(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/instances"
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Instance ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "instances", int64(instanceID))
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance := &model.Instance{Model: model.Model{ID: int64(instanceID)}}
	if err = db.Preload("Image").Take(instance).Error; err != nil {
		logger.Error("Instance query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Instance query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if c.Req.Method == "GET" {
		if instance.Image.QAEnabled {
			c.Data["Instance"] = instance
			c.Data["Link"] = fmt.Sprintf("/instances/%d/set_user_password", instanceID)
			c.HTML(200, "instances_user_passwd")
		} else {
			c.Data["ErrorMsg"] = "Guest Agent is not enabled for the image of instance"
			c.HTML(http.StatusBadRequest, "error")
		}
		return
	} else if c.Req.Method == "POST" {
		user := c.QueryTrim("username")
		password := c.QueryTrim("password")
		err := instanceAdmin.SetUserPassword(ctx, int64(instanceID), user, password)
		if err != nil {
			logger.Error("Set user password failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		c.Redirect(redirectTo)

	}
}

func (v *InstanceView) Reinstall(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/instances"
	db := DB()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Instance ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		logger.Error("Instance query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Instance query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if c.Req.Method == "GET" {
		images := []*model.Image{}
		if err = db.Find(&images).Error; err != nil {
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(500, "500")
			return
		}
		flavors := []*model.Flavor{}
		if err = db.Find(&flavors).Error; err != nil {
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(500, "500")
			return
		}
		keys := []*model.Key{}
		if err = db.Find(&keys).Error; err != nil {
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(500, "500")
			return
		}
		c.Data["Instance"] = instance
		c.Data["Images"] = images
		c.Data["Flavors"] = flavors
		c.Data["Keys"] = keys
		c.Data["Link"] = fmt.Sprintf("/instances/%d/reinstall", instanceID)
		c.HTML(200, "instances_reinstall")
		return
	} else if c.Req.Method == "POST" {
		imageID := c.QueryInt64("image")
		if imageID <= 0 {
			imageID = instance.ImageID
		}
		var image *model.Image
		image, err = imageAdmin.Get(ctx, imageID)
		if err != nil {
			c.Data["ErrorMsg"] = "No valid image"
			c.HTML(http.StatusBadRequest, "error")
			return
		}

		flavorID := c.QueryInt64("flavor")
		if flavorID <= 0 && instance.Cpu == 0 {
			flavorID = instance.FlavorID
		}
		cpu, memory, disk := instance.Cpu, instance.Memory, instance.Disk
		if flavorID > 0 {
			var flavor *model.Flavor
			flavor, err = flavorAdmin.Get(ctx, flavorID)
			if err != nil {
				logger.Errorf("No valid flavor", err)
				c.Data["ErrorMsg"] = "No valid flavor"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			cpu, memory, disk = flavor.Cpu, flavor.Memory, flavor.Disk
		}

		rootPasswd := c.QueryTrim("rootpasswd")
		keys := c.QueryTrim("keys")
		if rootPasswd == "" && keys == "" {
			c.Data["ErrorMsg"] = "Password or key is empty"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		var instKeys []*model.Key
		if keys != "" {
			k := strings.Split(keys, ",")
			for i := 0; i < len(k); i++ {
				kID, err := strconv.Atoi(k[i])
				if err != nil {
					logger.Error("Invalid key ID", err)
					err = nil
					continue
				}
				var key *model.Key
				key, err = keyAdmin.Get(ctx, int64(kID))
				if err != nil {
					logger.Error("Failed to access key", err)
					c.Data["ErrorMsg"] = "Failed to access key"
					c.HTML(http.StatusBadRequest, "error")
					return
				}
				instKeys = append(instKeys, key)
			}
		}
		loginPort := c.QueryInt("login_port")

		err = instanceAdmin.Reinstall(ctx, instance, image, rootPasswd, instKeys, cpu, memory, disk, loginPort)
		if err != nil {
			logger.Error("Reinstall failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		c.Redirect(redirectTo)
	}
}

func (v *InstanceView) checkNetparam(subnets []*model.Subnet, IP, mac string) (macAddr string, err error) {
	if mac != "" {
		macl := strings.Split(mac, ":")
		if len(macl) != 6 {
			logger.Errorf("Invalid mac address format: %s", mac)
			err = fmt.Errorf("Invalid mac address format: %s", mac)
			return
		}
		macAddr = strings.ToLower(mac)
		var tmp [6]int
		_, err = fmt.Sscanf(macAddr, "%02x:%02x:%02x:%02x:%02x:%02x", &tmp[0], &tmp[1], &tmp[2], &tmp[3], &tmp[4], &tmp[5])
		if err != nil {
			logger.Error("Failed to parse mac address")
			return
		}
		if tmp[0]%2 == 1 {
			logger.Error("Not a valid unicast mac address")
			err = fmt.Errorf("Not a valid unicast mac address")
			return
		}
	}
	ipOK := false
	if IP == "" {
		ipOK = true
	}
	routerID := int64(0)
	for _, subnet := range subnets {
		if routerID == 0 && subnet.RouterID > 0 {
			routerID = subnet.RouterID
		}
		if routerID != subnet.RouterID {
			err = fmt.Errorf("Subnets must be either public or belong to the same vpc")
		}
		var inNet *net.IPNet
		_, inNet, err = net.ParseCIDR(subnet.Network)
		if err != nil {
			logger.Error("CIDR parsing failed ", err)
			return
		}
		if !ipOK {
			ipOK = inNet.Contains(net.ParseIP(IP))
		}
	}
	if !ipOK {
		err = fmt.Errorf("Primary IP not belonging to any of the subnets")
		return
	}
	return
}

func (v *InstanceView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Need Write permissions")
		c.Data["ErrorMsg"] = "Need Write permissions"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../instances"
	hostname := c.QueryTrim("hostname")
	rootPasswd := c.QueryTrim("rootpasswd")
	cnt := c.QueryTrim("count")
	count, err := strconv.Atoi(cnt)
	if err != nil {
		logger.Error("Invalid instance count", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	hyperID := c.QueryInt("hyper")
	if hyperID >= 0 {
		permit := memberShip.CheckPermission(model.Admin)
		if !permit {
			logger.Error("Need Admin permissions")
			c.Data["ErrorMsg"] = "Need Admin permissions"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	loginPort := c.QueryInt("login_port")
	if loginPort > 65535 || loginPort < 0 {
		logger.Error("Invalid ssh port")
		c.Data["ErrorMsg"] = "Invalid login port"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	imageID := c.QueryInt64("image")
	if imageID <= 0 {
		logger.Error("No valid image ID", imageID)
		c.Data["ErrorMsg"] = "No valid image ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	image, err := imageAdmin.Get(ctx, imageID)
	if err != nil {
		c.Data["ErrorMsg"] = "No valid image"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	flavorID := c.QueryInt64("flavor")
	if flavorID <= 0 {
		logger.Error("Invalid flavor ID", flavorID)
		c.Data["ErrorMsg"] = "Invalid flavor ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	flavor, err := flavorAdmin.Get(ctx, flavorID)
	if err != nil {
		logger.Error("No valid flavor", err)
		c.Data["ErrorMsg"] = "No valid flavor"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	zoneID := c.QueryInt64("zone")
	zone, err := zoneAdmin.Get(ctx, zoneID)
	if err != nil {
		logger.Error("No valid zone", err)
		c.Data["ErrorMsg"] = "No valid zone"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ipAddr := ""
	primaryIP := c.QueryTrim("primaryip")
	if primaryIP != "" {
		ipAddr = strings.Split(primaryIP, "/")[0]
	}
	vlan := int64(0)
	primaryMac := c.QueryTrim("primarymac")
	var primarySubnets []*model.Subnet
	primary := c.QueryTrim("primary")
	logger.Error("primary subnets: ", primary)
	s := strings.Split(primary, ",")
	for i := 0; i < len(s); i++ {
		sID, err := strconv.Atoi(s[i])
		if err != nil {
			logger.Error("Invalid primary subnet ID", err)
			continue
		}
		var pSubnet *model.Subnet
		pSubnet, err = subnetAdmin.Get(ctx, int64(sID))
		if err != nil {
			logger.Error("Get primary subnet failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if vlan == 0 {
			vlan = pSubnet.Vlan
		} else if vlan != pSubnet.Vlan {
			logger.Error("All subnets including sites must be in the same vlan")
			c.Data["ErrorMsg"] = "All subnets including sites must be in the same vlan"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		primarySubnets = append(primarySubnets, pSubnet)
	}
	var publicAddresses []*model.FloatingIp
	publicIps := c.QueryTrim("public_ips")
	logger.Error("public ips: ", publicIps)
	f := strings.Split(publicIps, ",")
	for i := 0; i < len(f); i++ {
		fID, err := strconv.Atoi(f[i])
		if err != nil {
			logger.Error("Invalid public ip ID", err)
			continue
		}
		var floatingIp *model.FloatingIp
		floatingIp, err = floatingIpAdmin.Get(ctx, int64(fID))
		if err != nil {
			logger.Error("Get public ip failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}

		err = floatingIpAdmin.EnsureSubnetID(ctx, floatingIp)
		if err != nil {
			logger.Error("Failed to ensure subnet_id", err)
			c.Data["ErrorMsg"] = "Failed to ensure subnet_id"
			c.HTML(http.StatusBadRequest, "error")
			return
		}

		if vlan == 0 {
			vlan = floatingIp.Interface.Address.Subnet.Vlan
		} else if vlan != floatingIp.Interface.Address.Subnet.Vlan {
			logger.Error("All public ips must be in the same vlan")
			c.Data["ErrorMsg"] = "All public ips must be in the same vlan"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		publicAddresses = append(publicAddresses, floatingIp)
	}
	if len(primarySubnets) == 0 && len(publicAddresses) == 0 {
		logger.Error("Subnet(s) or public addresses for primary interface must be specified", err)
		c.Data["ErrorMsg"] = "Subnet(s) for primary interface must be specified"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	cnt = c.QueryTrim("ip_count")
	ipCount, err := strconv.Atoi(cnt)
	if err != nil {
		logger.Error("Invalid primary ip count", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var siteSubnets []*model.Subnet
	sites := c.QueryTrim("sites")
	s = strings.Split(sites, ",")
	for i := 0; i < len(s); i++ {
		sID, err := strconv.Atoi(s[i])
		if err != nil {
			logger.Error("Invalid site subnet ID", err)
			continue
		}
		var site *model.Subnet
		site, err = subnetAdmin.Get(ctx, int64(sID))
		if err != nil {
			logger.Error("Get site subnet failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if site.Interface > 0 {
			logger.Error("Site subnet is not available", err)
			c.Data["ErrorMsg"] = "Site subnet is not available"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if vlan != site.Vlan {
			logger.Error("All subnets including sites must be in the same vlan")
			c.Data["ErrorMsg"] = "All subnets including sites must be in the same vlan"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		siteSubnets = append(siteSubnets, site)
	}
	macAddr, err := v.checkNetparam(primarySubnets, ipAddr, primaryMac)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	routerID := int64(0)
	if len(primarySubnets) > 0 {
		routerID = primarySubnets[0].RouterID
	}
	secgroups := c.QueryTrim("secgroups")
	var securityGroups []*model.SecurityGroup
	if secgroups != "" {
		sg := strings.Split(secgroups, ",")
		for i := 0; i < len(sg); i++ {
			sgID, err := strconv.Atoi(sg[i])
			if err != nil {
				err = nil
				continue
			}
			var secgroup *model.SecurityGroup
			secgroup, err = secgroupAdmin.Get(ctx, int64(sgID))
			if err != nil {
				logger.Error("Get security groups failed", err)
				c.Data["ErrorMsg"] = "Get security groups failed"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			if secgroup.RouterID != routerID {
				logger.Error("Security group is not the same router with subnet")
				c.Data["ErrorMsg"] = "Security group is not in subnet vpc"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			securityGroups = append(securityGroups, secgroup)
		}
	} else {
		var sgID int64
		var secGroup *model.SecurityGroup
		if routerID > 0 {
			var router *model.Router
			router, err = routerAdmin.Get(ctx, routerID)
			if err != nil {
				logger.Error("Get router failed", err)
				c.Data["ErrorMsg"] = "Get router failed"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			sgID = router.DefaultSG
			secGroup, err = secgroupAdmin.Get(ctx, int64(sgID))
			if err != nil {
				logger.Error("Get security group failed", err)
				c.Data["ErrorMsg"] = "Get security group failed"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
		} else {
			secGroup, err = secgroupAdmin.GetDefaultSecgroup(ctx)
			if err != nil {
				logger.Error("Get default security group failed", err)
				c.Data["ErrorMsg"] = "Get security group failed"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
		}
		securityGroups = append(securityGroups, secGroup)
	}
	primaryIface := &InterfaceInfo{
		Subnets:        primarySubnets,
		IpAddress:      ipAddr,
		MacAddress:     macAddr,
		Count:          ipCount,
		PublicIps:      publicAddresses,
		SecurityGroups: securityGroups,
		SiteSubnets:    siteSubnets,
		Inbound:        1000,
		Outbound:       1000,
	}
	subnets := c.QueryTrim("subnets")
	var secondaryIfaces []*InterfaceInfo
	s = strings.Split(subnets, ",")
	for i := 0; i < len(s); i++ {
		sID, err := strconv.Atoi(s[i])
		if err != nil {
			logger.Error("Invalid secondary subnet ID", err)
			err = nil
			continue
		}
		var subnet *model.Subnet
		subnet, err = subnetAdmin.Get(ctx, int64(sID))
		if err != nil {
			logger.Error("Get secondary subnet failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if subnet.RouterID != routerID {
			logger.Error("All subnets must be in the same vpc", err)
			c.Data["ErrorMsg"] = "All subnets must be in the same vpc"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		secondaryIfaces = append(secondaryIfaces, &InterfaceInfo{
			Subnets:        []*model.Subnet{subnet},
			IpAddress:      "",
			MacAddress:     "",
			Count:          1,
			SecurityGroups: securityGroups,
			Inbound:        1000,
			Outbound:       1000,
		})
	}
	keys := c.QueryTrim("keys")
	k := strings.Split(keys, ",")
	var instKeys []*model.Key
	for i := 0; i < len(k); i++ {
		kID, err := strconv.Atoi(k[i])
		if err != nil {
			logger.Error("Invalid key ID", err)
			err = nil
			continue
		}
		var key *model.Key
		key, err = keyAdmin.Get(ctx, int64(kID))
		if err != nil {
			logger.Error("Failed to access key", err)
			c.Data["ErrorMsg"] = "Failed to access key"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		instKeys = append(instKeys, key)
	}
	nestedEnable := c.QueryBool("nested_enable")
	userdata := c.QueryTrim("userdata")
	poolID := c.QueryTrim("pool")
	_, err = instanceAdmin.Create(ctx, count, hostname, userdata, image, zone, routerID, primaryIface, secondaryIfaces, instKeys, rootPasswd, loginPort, hyperID, flavor.Cpu, flavor.Memory, flavor.Disk, nestedEnable, poolID)
	if err != nil {
		logger.Error("Create instance failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
