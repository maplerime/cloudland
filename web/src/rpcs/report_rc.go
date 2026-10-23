/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"
)

func init() {
	Add("report_rc", ReportRC)
	Add("vm_infection_result", VMInfectionResult)
}

func ReportRC(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| report_rc.sh 'cpu=12/16' 'memory=13395304/16016744' 'disk=58969763392/108580577280'
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 4 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	id := ctx.Value("hostid").(int32)
	var cpu int64
	var cpuTotal int64
	var memory int64
	var memoryTotal int64
	var disk int64
	var diskTotal int64
	var hugepages2MFree int64
	var hugepages1GFree int64
	var hugepageSizeKB int64
	var loadAvg1m float64
	var loadAvg5m float64
	var loadAvg15m float64
	cpuIdlePct := float64(100) // safe default for backward compatibility
	for _, arg := range args[1:] {
		kv := strings.Split(arg, "=")
		if len(kv) != 2 {
			logger.Error("Invalid key value pair", arg)
			return
		}
		key := kv[0]
		value := kv[1]
		switch key {
		case "cpu":
			vp := strings.Split(value, "/")
			if len(vp) == 2 {
				cpu, err = strconv.ParseInt(vp[0], 10, 64)
				cpuTotal, err = strconv.ParseInt(vp[1], 10, 64)
			}
		case "memory":
			vp := strings.Split(value, "/")
			if len(vp) == 2 {
				memory, err = strconv.ParseInt(vp[0], 10, 64)
				memoryTotal, err = strconv.ParseInt(vp[1], 10, 64)
			}
		case "disk":
			vp := strings.Split(value, "/")
			if len(vp) == 2 {
				disk, err = strconv.ParseInt(vp[0], 10, 64)
				diskTotal, err = strconv.ParseInt(vp[1], 10, 64)
			}
		case "hugepages_2m":
			vp := strings.Split(value, "/")
			if len(vp) == 2 {
				hugepages2MFree, _ = strconv.ParseInt(vp[0], 10, 64)
			}
		case "hugepages_1g":
			vp := strings.Split(value, "/")
			if len(vp) == 2 {
				hugepages1GFree, _ = strconv.ParseInt(vp[0], 10, 64)
			}
		case "hugepage_size_kb":
			hugepageSizeKB, _ = strconv.ParseInt(value, 10, 64)
		case "load":
			vp := strings.Split(value, "/")
			if len(vp) == 3 {
				loadAvg1m, _ = strconv.ParseFloat(vp[0], 64)
				loadAvg5m, _ = strconv.ParseFloat(vp[1], 64)
				loadAvg15m, _ = strconv.ParseFloat(vp[2], 64)
			}
		case "cpu_idle":
			cpuIdlePct, _ = strconv.ParseFloat(value, 64)
		case "hive_status":
			source := id
			if actual, ok := ctx.Value("source_hyper").(int32); ok && actual >= 0 {
				source = actual
			}
			if hiveErr := routes.SaveHiveNodeStatus(ctx, source, value); hiveErr != nil {
				logger.Errorf("[HIVE] report_rc node=%d status rejected: %v", source, hiveErr)
			}
		case "network":
			// accepted but not stored
		default:
			logger.Error("Undefined resource type", key)
		}
		if err != nil {
			logger.Error("Failed to get value", err)
		}
	}
	resource := &model.Resource{
		Hostid:          id,
		Cpu:             cpu,
		CpuTotal:        cpuTotal,
		Memory:          memory,
		MemoryTotal:     memoryTotal,
		Disk:            disk,
		DiskTotal:       diskTotal,
		Hugepages2MFree: hugepages2MFree,
		Hugepages1GFree: hugepages1GFree,
		HugepageSizeKB:  hugepageSizeKB,
		LoadAvg1m:       loadAvg1m,
		LoadAvg5m:       loadAvg5m,
		LoadAvg15m:      loadAvg15m,
		CpuIdlePct:      cpuIdlePct,
	}
	err = db.Where("hostid = ?", id).Assign(resource).FirstOrCreate(&model.Resource{}).Error
	if err != nil {
		logger.Error("Failed to create or update hyper resource", err)
		return
	}
	// GORM v1 skips zero-value fields when updating structs. Hugepage free values can
	// legitimately be 0, so force-write these fields using map updates.
	err = db.Model(&model.Resource{}).Where("hostid = ?", id).Updates(map[string]interface{}{
		"hugepages2_m_free": hugepages2MFree,
		"hugepages1_g_free": hugepages1GFree,
		"hugepage_size_kb":  hugepageSizeKB,
	}).Error
	if err != nil {
		logger.Error("Failed to update hypervisor hugepage metrics", err)
	}
	if cpu == 0 || memory == 0 || disk == 0 {
		err = db.Model(&model.Resource{}).Where("hostid = ?", id).Updates(map[string]interface{}{
			"cpu":    cpu,
			"memory": memory,
			"disk":   disk,
		}).Error
		if err != nil {
			logger.Error("Failed to update hypervisor resource", err)
		}
	}
	// Always update load/idle fields since zero values are valid
	err = db.Model(&model.Resource{}).Where("hostid = ?", id).Updates(map[string]interface{}{
		"load_avg1m":   loadAvg1m,
		"load_avg5m":   loadAvg5m,
		"load_avg15m":  loadAvg15m,
		"cpu_idle_pct": cpuIdlePct,
	}).Error
	if err != nil {
		logger.Error("Failed to update hypervisor load metrics", err)
	}
	return
}

func VMInfectionResult(ctx context.Context, args []string) (string, error) {
	if len(args) != 6 {
		return "", fmt.Errorf("wrong infection result parameters")
	}
	source, ok := ctx.Value("source_hyper").(int32)
	if !ok || source < 0 {
		return "", fmt.Errorf("missing infection result source")
	}
	logger.Debugf("[VM-INFECTION] report_rc source=%d fragment received", source)
	seq, err := strconv.Atoi(args[2])
	if err != nil {
		return "", err
	}
	total, err := strconv.Atoi(args[3])
	if err != nil {
		return "", err
	}
	err = routes.ReceiveVMInfectionChunk(ctx, source, routes.VMInfectionChunkInput{InstanceUUID: args[1], Seq: seq, Total: total, SHA256: args[4], Payload: args[5]})
	if err != nil {
		return "", err
	}
	return "OK", nil
}
