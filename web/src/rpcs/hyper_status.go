/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"fmt"
	"strconv"

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("hyper_status", HyperStatus)
}

func HyperStatus(ctx context.Context, args []string) (status string, err error) {
	//"|:-COMMAND-:| hyper_status.sh '$SCI_CLIENT_ID' '$HOSTNAME' '$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state' '$vtep_ip' '$ZONE_NAME' '$cpu_over_rate' '$mem_over_rate' '$disk_over_rate' '$cpu_model'"
	db := DB()
	argn := len(args)
	if argn < 15 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	hyperID, err := strconv.Atoi(args[1])
	if err != nil || hyperID < 0 {
		logger.Error("Invalid hypervisor ID", err)
		return
	}
	hyperName := args[2]
	availCpu, err := strconv.Atoi(args[3])
	if err != nil {
		logger.Error("Invalid available cpu", err)
		availCpu = 0
	}
	totalCpu, err := strconv.Atoi(args[4])
	if err != nil {
		logger.Error("Invalid total cpu", err)
		totalCpu = 0
	}
	availMem, err := strconv.Atoi(args[5])
	if err != nil {
		logger.Error("Invalid available memory", err)
		availMem = 0
	}
	totalMem, err := strconv.Atoi(args[6])
	if err != nil {
		logger.Error("Invalid total memory", err)
		totalMem = 0
	}
	availDisk, err := strconv.Atoi(args[7])
	if err != nil {
		logger.Error("Invalid available disk", err)
		availDisk = 0
	}
	totalDisk, err := strconv.Atoi(args[8])
	if err != nil {
		logger.Error("Invalid total disk", err)
		totalDisk = 0
	}
	hyperStatus, err := strconv.Atoi(args[9])
	if err != nil {
		logger.Error("Invalid hypervisor status", err)
		hyperStatus = 1
	}
	hostIP := args[10]
	zoneName := args[11]
	zone := &model.Zone{Name: zoneName}
	if zoneName != "" {
		err = db.Where("name = ?", zoneName).FirstOrCreate(zone).Error
		if err != nil {
			logger.Error("Failed to create zone", err)
			return
		}
	}
	hyper := &model.Hyper{Hostid: int32(hyperID)}
	err = db.Where("hostid = ?", hyperID).Take(hyper).Error
	if err != nil {
		logger.Error("Failed to take hyper", err)
		err = db.Create(hyper).Error
		if err != nil {
			logger.Error("Failed to create hyper", err)
			return
		}
	}
	// PET-769 should maintain the hypervisor's over commit rates in admin console and admin API
	// args 12 cpu_over_rate, args 13 mem_over_rate, args 14 disk_over_rate are float values
	cpuOverRate, err := strconv.ParseFloat(args[12], 32)
	if err != nil {
		logger.Error("Invalid cpu over rate", err)
	} else {
		hyper.CpuOverRate = float32(cpuOverRate)
	}
	memOverRate, err := strconv.ParseFloat(args[13], 32)
	if err != nil {
		logger.Error("Invalid memory over rate", err)
	} else {
		hyper.MemOverRate = float32(memOverRate)
	}
	diskOverRate, err := strconv.ParseFloat(args[14], 32)
	if err != nil {
		logger.Error("Invalid disk over rate", err)
	} else {
		hyper.DiskOverRate = float32(diskOverRate)
	}
	cpuModel := args[15]
	// end PET-769
	hyper.Hostname = hyperName
	hyper.Status = int32(hyperStatus)
	hyper.VirtType = "kvm-x86_64"
	hyper.CpuModel = cpuModel
	hyper.Zone = zone
	hyper.HostIP = hostIP

	updateFields := make(map[string]interface{})
	updateFields["cpu_over_rate"] = hyper.CpuOverRate
	updateFields["mem_over_rate"] = hyper.MemOverRate
	updateFields["disk_over_rate"] = hyper.DiskOverRate
	updateFields["hostname"] = hyper.Hostname
	updateFields["status"] = hyper.Status
	updateFields["virt_type"] = hyper.VirtType
	updateFields["zone_id"] = hyper.ZoneID
	updateFields["host_ip"] = hyper.HostIP

	err = db.Model(hyper).Updates(updateFields).Error
	if err != nil {
		logger.Error("Failed to save hypervisor", err)
		return
	}
	resource := &model.Resource{
		Hostid:      int32(hyperID),
		Cpu:         int64(availCpu),
		CpuTotal:    int64(totalCpu),
		Memory:      int64(availMem),
		MemoryTotal: int64(totalMem),
		Disk:        int64(availDisk),
		DiskTotal:   int64(totalDisk),
	}
	err = db.Where("hostid = ?", hyperID).Assign(resource).FirstOrCreate(&model.Resource{}).Error
	if err != nil {
		logger.Error("Failed to create or update hyper resource", err)
		return
	}
	if availCpu == 0 || availMem == 0 || availDisk == 0 {
		err = db.Model(&model.Resource{}).Where("hostid = ?", hyperID).Updates(map[string]interface{}{
			"cpu":    availCpu,
			"memory": availMem,
			"disk":   availDisk}).Error
		if err != nil {
			logger.Error("Failed to update hypervisor resource", err)
		}
	}
	if hyper.RouteIP == "" {
		_, err = SystemRouter(ctx, []string{args[0], args[1], args[2]})
		if err != nil {
			logger.Error("Failed to create system router", err)
		}
	}
	return
}
