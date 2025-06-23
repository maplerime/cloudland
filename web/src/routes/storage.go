/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"fmt"
	"github.com/go-macaron/session"
	"gopkg.in/macaron.v1"
	"net/http"
	"strconv"
	"strings"
	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
)

var (
	imageStorageAdmin = &ImageStorageAdmin{}
	imageStorageView  = &ImageStorageView{}
)

type ImageStorageAdmin struct{}
type ImageStorageView struct{}

func (a *ImageStorageAdmin) List(offset, limit int64, order string, image *model.Image) (total int64, storages []*model.ImageStorage, err error) {
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	storages = []*model.ImageStorage{}
	if err = db.Model(&model.ImageStorage{}).Where("image_id = ?", image.ID).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where("image_id = ?", image.ID).Find(&storages).Error; err != nil {
		return
	}

	return
}

func (a *ImageStorageAdmin) InitStorages(image *model.Image, configs []*model.Dictionary) (storagesResp []*model.ImageStorage, err error) {

	db := DB()
	// load all config if not provided
	if configs == nil {
		if err = db.Where("category= ?", "storage_pool").Find(&configs).Error; err != nil {
			logger.Errorf("Failed to list pools, %v", err)
			return
		}
	}

	// load exists image storage records
	var storages []*model.ImageStorage
	if err = db.Where("image_id = ?", image.ID).Find(&storages).Error; err != nil {
		logger.Errorf("Failed to list image storage data, %v", err)
		return
	}

	// build a map from exists storage records
	storageMap := make(map[string]*model.ImageStorage)
	for _, storage := range storages {
		storageMap[storage.PoolID] = storage
	}

	for _, config := range configs {
		poolID := config.Value
		if storage, exists := storageMap[poolID]; exists {
			if storage.Status != model.StorageStatusSynced {
				storage.Status = model.StorageStatusSyncing
				if err = db.Save(&storage).Error; err != nil {
					logger.Error("Update image storage failed", err)
					return
				}
				storagesResp = append(storagesResp, storage)
			}
			delete(storageMap, storage.PoolID)
		} else {
			newStorage := &model.ImageStorage{
				ImageID: image.ID,
				PoolID:  poolID,
				Status:  model.StorageStatusSyncing,
			}
			if err = db.Create(newStorage).Error; err != nil {
				logger.Error("Create new image storage failed", err)
				return
			}
			storagesResp = append(storagesResp, newStorage)
		}
	}

	if len(storageMap) != 0 {
		logger.Infof("Found %d storages not in pool, will delete them", len(storageMap))
		for _, storage := range storageMap {
			if err = db.Delete(storage).Error; err != nil {
				logger.Error("Delete image storage failed", err)
				return
			}
		}
	}

	return

}

func (a *ImageStorageAdmin) SyncRemoteInfo(ctx context.Context, image *model.Image) (err error) {
	db := DB()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized to sync remote info for image")
		err = fmt.Errorf("Not authorized")
		return
	}
	driver := GetVolumeDriver()
	if driver == "local" {
		err = fmt.Errorf("Local driver do not need to be synchronized")
		logger.Error(err)
		return
	}

	// update storage type if not set
	if image.StorageType == "" {
		image.StorageType = driver
		db.Save(image)
	}

	// reset storages
	storages, err := imageStorageAdmin.InitStorages(image, nil)
	if err != nil {
		logger.Error("Failed to initialize image storages", err)
		return
	}

	for _, storage := range storages {
		prefix := strings.Split(image.UUID, "-")[0]
		control := "inter=0"
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/sync_image_info.sh '%d' '%s' '%s' '%d'", image.ID, prefix, storage.PoolID, storage.ID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Sync remote info command execution failed", err)
			return
		}
	}
	return
}

func (v *ImageStorageView) List(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
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
	id := c.Params("id")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
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
	total, images, err := imageStorageAdmin.List(offset, limit, order, image)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusInternalServerError)
		return
	}

	pages := GetPages(total, limit)
	c.Data["Storages"] = images
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["ImageID"] = imageID
	c.HTML(200, "storages")
}

func (v *ImageStorageView) SyncRemoteInfo(c *macaron.Context, store session.Store) {
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
	err = imageStorageAdmin.SyncRemoteInfo(ctx, image)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "storages",
	})
	return
}
