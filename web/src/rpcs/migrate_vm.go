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

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("migrate_vm", MigrateVM)
}

type VolumeInfo struct {
	ID      int64  `json:"id"`
	UUID    string `json:"uuid"`
	Device  string `json:"device"`
	Booting bool   `json:"booting"`
}

func execSourceMigrate(ctx context.Context, instance *model.Instance, migration *model.Migration, taskID int64, migrationScript, migrationType string) (err error) {
	ctx, db := GetContextDB(ctx)
	targetHyper := &model.Hyper{}
	err = db.Where("hostid = ?", migration.TargetHyper).Take(targetHyper).Error
	if err != nil {
		logger.Error("Failed to query target hyper", err)
		return
	}
	sourceHyper := &model.Hyper{}
	err = db.Where("hostid = ?", migration.SourceHyper).Take(sourceHyper).Error
	if err != nil {
		logger.Error("Failed to query source hyper", err)
		return
	}
	volumes := []*VolumeInfo{}
	if len(instance.Volumes) == 0 {
		err = db.Where("instance_id = ?", instance.ID).Find(&instance.Volumes).Error
		if err != nil {
			logger.Error("Failed to query source hyper", err)
			return
		}
	}
	for _, volume := range instance.Volumes {
		volumes = append(volumes, &VolumeInfo{
			ID:      volume.ID,
			UUID:    volume.GetOriginVolumeID(),
			Device:  volume.Target,
			Booting: volume.Booting,
		})
	}
	volumesJson, err := json.Marshal(volumes)
	if err != nil {
		logger.Error("Failed to marshal instance json data", err)
		return
	}
	if sourceHyper.Status != 10 {
		control := fmt.Sprintf("inter=%d", migration.SourceHyper)
		command := fmt.Sprintf("%s '%d' '%d' '%d' '%d' '%s' '%s' <<EOF\n%s\nEOF", migrationScript, migration.ID, taskID, instance.ID, instance.RouterID, targetHyper.Hostname, migrationType, volumesJson)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Source migration command execution failed", err)
			return
		}
	} else {
		err = fmt.Errorf("Source hyper is not in a valid state")
		return
	}
	return
}

func MigrateVM(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| migrate_vm.sh '12' '2' '127' '3' 'state'
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 5 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	migrationID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Error("Invalid migration ID", err)
		return
	}
	taskID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Error("Invalid task ID", err)
		return
	}
	instID, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	hyperID, err := strconv.Atoi(args[4])
	if err != nil {
		logger.Error("Invalid hyper ID", err)
		return
	}
	status = args[5]
	taskStatus := status
	message := ""
	if len(args) > 6 {
		message = args[6]
	}
	migration := &model.Migration{Model: model.Model{ID: migrationID}}
	err = db.Model(migration).Take(migration).Error
	if err != nil {
		logger.Error("Failed to get migration record", err)
		return
	}
	instance := &model.Instance{Model: model.Model{ID: instID}}
	err = db.Preload("Volumes").Take(instance).Error
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	errHndl := ctx.Value("error")
	if errHndl != nil {
		reason := "Resource is not enough"
		err = db.Model(instance).Updates(map[string]interface{}{
			"status": "rollback",
			"reason": reason}).Error
		if err != nil {
			logger.Error("Failed to update instance", err)
		}
		err = db.Model(migration).Update(map[string]interface{}{"status": "failed"}).Error
		if err != nil {
			logger.Error("Failed to update migration", err)
		}
		return
	}

	// Use defer to handle status updates, ensuring both migration and task status
	// are set to "failed" if any error occurs during the function execution.
	defer func() {
		if status != "failed" {
			taskStatus = "completed"
		}
		migration.Status = status
		if err != nil {
			taskStatus = "failed"
			err = db.Model(&model.Task{}).Where("id = ?", taskID).Update(map[string]interface{}{"status": taskStatus, "message": err.Error()}).Error
		} else {
			err = db.Model(&model.Task{}).Where("id = ?", taskID).Update(map[string]interface{}{"status": taskStatus, "message": message}).Error
		}
		err = db.Model(migration).Update(map[string]interface{}{"status": status}).Error
		if err != nil {
			logger.Error("Failed to update migration", err)
		}
	}()

	if status == "completed" {
		err = db.Model(&model.Instance{Model: model.Model{ID: instID}}).Updates(map[string]interface{}{"status": model.InstanceStatusMigrated}).Error
		if err != nil {
			logger.Error("Failed to update instance status to unknown, %v", err)
			return
		}
		_, err = LaunchVM(ctx, []string{args[0], args[3], "migrated", args[4], "sync"})
		if err != nil {
			logger.Error("Failed to sync vm info", err)
			return
		}
		err = execSourceMigrate(ctx, instance, migration, taskID, "/opt/cloudland/scripts/backend/finish_source_migration.sh", migration.Type)
		if err != nil {
			logger.Error("Failed to exec finish source migration", err)
			return
		}
	} else if status == "rollback" {
		err = execSourceMigrate(ctx, instance, migration, taskID, "/opt/cloudland/scripts/backend/rollback_source_migration.sh", migration.Type)
		if err != nil {
			logger.Error("Failed to exec finish source migration", err)
			return
		}
	} else if status == "source_rollback" {
		err = db.Model(&model.Instance{Model: model.Model{ID: instID}}).Updates(map[string]interface{}{"status": model.InstanceStatusRollback}).Error
		if err != nil {
			logger.Error("Failed to update instance status to unknown, %v", err)
			return
		}
		task3 := &model.Task{
			Name:    "Source_Rollback",
			Mission: migration.ID,
			Summary: "Clean up target hypervisor",
			Status:  "in_progress",
		}
		err = db.Model(task3).Create(task3).Error
		if err != nil {
			logger.Error("Failed to create task2", err)
			return
		}
		control := fmt.Sprintf("inter=%d", migration.TargetHyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_target_migration.sh '%d' '%d' '%d'", migration.ID, task3.ID, instance.ID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Execute clear target failed", err)
			return
		}
	} else if status == "failed" {
		err = db.Model(&model.Instance{Model: model.Model{ID: instID}}).Updates(map[string]interface{}{"status": model.InstanceStatusUnknown}).Error
		if err != nil {
			logger.Error("Failed to update instance status to unknown, %v", err)
			return
		}
	} else if status == "target_prepared" {
		if migration.TargetHyper == -1 {
			migration.TargetHyper = int32(hyperID)
			err = db.Model(migration).Update(map[string]interface{}{"target_hyper": hyperID}).Error
			if err != nil {
				logger.Error("Failed to update migration", err)
				return
			}
		}
		targetHyper := &model.Hyper{}
		err = db.Where("hostid = ?", hyperID).Take(targetHyper).Error
		if err != nil {
			logger.Error("Failed to query hyper", err)
			return
		}
		task2 := &model.Task{
			Name:    "Prepare_Source",
			Mission: migration.ID,
			Summary: "Prepare resources on source hypervisor",
			Status:  "in_progress",
		}
		err = db.Model(task2).Create(task2).Error
		if err != nil {
			logger.Error("Failed to create task2", err)
			return
		}
		err = execSourceMigrate(ctx, instance, migration, task2.ID, "/opt/cloudland/scripts/backend/source_migration.sh", migration.Type)
		if err != nil {
			logger.Error("Failed to exec source migration", err)
			err = db.Model(&model.Task{}).Where("id = ?", task2.ID).Update(map[string]interface{}{"status": "failed", "message": err.Error()}).Error
			return
		}
	} else if status == "source_prepared" {
		err = db.Preload("SiteSubnets").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("Address.Subnet.Router").Where("instance = ?", instID).Find(&instance.Interfaces).Error
		if err != nil {
			logger.Error("Failed to get interfaces", err)
			return
		}
		var primaryIface *model.Interface
		for i, iface := range instance.Interfaces {
			if iface.PrimaryIf {
				primaryIface = instance.Interfaces[i]
				break
			}
		}
		err = db.Where("instance_id = ? and type = ?", instance.ID, PublicFloating).Find(&instance.FloatingIps).Error
		if err != nil {
			logger.Errorf("Failed to query floating ip(s), %v", err)
			return
		}
		if instance.RouterID > 0 && instance.FloatingIps != nil {
			for _, fip := range instance.FloatingIps {
				control := fmt.Sprintf("inter=%d", migration.SourceHyper)
				command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_floating.sh '%d' '%s' '%s' '%d' '%d'", fip.RouterID, fip.FipAddress, fip.IntAddress, primaryIface.Address.Subnet.Vlan, fip.ID)
				err = HyperExecute(ctx, control, command)
				if err != nil {
					logger.Error("Execute clear floating ip failed", err)
					return
				}
			}
		}
		if len(primaryIface.SiteSubnets) > 0 || len(primaryIface.SecondAddresses) > 0 {
			var moreAddresses []string
			_, moreAddresses, err = GetInstanceNetworks(ctx, instance, []*model.Interface{primaryIface})
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
			var oldAddrsJson []byte
			oldAddrsJson, err = json.Marshal(moreAddresses)
			if err != nil {
				logger.Errorf("Failed to marshal second addresses json data, %v", err)
				return
			}
			control := fmt.Sprintf("inter=%d", migration.SourceHyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_second_ips.sh '%d' '%s' '%s'<<EOF\n%s\nEOF", instance.ID, primaryIface.MacAddr, GetImageOSCode(ctx, instance), oldAddrsJson)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Execute clear second ips failed", err)
				return
			}
		}
		control := fmt.Sprintf("inter=%d", migration.TargetHyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/complete_migration.sh '%d' '%d' '%d' '%s'", migration.ID, taskID, instance.ID, migration.Type)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Execute clear target failed", err)
			return
		}
	}
	logger.Errorf("Migration condition: %s, new status: %s", migration.Status, status)
	return
}
