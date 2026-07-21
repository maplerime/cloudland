/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
	"web/src/scheduler"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	migrationAdmin = &MigrationAdmin{}
	migrationView  = &MigrationView{}
)

type MigrationAdmin struct{}
type MigrationView struct{}

func (a *MigrationAdmin) Create(ctx context.Context, name string, instances []*model.Instance, force bool, tgtHyper int32) (migrations []*model.Migration, err error) {
	logger.Debugf("Start migrating instances to %d, migration type %t", tgtHyper, force)
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if tgtHyper > -1 {
		targetHyper := &model.Hyper{}
		err = db.Where("hostid = ?", tgtHyper).Take(targetHyper).Error
		if err != nil {
			logger.Error("Failed to query hyper", err)
			err = NewCLError(ErrHypervisorNotFound, "Failed to find target hypervisor", err)
			return
		}
		if targetHyper.Status == 10 {
			err = NewCLError(ErrHypervisorInvalidState, "Target hypervisor is in wrong state", nil)
			logger.Error("Target hypervisor is in wrong state")
			return
		}
	}
	// Sort instances by ID before locking so that any two concurrent batch requests
	// acquire per-instance row locks in the same order, preventing lock-ordering deadlock.
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ID < instances[j].ID
	})
	for _, instance := range instances {
		// Row-lock the instance to serialize concurrent migration-create attempts for the
		// same instance. The instance row always exists, so it is the reliable mutex; a lock
		// on the migration table cannot block the first-ever migration (no row to lock yet).
		lockedInstance := &model.Instance{}
		err = db.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", instance.ID).Take(lockedInstance).Error
		if err != nil {
			logger.Errorf("Failed to lock instance %d for migration, %v", instance.ID, err)
			err = NewCLError(ErrInstanceNotFound, "Failed to lock instance for migration", err)
			return
		}
		// The instance passed from the API layer is a stale read (GetInstanceByUUID, no lock).
		// Refresh the volatile fields from the locked row before validating and dispatching.
		instance.Status = lockedInstance.Status
		instance.Hyper = lockedInstance.Hyper
		// Re-validate migratable state against the freshly-locked row.
		if instance.Status != model.InstanceStatusShutoff && instance.Status != model.InstanceStatusRunning && instance.Status != model.InstanceStatusPaused {
			logger.Infof("Skip instance %d: status %s is not migratable", instance.ID, instance.Status)
			continue
		}
		// Authoritative duplicate check: is there already a non-terminal migration for this
		// instance? Use the migration table (not instance.status, which is set late and reused
		// by resize). Terminal states = completed/failed/timeout/not_doing.
		var inflightCount int64
		err = db.Model(&model.Migration{}).
			Where("instance_id = ? AND status NOT IN (?)", instance.ID,
				[]string{"completed", "failed", "timeout", "not_doing"}).
			Count(&inflightCount).Error
		if err != nil {
			logger.Errorf("Failed to check in-flight migration for instance %d, %v", instance.ID, err)
			err = NewCLError(ErrMigrationCreateFailed, "Failed to check existing migration", err)
			return
		}
		if inflightCount > 0 {
			// User decision: skip this instance and keep migrating the rest (do not fail the
			// whole batch). ErrMigrationInProgress (111805) documents this condition.
			logger.Infof("Skip instance %d: %s", instance.ID, ErrMigrationInProgress)
			continue
		}
		sourceHyper := &model.Hyper{Hostid: instance.Hyper}
		err = db.Where(sourceHyper).Take(sourceHyper).Error
		if err != nil {
			logger.Error("Failed to query hyper", err)
			err = NewCLError(ErrHypervisorNotFound, "Failed to query source hypervisor", err)
			return
		}
		status := "in_progress"
		migrationType := "cold"
		if sourceHyper.Status != 10 && !force {
			migrationType = "warm"
		}
		if instance.Hyper == tgtHyper {
			logger.Error("No need to migrate if source and target hypervisors are the same")
			continue
		}
		task1 := &model.Task{
			Name:    "Prepare_Target",
			Summary: "Prepare resources on target hypervisor",
			Status:  model.TaskStatus(status),
		}
		migration := &model.Migration{
			Model:       model.Model{Creater: memberShip.UserID},
			Name:        name,
			InstanceID:  instance.ID,
			Type:        migrationType,
			Force:       force,
			SourceHyper: instance.Hyper,
			TargetHyper: tgtHyper,
			Phases:      []*model.Task{task1},
			Status:      status,
		}
		migration.Instance = instance
		logger.Debugf("Creating migration %+v", migration)
		err = db.Create(migration).Error
		if err != nil {
			logger.Error("DB create migration failed, %v", err)
			err = NewCLError(ErrMigrationCreateFailed, "DB create migration failed", err)
			return
		}
		var metadata string
		metadata, err = instanceAdmin.GetMetadata(ctx, instance, "")
		if err != nil {
			logger.Error("Failed to get metadata")
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
			err = NewCLError(ErrBootVolumeNotFound, "Instance has no boot volume", nil)
			return
		}
		poolID := bootVolume.GetVolumePoolID()
		control := fmt.Sprintf("inter=%d", tgtHyper)
		if tgtHyper == -1 {
			// Try placement scheduler first, fallback to GetHyperGroup (SCI select=)
			reqHugepageSizeKB := scheduler.ResolveRequestHugepageSizeKB(instance.ZoneID)
			selectedHyperID, schedErr := scheduler.SelectHost(ctx, &scheduler.PlacementRequest{
				VCPUs:          instance.Cpu,
				MemMB:          int64(instance.Memory),
				DiskGB:         int64(instance.Disk),
				ZoneID:         instance.ZoneID,
				ExcludeHypers:  []int32{instance.Hyper},
				HugepageSizeKB: reqHugepageSizeKB,
			})
			if schedErr != nil {
				markTaskFail := true
				if clErr, ok := schedErr.(*CLError); ok && clErr.Code == ErrPlacementDisabled {
					// Placement intentionally disabled for this zone — expected, not an error
					logger.Infof("Placement disabled for zone %d, using GetHyperGroup for migration of instance %d",
						instance.ZoneID, instance.ID)
					var hyperGroup string
					hyperGroup, err = GetHyperGroup(ctx, instance.ZoneID, instance.Hyper)
					if err != nil {
						logger.Errorf("GetHyperGroup failed for migration of instance %d, task %d will be marked not_doing: %v",
							instance.ID, migration.ID, err)
					} else {
						rcNeeded := fmt.Sprintf("cpu=%d memory=%d disk=%d network=%d", instance.Cpu, instance.Memory*1024, int64(instance.Disk)*1024*1024, 0)
						control = "select=" + hyperGroup + " " + rcNeeded
						markTaskFail = false
					}
				} else {
					logger.Warningf("Scheduler failed for migration of instance %d, task %d will be marked not_doing: %v",
						instance.ID, migration.ID, schedErr)
				}
				if markTaskFail {
					task1.Summary = "No qualified target"
					task1.Status = "not_doing"
					migration.Status = "not_doing"
					mErr := db.Model(&model.Migration{}).Where("id = ?", migration.ID).Updates(map[string]interface{}{
						"status": migration.Status,
					}).Error
					if mErr != nil {
						logger.Error("Failed to update save migration, %v", mErr)
						err = NewCLError(ErrMigrationUpdateFailed, "Failed to update migration", mErr)
						return
					}
					err = nil
					continue
				}
			} else {
				migration.TargetHyper = selectedHyperID
				control = fmt.Sprintf("inter=%d", selectedHyperID)
				logger.Infof("Scheduler selected hyper %d for migration of instance %d", selectedHyperID, instance.ID)
			}
		}
		err = db.Model(instance).Update("status", model.InstanceStatusMigrating).Error
		if err != nil {
			logger.Error("Failed to update instance status to migrating, %v", err)
			err = NewCLError(ErrInstanceUpdateFailed, "Failed to update instance status", err)
			return
		}
		cpu := instance.Cpu
		memory := instance.Memory
		disk := instance.Disk
		flavor := instance.Flavor
		if flavor != nil {
			cpu = flavor.Cpu
			memory = flavor.Memory
			disk = flavor.Disk
		}
		bootLoader := "bios"
		if instance.Image != nil {
			bootLoader = instance.Image.BootLoader
		}
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/target_migration.sh '%d' '%d' '%d' '%s' '%d' '%d' '%d' '%s' '%s' '%s' '%s' '%s'<<EOF\n%s\nEOF", migration.ID, task1.ID, instance.ID, instance.Hostname, cpu, memory, disk, sourceHyper.Hostname, migrationType, bootLoader, poolID, instance.UUID, base64.StdEncoding.EncodeToString([]byte(metadata)))
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Target migration command execution failed", err)
			return
		}
		migrations = append(migrations, migration)
	}
	return
}

func (a *MigrationAdmin) GetMigrationByUUID(ctx context.Context, uuID string) (migration *model.Migration, err error) {
	ctx, db := GetContextDB(ctx)
	migration = &model.Migration{}
	err = db.Preload("Instance").Preload("Phases").Where("uuid = ?", uuID).Take(migration).Error
	if err != nil {
		logger.Error("Failed to query migration, %v", err)
		err = NewCLError(ErrMigrationNotFound, "Failed to find migration", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to get migration")
		err = NewCLError(ErrPermissionDenied, "Not authorized to get migration", nil)
		return
	}
	return
}

func (a *MigrationAdmin) GetMigrationByName(ctx context.Context, name string) (migration *model.Migration, err error) {
	ctx, db := GetContextDB(ctx)
	migration = &model.Migration{}
	err = db.Where("name = ?", name).Take(migration).Error
	if err != nil {
		logger.Error("Failed to query migration, %v", err)
		err = NewCLError(ErrMigrationNotFound, "Failed to find migration", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to get migration")
		err = NewCLError(ErrPermissionDenied, "Not authorized to get migration", nil)
		return
	}
	return
}

func (a *MigrationAdmin) Get(ctx context.Context, id int64) (migration *model.Migration, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid migration ID: %d", id)
		logger.Error(err)
		return
	}
	ctx, db := GetContextDB(ctx)
	migration = &model.Migration{Model: model.Model{ID: id}}
	err = db.Take(migration).Error
	if err != nil {
		logger.Error("DB failed to query migration, %v", err)
		err = NewCLError(ErrMigrationNotFound, "Failed to find migration", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to get migration")
		err = NewCLError(ErrPermissionDenied, "Not authorized to get migration", nil)
		return
	}
	return
}

func (a *MigrationAdmin) GetMigration(ctx context.Context, reference *BaseReference) (migration *model.Migration, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = fmt.Errorf("Migration base reference must be provided with either uuid or name")
		return
	}
	if reference.ID != "" {
		migration, err = a.GetMigrationByUUID(ctx, reference.ID)
		return
	}
	if reference.Name != "" {
		migration, err = a.GetMigrationByName(ctx, reference.Name)
		return
	}
	return
}

func (a *MigrationAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, migrations []*model.Migration, err error) {
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	migrations = []*model.Migration{}
	if err = db.Model(&model.Migration{}).Where(query).Count(&total).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to count migrations", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Instance").Preload("Phases").Where(query).Find(&migrations).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to query migrations", err)
		return
	}

	return
}

func (v *MigrationView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	// Get pagination parameters
	listConfig, offset, limit := GetPaginationParams(c, "migrations")

	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, migrations, err := migrationAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusInternalServerError)
		return
	}
	c.Data["Migrations"] = migrations
	c.Data["Query"] = query
	SetPaginationData(c, "migrations", total, limit, offset, listConfig,
		`["ID", "Name", "CreatedAt", "UpdatedAt", "Instance", "Source Hyper", "Target Hyper", "Force", "Phases", "Status"]`,
		[]string{"ID", "UUID", "Name", "CreatedAt", "UpdatedAt", "Instance", "Source Hyper", "Target Hyper", "Force", "Phases", "Status"})

	c.HTML(200, "migrations")
}

func (v *MigrationView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, instances, err := instanceAdmin.List(c.Req.Context(), 0, -1, "", "")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusInternalServerError)
		return
	}
	hypers := []*model.Hyper{}
	err = DB().Where("hostid >= 0").Find(&hypers).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["Instances"] = instances
	c.Data["Hypers"] = hypers
	c.HTML(200, "migrations_new")
}

func (v *MigrationView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../migrations"
	name := c.QueryTrim("name")
	instList := c.QueryTrim("instances")
	var instances []*model.Instance
	instArray := strings.Split(instList, ",")
	for _, inst := range instArray {
		instID, err := strconv.Atoi(inst)
		if err != nil {
			logger.Error("Invalid instance ID", err)
			err = nil
			continue
		}
		var instance *model.Instance
		instance, err = instanceAdmin.Get(ctx, int64(instID))
		if err != nil {
			logger.Error("Failed to get instance", err)
			c.Data["ErrorMsg"] = "Failed to get instance"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		instances = append(instances, instance)
	}
	tgthyper := c.QueryInt("hyper")
	forceStr := c.QueryTrim("force")
	force := false
	if forceStr == "yes" {
		force = true
	}
	_, err := migrationAdmin.Create(ctx, name, instances, force, int32(tgthyper))
	if err != nil {
		logger.Error("Create migration failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
