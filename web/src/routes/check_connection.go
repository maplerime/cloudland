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
	if !m.CheckPermission(model.Admin) {
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

func (a *CheckConnectionAdmin) Delete(ctx context.Context, id string) error {
	m := GetMemberShip(ctx)
	if !m.CheckPermission(model.Admin) {
		return fmt.Errorf("not authorized for this operation")
	}
	_, db := GetContextDB(ctx)
	entry := &model.CheckConnection{}
	if err := db.Where(m.GetWhere()).Where("uuid = ?", id).Take(entry).Error; err != nil {
		return err
	}
	return a.deleteEntries(ctx, []*model.CheckConnection{entry})
}

// Capture addresses before detaching floating IPs and releasing interfaces.
func (a *CheckConnectionAdmin) instanceIPs(ctx context.Context, instanceID int64) ([]string, error) {
	_, db := GetContextDB(ctx)
	var ids []int64
	if err := db.Model(&model.Interface{}).Where("instance = ?", instanceID).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	ips := []string{}
	if len(ids) > 0 {
		var addresses []*model.Address
		if err := db.Where("interface IN (?) OR second_interface IN (?)", ids, ids).Find(&addresses).Error; err != nil {
			return nil, err
		}
		for _, address := range addresses {
			ips = append(ips, address.Address)
		}
	}
	var fips []*model.FloatingIp
	if err := db.Where("instance_id = ?", instanceID).Find(&fips).Error; err != nil {
		return nil, err
	}
	for _, fip := range fips {
		ips = append(ips, fip.FipAddress, fip.IPAddress, fip.IntAddress)
	}
	return ips, nil
}

// DeleteByInstance is a trusted lifecycle operation used by the VM deletion RPC.
func (a *CheckConnectionAdmin) DeleteByInstance(ctx context.Context, instance *model.Instance) error {
	ips, err := a.instanceIPs(ctx, instance.ID)
	if err != nil {
		return err
	}
	return a.DeleteByIPs(ctx, ips)
}

// DeleteByIPs also removes watches created by administrators for another tenant.
func (a *CheckConnectionAdmin) DeleteByIPs(ctx context.Context, input []string) error {
	ips := []string{}
	seen := map[string]bool{}
	for _, value := range input {
		value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
		if value == "" {
			continue
		}
		ip := net.ParseIP(value)
		if ip == nil {
			return fmt.Errorf("invalid instance IP address: %q", value)
		}
		if !seen[ip.String()] {
			ips = append(ips, ip.String())
			seen[ip.String()] = true
		}
	}
	if len(ips) == 0 {
		return nil
	}
	_, db := GetContextDB(ctx)
	var entries []*model.CheckConnection
	if err := db.Where("ip IN (?)", ips).Find(&entries).Error; err != nil {
		return err
	}
	return a.deleteEntries(ctx, entries)
}

func (a *CheckConnectionAdmin) deleteEntries(ctx context.Context, entries []*model.CheckConnection) (err error) {
	if len(entries) == 0 {
		return nil
	}
	_, db := GetContextDB(ctx)
	ids := []int64{}
	ips := map[string]bool{}
	for _, entry := range entries {
		ip := net.ParseIP(entry.IP)
		if ip == nil {
			return fmt.Errorf("invalid connection check IP: %q", entry.IP)
		}
		ids = append(ids, entry.ID)
		ips[ip.String()] = true
	}
	execute := a.execute
	if execute == nil {
		execute = HyperExecute
	}
	for ip := range ips {
		var remaining int
		if err = db.Model(&model.CheckConnection{}).Where("ip = ? AND id NOT IN (?)", ip, ids).Count(&remaining).Error; err != nil {
			return err
		}
		if remaining > 0 {
			continue
		}
		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/check_connection.sh '%s' 'delete'", ip)
		if err = execute(ctx, "toall=", command); err != nil {
			return err
		}
	}
	return db.Where("id IN (?)", ids).Delete(&model.CheckConnection{}).Error
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
