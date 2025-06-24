/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"fmt"
	"github.com/spf13/viper"
	"net/http"
	"os"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
	"web/src/utils"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	imageAdmin = &ImageAdmin{}
	imageView  = &ImageView{}
)

type ImageAdmin struct{}
type ImageView struct{}

func FileExist(filename string) bool {
	_, err := os.Lstat(filename)
	return !os.IsNotExist(err)
}

func (a *ImageAdmin) Create(ctx context.Context, osCode, name, osVersion, virtType, userName, url, architecture, bootLoader string, qaEnabled bool, instID int64, uuid string) (image *model.Image, err error) {
	logger.Debugf("Creating image %s %s %s %s %s %s %s %s %t %d %s", osCode, name, osVersion, virtType, userName, url, architecture, bootLoader, qaEnabled, instID, uuid)
	memberShip := GetMemberShip(ctx)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	var instance *model.Instance
	if instID > 0 {
		instance = &model.Instance{Model: model.Model{ID: instID}}
		err = db.Preload("Image").Preload("Volumes").Take(instance).Error
		if err != nil {
			logger.Error("DB failed to query instance", err)
			return
		}
		if instance.Status != "shut_off" {
			err = fmt.Errorf("instance [%s] is running, shut it down first before capturing", instance.Hostname)
			logger.Error(err)
			return
		}
		image = instance.Image.Clone()
		image.Model = model.Model{Creater: memberShip.UserID}
		image.Owner = memberShip.OrgID
		image.Name = name
		image.Status = "creating"
		image.CaptureFromInstanceID = instance.ID
		image.CaptureFromInstance = instance
	} else {
		image = &model.Image{
			Model:        model.Model{Creater: memberShip.UserID},
			Owner:        memberShip.OrgID,
			OsVersion:    osVersion,
			VirtType:     virtType,
			UserName:     userName,
			Name:         name,
			OSCode:       osCode,
			Status:       "creating",
			Architecture: architecture,
			BootLoader:   bootLoader,
			QAEnabled:    qaEnabled,
		}
		if uuid != "" {
			logger.Debugf("Creating image with UUID %s", uuid)
			image.UUID = uuid
		}
	}
	logger.Debugf("Creating image %+v", image)
	err = db.Create(image).Error
	if err != nil {
		logger.Error("DB create image failed, %v", err)
	}

	// create default storage
	defaultPool := viper.GetString("volume.default_wds_pool_id")
	storageID := int64(0)
	if defaultPool != "" {
		storage := &model.ImageStorage{
			ImageID: image.ID,
			Image:   image,
			PoolID:  defaultPool,
			Status:  model.StorageStatusUnknown,
		}
		if err = db.Create(storage).Error; err != nil {
			logger.Error("Failed to create default image storage", err)
			return
		}
		storageID = storage.ID
	}

	// create with default pool id
	prefix := strings.Split(image.UUID, "-")[0]
	control := "inter=0"
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_image.sh '%d' '%s' '%s' '%d'", image.ID, prefix, url, storageID)
	if instID > 0 {
		bootVolumeUUID := ""
		if instance.Volumes != nil {
			for _, volume := range instance.Volumes {
				if volume.Booting {
					bootVolumeUUID = volume.GetOriginVolumeID()
					break
				}
			}
		}
		control = fmt.Sprintf("inter=%d", instance.Hyper)
		command = fmt.Sprintf("/opt/cloudland/scripts/backend/capture_image.sh '%d' '%s' '%d' '%s' '%d'", image.ID, prefix, instance.ID, bootVolumeUUID, storageID)
	}
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Create image command execution failed", err)
		return
	}
	return
}

func (a *ImageAdmin) GetImageByUUID(ctx context.Context, uuID string) (image *model.Image, err error) {
	db := DB()
	image = &model.Image{}
	err = db.Where("uuid = ?", uuID).Take(image).Error
	if err != nil {
		logger.Error("Failed to query image, %v", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized to get image")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *ImageAdmin) GetImageByName(ctx context.Context, name string) (image *model.Image, err error) {
	db := DB()
	image = &model.Image{}
	err = db.Where("name = ?", name).Take(image).Error
	if err != nil {
		logger.Error("Failed to query image, %v", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized to get image")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *ImageAdmin) Get(ctx context.Context, id int64) (image *model.Image, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid image ID: %d", id)
		logger.Error(err)
		return
	}
	db := DB()
	image = &model.Image{Model: model.Model{ID: id}}
	err = db.Take(image).Error
	if err != nil {
		logger.Error("DB failed to query image, %v", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized to get image")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *ImageAdmin) GetImage(ctx context.Context, reference *BaseReference) (image *model.Image, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = fmt.Errorf("Image base reference must be provided with either uuid or name")
		return
	}
	if reference.ID != "" {
		image, err = a.GetImageByUUID(ctx, reference.ID)
		return
	}
	if reference.Name != "" {
		image, err = a.GetImageByName(ctx, reference.Name)
		return
	}
	return
}

func (a *ImageAdmin) Delete(ctx context.Context, image *model.Image) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, image.Owner)
	if !permit {
		logger.Error("Not authorized to delete image")
		err = fmt.Errorf("Not authorized")
		return
	}
	refCount := 0
	err = db.Model(&model.Instance{}).Where("image_id = ?", image.ID).Count(&refCount).Error
	if err != nil {
		logger.Error("Failed to count the number of instances using the image", err)
		return
	}
	if refCount > 0 {
		logger.Error("Image can not be deleted if there are instances using it")
		err = fmt.Errorf("The image can not be deleted if there are instances using it")
		return
	}
	if image.Status == "available" {
		prefix := strings.Split(image.UUID, "-")[0]
		control := "inter="
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_image.sh '%d' '%s' '%s'", image.ID, prefix, image.Format)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Clear image command execution failed", err)
			return
		}
	}
	if err = db.Delete(image).Error; err != nil {
		return
	}
	return
}

func (a *ImageAdmin) List(offset, limit int64, order, query string) (total int64, images []*model.Image, err error) {
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	images = []*model.Image{}
	if err = db.Model(&model.Image{}).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(query).Find(&images).Error; err != nil {
		return
	}

	return
}

func (a *ImageAdmin) Update(ctx context.Context, image *model.Image, osCode, name, osVersion, userName string, pools []string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to update image")
		err = fmt.Errorf("Not Authorized")
		return
	}
	if osCode != "" {
		image.OSCode = osCode
	}
	if name != "" {
		image.Name = name
	}
	if osVersion != "" {
		image.OsVersion = osVersion
	}
	if userName != "" {
		image.UserName = userName
	}
	err = db.Model(image).Updates(image).Error
	if err != nil {
		logger.Error("Failed to save image", err)
		return
	}

	driver := GetVolumeDriver()
	if driver == "local" {
		return
	}

	// with default pool
	defaultPoolID := viper.GetString("volume.default_wds_pool_id")
	withDefault := false
	var configs []*model.Dictionary
	for _, poolID := range pools {
		config := &model.Dictionary{}
		if err = db.Where("value = ? AND category = 'storage_pool'", poolID).First(&config).Error; err != nil {
			logger.Errorf("Failed to get config")
			continue
		}
		configs = append(configs, config)
		if poolID == defaultPoolID {
			withDefault = true
		}
	}
	if !withDefault {
		configs = append(configs, &model.Dictionary{Value: defaultPoolID})
	}
	storages, err := imageStorageAdmin.InitStorages(image, configs)
	if err != nil {
		logger.Error("Failed to initialize image storages", err)
		return
	}

	for _, storage := range storages {
		if storage.Status == model.StorageStatusSynced {
			continue
		}
		prefix := strings.Split(image.UUID, "-")[0]
		control := "inter=0"
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clone_image.sh '%d' '%s' '%s' '%d'", image.ID, prefix, storage.PoolID, storage.ID)
		if storage.PoolID == defaultPoolID {
			command = fmt.Sprintf("/opt/cloudland/scripts/backend/sync_image_info.sh '%d' '%s' '%s' '%d'", image.ID, prefix, storage.PoolID, storage.ID)
		}
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Sync remote info command execution failed", err)
		}
	}
	return
}

func (v *ImageView) List(c *macaron.Context, store session.Store) {
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
	total, images, err := imageAdmin.List(offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusInternalServerError)
		return
	}
	pages := GetPages(total, limit)
	c.Data["Images"] = images
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "images")
}

func (v *ImageView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	imageID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	image, err := imageAdmin.Get(ctx, int64(imageID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = imageAdmin.Delete(ctx, image)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "images",
	})
	return
}

func (v *ImageView) New(c *macaron.Context, store session.Store) {
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
	c.Data["Instances"] = instances
	c.HTML(200, "images_new")
}

func (v *ImageView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../images"
	osCode := c.QueryTrim("osCode")
	uuid := c.QueryTrim("uuid")
	if uuid != "" && !utils.IsUUID(uuid) {
		c.Data["ErrorMsg"] = "Invalid UUID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	url := c.QueryTrim("url")
	instance := c.QueryInt64("instance")
	osVersion := c.QueryTrim("osVersion")
	virtType := "kvm-x86_64"
	userName := c.QueryTrim("userName")
	qaEnabled := true
	architecture := "x86_64"
	bootLoader := c.QueryTrim("bootLoader")
	_, err := imageAdmin.Create(c.Req.Context(), osCode, name, osVersion, virtType, userName, url, architecture, bootLoader, qaEnabled, instance, uuid)
	if err != nil {
		logger.Error("Create image failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *ImageView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
	id := c.Params(":id")
	imageID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "images", int64(imageID))
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
	image := &model.Image{Model: model.Model{ID: int64(imageID)}}
	if err = db.Take(image).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	query := fmt.Sprintf("category='%s'", "storage_pool")
	_, pools, err := dictionaryAdmin.List(c.Req.Context(), 0, -1, "", query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	_, storages, err := imageStorageAdmin.List(0, -1, "", image)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	selectedPools := make(map[string]bool)
	for _, s := range storages {
		if s.Status == model.StorageStatusSynced {
			selectedPools[s.PoolID] = true
		}
	}
	c.Data["Image"] = image
	c.Data["Pools"] = pools
	c.Data["Storages"] = selectedPools
	c.HTML(200, "images_patch")
}

func (v *ImageView) Patch(c *macaron.Context, store session.Store) {
	db := DB()
	memberShip := GetMemberShip(c.Req.Context())
	redirectTo := "../images"
	id := c.Params(":id")
	osCode := c.QueryTrim("osCode")
	name := c.QueryTrim("name")
	osVersion := c.QueryTrim("osVersion")
	userName := c.QueryTrim("userName")
	imageID, err := strconv.Atoi(id)
	pools := c.QueryStrings("pools")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "images", int64(imageID))
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

	image := &model.Image{Model: model.Model{ID: int64(imageID)}}
	if err = db.Take(image).Error; err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}

	err = imageAdmin.Update(c.Req.Context(), image, osCode, name, osVersion, userName, pools)
	if err != nil {
		logger.Error("Failed to update volume", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
	return
}
