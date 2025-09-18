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
	"gopkg.in/macaron.v1"
)

var (
	imageStorageAdmin = &ImageStorageAdmin{}
	imageStorageView  = &ImageStorageView{}
)

type ImageStorageAdmin struct{}
type ImageStorageView struct{}

func (a *ImageStorageAdmin) List(offset, limit int64, order string, image *model.Image, query string) (total int64, storages []*model.ImageStorage, err error) {
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("pool_id = '%s'", query)
	}

	storages = []*model.ImageStorage{}
	if err = db.Model(&model.ImageStorage{}).Where("image_id = ?", image.ID).Where(query).Count(&total).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to count image storage(s)", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where("image_id = ?", image.ID).Where(query).Find(&storages).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to query image storage(s)", err)
		return
	}

	return
}

// InitStorages initializes the image storage records for a given image and pool
func (a *ImageStorageAdmin) InitStorages(ctx context.Context, image *model.Image, pools []string) (storagesResp []*model.ImageStorage, err error) {
	ctx, db := GetContextDB(ctx)
	defaultPoolID := viper.GetString("volume.default_wds_pool_id")
	containsDefault := false
	// valid pools
	finalPools := make([]string, 0)
	for _, poolID := range pools {
		dictionary := &model.Dictionary{}
		if poolID == defaultPoolID {
			finalPools = append(finalPools, defaultPoolID)
			containsDefault = true
			continue
		}
		dictionary, err = dictionaryAdmin.Find(ctx, "storage_pool", poolID)
		if err != nil {
			logger.Errorf("Failed to find storage pool %s, %v", poolID, err)
			return
		}
		finalPools = append(finalPools, dictionary.Value)
	}

	// set default
	if !containsDefault {
		finalPools = append(finalPools, defaultPoolID)
	}

	// load exists image storage records
	var storages []*model.ImageStorage
	if err = db.Where("image_id = ?", image.ID).Find(&storages).Error; err != nil {
		logger.Errorf("Failed to list image storage data, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to list image storage data", err)
		return
	}

	storageMap := make(map[string]*model.ImageStorage)
	for _, storage := range storages {
		storageMap[storage.PoolID] = storage
	}

	for _, poolID := range finalPools {
		if storage, exists := storageMap[poolID]; exists {
			if storage.Status != model.StorageStatusSynced && storage.Status != model.StorageStatusSyncing {
				storage.Status = model.StorageStatusUnknown
				// 构建需要更新的字段映射
				updateFields := make(map[string]interface{})
				updateFields["status"] = storage.Status

				if err = db.Model(&storage).Updates(updateFields).Error; err != nil {
					logger.Error("Update image storage failed", err)
					err = NewCLError(ErrImageStorageCreateFailed, "Failed to Create image storage", err)
					return
				}
			}
			storagesResp = append(storagesResp, storage)
		} else {
			newStorage := &model.ImageStorage{
				Image:   image,
				ImageID: image.ID,
				PoolID:  poolID,
				Status:  model.StorageStatusUnknown,
			}
			if err = db.Create(newStorage).Error; err != nil {
				logger.Error("Create new image storage failed", err)
				err = NewCLError(ErrImageStorageCreateFailed, "Failed to Create image storage", err)
				return
			}
			storagesResp = append(storagesResp, newStorage)
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
	total, images, err := imageStorageAdmin.List(offset, limit, order, image, "")
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

func (a *ImageStorageAdmin) CheckDefaultPool() (err error) {
	driver := GetVolumeDriver()
	db := DB()
	if driver != "local" {
		defaultPool := viper.GetString("volume.default_wds_pool_id")
		err = db.Where("category='storage_pool' AND value=?", defaultPool).First(&model.Dictionary{}).Error
		if err != nil {
			err = fmt.Errorf("default storage pool %s is not in configurations", defaultPool)
		}
	}
	return
}
