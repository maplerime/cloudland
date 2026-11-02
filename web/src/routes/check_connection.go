package routes

import (
	"context"
	"fmt"
	"net"
	"strings"

	. "web/src/common"
	"web/src/model"
)

var checkConnectionAdmin = &CheckConnectionAdmin{}

type CheckConnectionAdmin struct {
	execute func(context.Context, string, string) error
}

const MaxCheckConnectionIPs = 256

// NormalizeCheckConnectionIPs validates the entire batch before any DB writes.
func NormalizeCheckConnectionIPs(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > MaxCheckConnectionIPs {
		return nil, fmt.Errorf("provide between 1 and %d IP addresses", MaxCheckConnectionIPs)
	}
	ips := []string{}
	seen := map[string]bool{}
	for _, raw := range input {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %q", raw)
		}
		address := ip.String()
		if !seen[address] {
			seen[address] = true
			ips = append(ips, address)
		}
	}
	return ips, nil
}

func (a *CheckConnectionAdmin) Create(ctx context.Context, input []string) (entries []*model.CheckConnection, err error) {
	m := GetMemberShip(ctx)
	if !m.CheckPermission(model.Writer) {
		return nil, fmt.Errorf("not authorized for this operation")
	}
	ips, err := NormalizeCheckConnectionIPs(input)
	if err != nil {
		return nil, err
	}
	_, db := GetContextDB(ctx)
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer tx.Rollback()
	entries = make([]*model.CheckConnection, 0, len(ips))
	for _, ip := range ips {
		entry := &model.CheckConnection{Model: model.Model{Creater: m.UserID}, Owner: m.OrgID, IP: ip, Status: "pending"}
		if err = tx.Create(entry).Error; err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err = tx.Commit().Error; err != nil {
		return nil, err
	}
	// Commit first so failed submissions remain visible.
	for _, entry := range entries {
		if dispatchErr := a.dispatch(ctx, entry); dispatchErr != nil && err == nil {
			err = dispatchErr
		}
	}
	return entries, err
}

func (a *CheckConnectionAdmin) List(ctx context.Context, offset, limit int64) (total int64, entries []*model.CheckConnection, err error) {
	_, db := GetContextDB(ctx)
	where := GetMemberShip(ctx).GetWhere()
	entries = []*model.CheckConnection{}
	if err = db.Model(&model.CheckConnection{}).Where(where).Count(&total).Error; err != nil {
		return
	}
	err = db.Where(where).Order("created_at desc, id desc").Offset(offset).Limit(limit).Find(&entries).Error
	return
}

func (a *CheckConnectionAdmin) dispatch(ctx context.Context, entry *model.CheckConnection) error {
	// The address is validated before it is stored.
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/check_connection.sh '%s'", entry.IP)
	execute := a.execute
	if execute == nil {
		execute = HyperExecute
	}
	dispatchErr := execute(ctx, "toall=", command)
	entry.Status, entry.DispatchError = "submitted", ""
	if dispatchErr != nil {
		entry.Status, entry.DispatchError = "failed", dispatchErr.Error()
	}
	_, db := GetContextDB(ctx)
	if err := db.Model(entry).Updates(map[string]interface{}{"status": entry.Status, "dispatch_error": entry.DispatchError}).Error; err != nil {
		return err
	}
	// A dispatch error is part of the persisted request; callers receive the row
	// and its status rather than an error suggesting that no request was created.
	return nil
}
