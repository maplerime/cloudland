/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	. "web/src/common"
	"web/src/model"
)

var (
	imageStorageAdmin = &ImageStorageAdmin{}
)

type ImageStorageAdmin struct{}

func (a *ImageStorageAdmin) Create(ctx context.Context, imageID int64, volumeID, poolID string) (imageStorage *model.ImageStorage, err error) {
	logger.Debugf("Creating image storage %d %s %s", imageID, volumeID, poolID)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	imageStorage = &model.ImageStorage{
		ImageID:  imageID,
		VolumeID: volumeID,
		PoolID:   poolID,
	}
	err = db.Where(imageStorage).First(&imageStorage).Error
	if err == nil {
		logger.Debugf("Image storage already exists: %+v", imageStorage)
		return
	}
	err = db.Create(imageStorage).Error
	if err != nil {
		logger.Error("DB create image storage failed, %v", err)
		return
	}
	return
}

func (a *ImageStorageAdmin) GetImageStorageByUUID(ctx context.Context, uuID string) (imageStorage *model.ImageStorage, err error) {
	db := DB()
	imageStorage = &model.ImageStorage{}
	err = db.Where("uuid = ?", uuID).Take(imageStorage).Error
	if err != nil {
		logger.Error("Failed to query image storage, %v", err)
		return
	}
	return
}

func (a *ImageStorageAdmin) GetImageStorageByImageID(ctx context.Context, imageID string) (imageStorage *model.ImageStorage, err error) {
	db := DB()
	imageStorage = &model.ImageStorage{}
	err = db.Where("image_id = ?", imageID).Take(imageStorage).Error
	if err != nil {
		logger.Error("Failed to query image storage, %v", err)
		return
	}
	return
}
