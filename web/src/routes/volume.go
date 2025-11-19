/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"github.com/spf13/viper"
	macaron "gopkg.in/macaron.v1"
)

var (
	volumeAdmin = &VolumeAdmin{}
	volumeView  = &VolumeView{}
)

type VolumeAdmin struct{}
type VolumeView struct{}

func GetVolumeDriver() (driver string) {
	if viper.IsSet("volume.driver") {
		driver = viper.GetString("volume.driver")
	} else {
		driver = "local"
	}
	return
}

func (a *VolumeAdmin) Get(ctx context.Context, id int64) (volume *model.Volume, err error) {
	if id <= 0 {
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid volume ID: %d", id), nil)
		logger.Error(err)
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	volume = &model.Volume{Model: model.Model{ID: id}}
	if err = db.Preload("Instance").Where(where).Take(volume).Error; err != nil {
		logger.Error("Failed to query volume, %v", err)
		err = NewCLError(ErrVolumeNotFound, "Failed to query volume", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, volume.Owner)
	if !permit {
		logger.Error("Not authorized to read the volume")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the volume", nil)
		return
	}
	return
}

func (a *VolumeAdmin) GetVolumeByUUID(ctx context.Context, uuID string) (volume *model.Volume, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	volume = &model.Volume{}
	where := memberShip.GetWhere()
	err = db.Preload("Instance").Where(where).Where("uuid = ?", uuID).Take(volume).Error
	if err != nil {
		logger.Error("DB: query volume failed", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, volume.Owner)
	if !permit {
		logger.Error("Not authorized to read the volume")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the volume", nil)
		return
	}
	return
}

func (a *VolumeAdmin) CreateVolume(ctx context.Context, name string, size int32, instanceID int64, booting bool,
	iopsLimit int32, iopsBurst int32, bpsLimit int32, bpsBurst int32, poolID string) (volume *model.Volume, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if iopsLimit == 0 {
		iopsLimit = viper.GetInt32("volume.default_iops_limit")
	}
	if iopsBurst == 0 {
		iopsBurst = viper.GetInt32("volume.default_iops_burst")
	}
	if bpsLimit == 0 {
		bpsLimit = viper.GetInt32("volume.default_bps_limit")
	}
	if bpsBurst == 0 {
		bpsBurst = viper.GetInt32("volume.default_bps_burst")
	}
	if poolID == "" {
		poolID = viper.GetString("volume.default_wds_pool_id")
	}
	target := ""
	if booting {
		target = "vda"
	}
	memberShip := GetMemberShip(ctx)
	volume = &model.Volume{
		Model:      model.Model{Creater: memberShip.UserID},
		Owner:      memberShip.OrgID,
		Name:       name,
		InstanceID: instanceID,
		Booting:    booting,
		Format:     "raw",
		Target:     target,
		Size:       int32(size),
		IopsLimit:  iopsLimit,
		IopsBurst:  iopsBurst,
		BpsLimit:   bpsLimit,
		BpsBurst:   bpsBurst,
		Status:     "pending",
		PoolID:     poolID,
	}
	err = db.Create(volume).Error
	if err != nil {
		logger.Error("DB failed to create volume", err)
		err = NewCLError(ErrVolumeCreationFailed, "Failed to create volume", err)
		return
	}
	return
}

func (a *VolumeAdmin) Create(ctx context.Context, name string, size int32,
	iopsLimit int32, iopsBurst int32, bpsLimit int32, bpsBurst int32, poolID string) (volume *model.Volume, err error) {
	memberShip := GetMemberShip(ctx)
	// check the permission
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized to create volume")
		err = NewCLError(ErrPermissionDenied, "Not authorized to create volume", nil)
		return
	}

	volume, err = a.CreateVolume(ctx, name, size, 0, false, iopsLimit, iopsBurst, bpsLimit, bpsBurst, poolID)
	if err != nil {
		logger.Error("DB create volume failed", err)
		return
	}

	control := fmt.Sprintf("inter=")
	// RN-156: append the volume UUID to the command
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_volume_%s.sh '%d' '%d' '%s' '%d' '%d' '%d' '%d' '%s'",
		GetVolumeDriver(), volume.ID, volume.Size, volume.UUID, iopsLimit, iopsBurst, bpsLimit, bpsBurst, poolID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Create volume execution failed", err)
		return
	}
	return
}

func (a *VolumeAdmin) UpdateByUUID(ctx context.Context, uuid string, name string, instID int64) (volume *model.Volume, err error) {
	ctx, db := GetContextDB(ctx)
	volume = &model.Volume{}
	if err = db.Where("uuid = ?", uuid).Take(volume).Error; err != nil {
		logger.Error("DB: query volume failed", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", err)
		return
	}
	return a.Update(ctx, volume.ID, name, instID)
}

func (a *VolumeAdmin) Update(ctx context.Context, id int64, name string, instID int64) (volume *model.Volume, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	volume = &model.Volume{Model: model.Model{ID: id}}
	if err = db.Preload("Instance").Take(volume).Error; err != nil {
		logger.Error("DB: query volume failed", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", err)
		return
	}
	// check the permission
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, volume.Owner)
	if !permit {
		logger.Error("Not authorized to update the volume")
		err = NewCLError(ErrPermissionDenied, "Not authorized to update the volume", nil)
		return
	}

	if volume.IsError() {
		err = NewCLError(ErrVolumeInvalidState, fmt.Sprintf("Volume %s is in error state, cannot update now", volume.UUID), nil)
		return
	}

	if volume.InstanceID > 0 && instID > 0 && volume.InstanceID != instID {
		err = NewCLError(ErrVolumeIsInUse, "Please detach volume before attach it to new instance", nil)
		return
	}
	if name != "" {
		volume.Name = name
	}
	vol_driver := GetVolumeDriver()
	uuid := volume.UUID
	if vol_driver != "local" {
		uuid = volume.GetOriginVolumeID()
	}
	if volume.InstanceID != instID && volume.IsBusy() {
		// no change
		logger.Error("Volume is busy, cannot be updated", volume.Status)
		err = NewCLError(ErrVolumeIsBusy, fmt.Sprintf("Volume is busy, cannot be updated, status: %s", volume.Status), nil)
		return
	}
	// RN-156: append the volume UUID to the command
	if volume.InstanceID > 0 && instID == 0 && volume.IsAttached() {
		if volume.Booting {
			logger.Error("Boot volume can not be detached")
			err = NewCLError(ErrBootVolumeCannotDetach, "Boot volume can not be detached", nil)
			return
		}
		control := fmt.Sprintf("inter=%d", volume.Instance.Hyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/detach_volume_%s.sh '%d' '%d' '%s'", vol_driver, volume.Instance.ID, volume.ID, uuid)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Detach volume execution failed", err)
			return
		}
		volume.Status = model.VolumeStatusDetaching
		// PET-224: we should not set the instance ID to 0 here
		// the instance ID should be set to 0 after the volume is detached successfully (after script executed successfully)
		//volume.Instance = nil
		//volume.InstanceID = 0
	} else if instID > 0 && volume.InstanceID == 0 && volume.Status == model.VolumeStatusAvailable {
		instance := &model.Instance{Model: model.Model{ID: instID}}
		if err = db.Model(instance).Take(instance).Error; err != nil {
			logger.Error("DB: query instance failed", err)
			err = NewCLError(ErrInstanceNotFound, "Instance not found", err)
			return
		}
		control := fmt.Sprintf("inter=%d", instance.Hyper)
		// RN-156: append the volume UUID to the command
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/attach_volume_%s.sh '%d' '%d' '%s' '%s'", vol_driver, instance.ID, volume.ID, volume.GetVolumePath(), uuid)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Create volume execution failed", err)
			return
		}
		volume.Status = model.VolumeStatusAttaching
		// PET-224: we should not set the instance ID to instID here
		// the instance ID should be set to instID after the volume is attached successfully (after script executed successfully)
		//volume.InstanceID = instID
		//volume.Instance = nil
	}
	if err = db.Model(volume).Updates(volume).Error; err != nil {
		logger.Error("DB: update volume failed", err)
		err = NewCLError(ErrVolumeUpdateFailed, "Failed to update volume", err)
		return
	}
	return
}

func (a *VolumeAdmin) Delete(ctx context.Context, volume *model.Volume) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	// check the permission
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, volume.Owner)
	if !permit {
		logger.Error("Not authorized to delete the volume")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the volume", nil)
		return
	}

	if volume.IsBusy() || volume.IsAttached() {
		logger.Errorf("Volume is busy, cannot be deleted %+v", volume)
		err = NewCLError(ErrVolumeIsBusy, fmt.Sprintf("Volume[%s](%s) is busy, cannot be deleted", volume.Name, volume.UUID), nil)
		return
	}
	if err = db.Model(volume).Delete(volume).Error; err != nil {
		logger.Error("DB: delete volume failed", err)
		err = NewCLError(ErrVolumeDeleteFailed, "Failed to delete volume", err)
		return
	}
	control := fmt.Sprintf("inter=")
	vol_driver := GetVolumeDriver()
	uuid := volume.UUID
	if vol_driver != "local" {
		uuid = volume.GetOriginVolumeID()
	}
	logger.Debug("Delete volume", vol_driver, volume.ID, uuid, volume.GetVolumePath())
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_volume_%s.sh '%d' '%s' '%s'", vol_driver, volume.ID, uuid, volume.GetVolumePath())
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete volume execution failed", err)
		return
	}
	// seems the volume is already deleted
	/*
		if err = db.Delete(volume).Error; err != nil {
			logger.Error("DB: delete volume failed", err)
			return
		}
	*/
	return
}

func (a *VolumeAdmin) CanDeleteVolume(volume *model.Volume) bool {
	switch volume.Status {
	case model.VolumeStatusAvailable,
		model.VolumeStatusPending,
		model.VolumeStatusError:
		return true
	default:
		return false
	}
}

func (a *VolumeAdmin) DeleteVolumeByUUID(ctx context.Context, uuID string) (err error) {
	ctx, db := GetContextDB(ctx)
	volume := &model.Volume{}
	if err = db.Where("uuid = ?", uuID).Take(volume).Error; err != nil {
		logger.Error("DB: query volume failed", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", err)
		return
	}
	return a.Delete(ctx, volume)
}

func (a *VolumeAdmin) Resize(ctx context.Context, volume *model.Volume, size int32) (err error) {
	logger.Debugf("Resize volume %d with size %d", volume.ID, size)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, nil)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit, err := memberShip.CheckOwner(model.Writer, "volumes", volume.ID)
	if !permit {
		logger.Error("Failed to check owner")
		return
	}
	if !permit {
		logger.Error("Not authorized to delete the instance")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the instance", nil)
		return
	}
	if volume.IsError() {
		logger.Error("Volume is in error status")
		err = NewCLError(ErrVolumeInvalidState, "Volume is in error status", nil)
		return
	}
	if volume.IsBusy() {
		logger.Error("Volume is busy")
		err = NewCLError(ErrVolumeIsBusy, "Volume is busy", nil)
		return
	}

	if size <= volume.Size {
		logger.Error("The size must be greater than the original size")
		err = NewCLError(ErrVolumeInvalidSize, "The size must be greater than the original size", nil)
		return
	}
	if err = db.Model(volume).Updates(map[string]interface{}{
		"size":   size,
		"status": model.VolumeStatusResizing,
	}).Error; err != nil {
		logger.Error("update volume failed", err)
		err = NewCLError(ErrVolumeUpdateFailed, "Failed to update volume", err)
		return
	}
	if volume.Booting {
		instance := &model.Instance{Model: model.Model{ID: volume.InstanceID}}
		if err = db.Model(instance).Take(instance).Error; err != nil {
			logger.Error("DB: query instance failed", err)
			err = NewCLError(ErrInstanceNotFound, "Instance not found", err)
			return
		}
		cpu, memory := instance.Cpu, instance.Memory
		if instance.Cpu == 0 {
			var flavor *model.Flavor
			flavor, err = flavorAdmin.Get(ctx, instance.FlavorID)
			if err != nil {
				logger.Errorf("Failed to get flavor %+v, %+v", instance.FlavorID, err)
				err = NewCLError(ErrFlavorNotFound, "Flavor not found", err)
				return
			}
			cpu, memory = flavor.Cpu, flavor.Memory
		}
		if err = db.Model(instance).Updates(map[string]interface{}{
			"cpu":    cpu,
			"memory": memory,
			"disk":   size,
		}).Error; err != nil {
			logger.Error("DB: update instance failed", err)
			err = NewCLError(ErrInstanceUpdateFailed, "Failed to update instance", err)
			return
		}
	}
	control := fmt.Sprintf("inter=")
	volDriver := GetVolumeDriver()
	uuid := volume.UUID
	if volDriver != "local" {
		uuid = volume.GetOriginVolumeID()
	}
	if volume.InstanceID != 0 {
		control = fmt.Sprintf("inter=%d", volume.Instance.Hyper)
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/resize_volume_%s.sh '%d' '%s' '%d' '%t' '%d'", volDriver, volume.ID, uuid, size, volume.Booting, volume.InstanceID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Resize remote exec failed", err)
		return
	}
	return
}

func (a *VolumeAdmin) GetVolumesByInstanceID(ctx context.Context, instanceID int64) (volumes []*model.Volume, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	volumes = []*model.Volume{}
	if err = db.Preload("Instance").Where(where).Where("instance_id = ?", instanceID).Find(&volumes).Error; err != nil {
		logger.Error("Failed to query volumes, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query volumes", err)
		return
	}
	return
}

// list data volumes
func (a *VolumeAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, volumes []*model.Volume, err error) {
	return a.ListVolume(ctx, offset, limit, order, query, "all")
}

func (a *VolumeAdmin) ListVolume(ctx context.Context, offset, limit int64, order, query string, volume_type string) (total int64, volumes []*model.Volume, err error) {
	memberShip := GetMemberShip(ctx)
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
	where := memberShip.GetWhere()
	booting_where := ""
	if volume_type == "data" {
		booting_where = fmt.Sprintf("booting=%t", false)
	} else if volume_type == "boot" {
		booting_where = fmt.Sprintf("booting=%t", true)
	} else if volume_type == "all" {
		booting_where = ""
	} else {
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid volume type %s", volume_type), nil)
		return
	}

	volumes = []*model.Volume{}
	if booting_where != "" {
		if err = db.Model(&model.Volume{}).Where(where).Where(query).Where(booting_where).Count(&total).Error; err != nil {
			err = NewCLError(ErrSQLSyntaxError, "Failed to count volumes", err)
			return
		}
	} else {
		if err = db.Model(&model.Volume{}).Where(where).Where(query).Count(&total).Error; err != nil {
			err = NewCLError(ErrSQLSyntaxError, "Failed to count volumes", err)
			return
		}
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Instance").Where(where).Where(query).Find(&volumes).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to query volumes", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, vol := range volumes {
			vol.OwnerInfo = &model.Organization{Model: model.Model{ID: vol.Owner}}
			if err = db.Take(vol.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				err = NewCLError(ErrOwnerNotFound, "Owner organization not found", err)
				return
			}
		}
	}

	return
}

func (v *VolumeView) List(c *macaron.Context, store session.Store) {
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
	total, volumes, err := volumeAdmin.ListVolume(c.Req.Context(), offset, limit, order, query, "all")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Volumes"] = volumes
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "volumes")
}

func (v *VolumeView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.Error(http.StatusBadRequest)
		return
	}
	volumeID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	volume, err := volumeAdmin.Get(ctx, int64(volumeID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = volumeAdmin.Delete(c.Req.Context(), volume)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "volumes",
	})
	return
}

func (v *VolumeView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	pools := []*model.Dictionary{}
	err := db.Where("category = ?", "storage_pool").Find(&pools).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	c.Data["Pools"] = pools
	c.HTML(200, "volumes_new")
}

func (v *VolumeView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
	id := c.Params(":id")
	volID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "volumes", int64(volID))
	if err != nil {
		logger.Error("Failed to check permission", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	volume := &model.Volume{Model: model.Model{ID: int64(volID)}}
	if err := db.Preload("Instance").Take(volume).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	_, instances, err := instanceAdmin.List(c.Req.Context(), 0, -1, "", "")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	c.Data["Volume"] = volume
	c.Data["Instances"] = instances
	c.HTML(200, "volumes_patch")
}

func (v *VolumeView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	redirectTo := "../volumes"
	id := c.Params(":id")
	name := c.QueryTrim("name")
	instance := c.QueryTrim("instance")
	volID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "volumes", int64(volID))
	if err != nil {
		logger.Error("Failed to check permission", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	logger.Debugf("Patch volume(%s) to instance(%s)", id, instance)
	instID, err := strconv.Atoi(instance)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if instID > 0 {
		// have to check the instance permission
		permit, err = memberShip.CheckOwner(model.Writer, "instances", int64(instID))
		if err != nil {
			logger.Error("Failed to check permission", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}

		if !permit {
			logger.Error("Not authorized for this operation")
			c.Data["ErrorMsg"] = "Not authorized for this operation"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	_, err = volumeAdmin.Update(c.Req.Context(), int64(volID), name, int64(instID))
	if err != nil {
		logger.Error("Failed to update volume", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
	return
}

func (v *VolumeView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../volumes"
	name := c.QueryTrim("name")
	size := c.QueryTrim("size")
	vsize, err := strconv.Atoi(size)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	poolID := c.QueryTrim("pool")
	_, err = volumeAdmin.Create(c.Req.Context(), name, int32(vsize), 0, 0, 0, 0, poolID)
	if err != nil {
		logger.Error("Create volume failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *VolumeView) Resize(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/volumes"
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	volumeID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Volume ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	volume, err := volumeAdmin.Get(ctx, int64(volumeID))
	if err != nil {
		logger.Error("Volume query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Volume query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if c.Req.Method == "GET" {
		c.Data["Link"] = fmt.Sprintf("/volumes/%d/resize", volumeID)
		c.HTML(200, "volumes_resize")
		return
	} else if c.Req.Method == "POST" {
		size := c.QueryInt64("size")
		if size <= int64(volume.Size) {
			logger.Error("The size must be greater than the original size")
			c.Data["ErrorMsg"] = "The size must be greater than the original size"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		err = volumeAdmin.Resize(ctx, volume, int32(size))
		if err != nil {
			logger.Error("Resize failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		c.Redirect(redirectTo)
	}
}
