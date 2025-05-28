/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

import (
	"context"

	"web/src/model"
)

func GetImageOSCode(ctx context.Context, instance *model.Instance) string {
	osCode := "linux"
	if instance.Image == nil {
		_, db := GetContextDB(ctx)
		instance.Image = &model.Image{Model: model.Model{ID: instance.ImageID}}
		err := db.Take(instance.Image).Error
		if err != nil {
			logger.Error("Invalid image ", instance.ImageID)
			return osCode
		}
	}
	osCode = instance.Image.OSCode
	return osCode
}
