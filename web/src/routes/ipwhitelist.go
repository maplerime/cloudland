/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	. "web/src/common"
	"web/src/model"
)

var ipWhitelistAdmin = &IPWhitelistAdmin{}

type IPWhitelistAdmin struct{}

func (a *IPWhitelistAdmin) Create(ctx context.Context, instanceUUID, ip, reason string) (entry *model.IPWhitelist, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	entry = &model.IPWhitelist{
		Model:        model.Model{Creater: memberShip.UserID},
		Owner:        memberShip.OrgID,
		InstanceUUID: instanceUUID,
		IP:           ip,
		Reason:       reason,
	}
	if err = db.Create(entry).Error; err != nil {
		logger.Error("DB failed to create ip whitelist entry", err)
		return
	}
	err = a.broadcastAll(ctx)
	if err != nil {
		logger.Error("Failed to broadcast ip whitelist to compute nodes", err)
		err = nil
	}
	return
}

func (a *IPWhitelistAdmin) List(ctx context.Context, offset, limit int64, query string) (total int64, entries []*model.IPWhitelist, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	entries = []*model.IPWhitelist{}
	if query != "" {
		where = fmt.Sprintf("%s and (%s)", where, query)
	}
	if err = db.Model(&model.IPWhitelist{}).Where(where).Count(&total).Error; err != nil {
		logger.Error("DB failed to count ip whitelist entries", err)
		return
	}
	if err = db.Where(where).Offset(offset).Limit(limit).Find(&entries).Error; err != nil {
		logger.Error("DB failed to list ip whitelist entries", err)
		return
	}
	return
}

func (a *IPWhitelistAdmin) GetByUUID(ctx context.Context, uuid string) (entry *model.IPWhitelist, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	entry = &model.IPWhitelist{}
	if err = db.Where(where).Where("uuid = ?", uuid).Take(entry).Error; err != nil {
		logger.Errorf("DB failed to get ip whitelist entry %s, %v", uuid, err)
		return
	}
	return
}

func (a *IPWhitelistAdmin) DeleteByUUID(ctx context.Context, uuid string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	entry, err := a.GetByUUID(ctx, uuid)
	if err != nil {
		return
	}
	if err = db.Delete(entry).Error; err != nil {
		logger.Error("DB failed to delete ip whitelist entry", err)
		return
	}
	err = a.broadcastAll(ctx)
	if err != nil {
		logger.Error("Failed to broadcast ip whitelist to compute nodes", err)
		err = nil
	}
	return
}

func (a *IPWhitelistAdmin) DeleteByInstanceUUID(ctx context.Context, instanceUUID string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if err = db.Where("instance_uuid = ?", instanceUUID).Delete(&model.IPWhitelist{}).Error; err != nil {
		logger.Error("DB failed to delete ip whitelist entries by instance_uuid", err)
		return
	}
	err = a.broadcastAll(ctx)
	if err != nil {
		logger.Error("Failed to broadcast ip whitelist to compute nodes", err)
		err = nil
	}
	return
}

type whitelistPayload struct {
	Whitelist []whitelistEntry `json:"whitelist"`
}

type whitelistEntry struct {
	InstanceUUID string `json:"instance_uuid"`
	IP           string `json:"ip"`
	Reason       string `json:"reason"`
}

// BroadcastAll sends the full whitelist JSON to all compute nodes via HyperExecute.
// It is exported so that the apis package can trigger a manual refresh.
func (a *IPWhitelistAdmin) BroadcastAll(ctx context.Context) (err error) {
	return a.broadcastAll(ctx)
}

func (a *IPWhitelistAdmin) broadcastAll(ctx context.Context) (err error) {
	_, db := GetContextDB(ctx)
	var entries []*model.IPWhitelist
	if err = db.Find(&entries).Error; err != nil {
		logger.Error("DB failed to query ip whitelist for broadcast", err)
		return
	}
	payload := whitelistPayload{
		Whitelist: make([]whitelistEntry, 0, len(entries)),
	}
	for _, e := range entries {
		payload.Whitelist = append(payload.Whitelist, whitelistEntry{
			InstanceUUID: e.InstanceUUID,
			IP:           e.IP,
			Reason:       e.Reason,
		})
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal ip whitelist payload", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(jsonData)
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/operation/manage_ip_whitelist.sh 'refresh' '%s'", encoded)
	err = HyperExecute(ctx, "toall=", command)
	if err != nil {
		logger.Error("HyperExecute broadcast of ip whitelist failed", err)
		return
	}
	return
}

// IsIPWhitelisted returns true if the given IP is present in the whitelist DB.
// This function uses a fresh DB connection and is safe to call from any package.
func IsIPWhitelisted(ip string) bool {
	db := DB()
	var count int
	if err := db.Model(&model.IPWhitelist{}).Where("ip = ?", ip).Count(&count).Error; err != nil {
		logger.Errorf("Failed to check ip whitelist for %s: %v", ip, err)
		return false
	}
	return count > 0
}
