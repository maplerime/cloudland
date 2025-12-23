/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

import (
	"context"
	"fmt"

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

func GetHyperGroup(ctx context.Context, zoneID int64, skipHyper int32) (hyperGroup string, err error) {
	ctx, db := GetContextDB(ctx)
	hypers := []*model.Hyper{}
	where := fmt.Sprintf("status = 1 and hostid <> %d", skipHyper)
	if zoneID > 0 {
		where = fmt.Sprintf("zone_id = %d and status = 1 and hostid <> %d", zoneID, skipHyper)
	}
	if err = db.Where(where).Find(&hypers).Error; err != nil {
		logger.Error("Hypers query failed", err)
		return "", NewCLError(ErrSQLSyntaxError, "Failed to query hypervisors", err)
	}
	if len(hypers) == 0 {
		logger.Error("No qualified hypervisor")
		return "", NewCLError(ErrNoQualifiedHypervisor, "No qualified hypervisor found", nil)
	}
	hyperGroup = fmt.Sprintf("group-zone-%d", zoneID)
	for i, h := range hypers {
		if i == 0 {
			hyperGroup = fmt.Sprintf("%s:%d", hyperGroup, h.Hostid)
		} else {
			hyperGroup = fmt.Sprintf("%s,%d", hyperGroup, h.Hostid)
		}
	}
	return
}
