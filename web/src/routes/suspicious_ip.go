package routes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	. "web/src/common"
	"web/src/model"
)

var suspiciousIPAdmin = &SuspiciousIPAdmin{}
var ErrSuspiciousIPPermission = errors.New("not authorized for this operation")

type SuspiciousIPAdmin struct {
	execute func(context.Context, string, string) error
}

func normalizeSuspiciousIP(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return "", fmt.Errorf("invalid IP address: %q", value)
	}
	return ip.String(), nil
}

func suspiciousIPPermitted(ctx context.Context) error {
	if !GetMemberShip(ctx).CheckPermission(model.Admin) {
		return ErrSuspiciousIPPermission
	}
	return nil
}

// RecordObservedSuspiciousIPs is called only by node RPCs. Repeated observations
// update discovery metadata without overwriting operator notes or block status.
func RecordObservedSuspiciousIPs(ctx context.Context, node int32, input []string) (err error) {
	ips := []string{}
	seen := map[string]bool{}
	for _, value := range input {
		ip, e := normalizeSuspiciousIP(value)
		if e != nil {
			return e
		}
		if !seen[ip] {
			ips = append(ips, ip)
			seen[ip] = true
		}
	}
	ctx, db, own := StartTransaction(ctx)
	defer func() {
		if own {
			EndTransaction(ctx, err)
		}
	}()
	now := time.Now().UTC()
	for _, ip := range ips {
		err = db.Exec(`INSERT INTO suspicious_ips (uuid, ip, status, created_at, updated_at, last_seen_at, last_seen_node)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (ip) DO UPDATE SET
			last_seen_at = excluded.last_seen_at, last_seen_node = excluded.last_seen_node, updated_at = excluded.updated_at`,
			uuid.New().String(), ip, model.SuspiciousIPPending, now, now, now, node).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *SuspiciousIPAdmin) List(ctx context.Context, offset, limit int64, ip string) (total int64, entries []*model.SuspiciousIP, err error) {
	if err = suspiciousIPPermitted(ctx); err != nil {
		return
	}
	_, db := GetContextDB(ctx)
	query := db.Model(&model.SuspiciousIP{})
	if ip != "" {
		query = query.Where("ip LIKE ?", "%"+ip+"%")
	}
	entries = []*model.SuspiciousIP{}
	if err = query.Count(&total).Error; err != nil {
		return
	}
	err = query.Order("updated_at desc, id desc").Offset(offset).Limit(limit).Find(&entries).Error
	return
}

func (a *SuspiciousIPAdmin) Get(ctx context.Context, id string) (*model.SuspiciousIP, error) {
	if err := suspiciousIPPermitted(ctx); err != nil {
		return nil, err
	}
	_, db := GetContextDB(ctx)
	entry := &model.SuspiciousIP{}
	err := db.Where("uuid = ?", id).Take(entry).Error
	return entry, err
}

func (a *SuspiciousIPAdmin) Create(ctx context.Context, value, note string) (*model.SuspiciousIP, error) {
	if err := suspiciousIPPermitted(ctx); err != nil {
		return nil, err
	}
	ip, err := normalizeSuspiciousIP(value)
	if err != nil {
		return nil, err
	}
	_, db := GetContextDB(ctx)
	entry := &model.SuspiciousIP{Model: model.Model{Creater: GetMemberShip(ctx).UserID}, IP: ip, Status: model.SuspiciousIPPending, Note: note}
	return entry, db.Create(entry).Error
}

func (a *SuspiciousIPAdmin) Update(ctx context.Context, id, value, note string) (*model.SuspiciousIP, error) {
	entry, err := a.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	ip, err := normalizeSuspiciousIP(value)
	if err != nil {
		return nil, err
	}
	if entry.Status == model.SuspiciousIPBlocked && ip != entry.IP {
		return nil, fmt.Errorf("cannot change an IP with a submitted block")
	}
	_, db := GetContextDB(ctx)
	updates := map[string]interface{}{"ip": ip, "note": note}
	if ip != entry.IP {
		updates["status"], updates["block_error"] = model.SuspiciousIPPending, ""
		updates["last_seen_at"], updates["last_seen_node"] = nil, nil
		updates["blocked_at"] = nil
	}
	if err = db.Model(entry).Updates(updates).Error; err != nil {
		return nil, err
	}
	return a.Get(ctx, id)
}

func (a *SuspiciousIPAdmin) Delete(ctx context.Context, id string) error {
	entry, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	ip, err := normalizeSuspiciousIP(entry.IP)
	if err != nil {
		return err
	}
	execute := a.execute
	if execute == nil {
		execute = HyperExecute
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '%s'", ip)
	if err := execute(ctx, "toall=", command); err != nil {
		return fmt.Errorf("failed to submit IP unblock: %w", err)
	}
	_, db := GetContextDB(ctx)
	// The unique IP may be discovered again after an operator deletes the record.
	return db.Unscoped().Delete(entry).Error
}

// Block also accepts addresses never observed before, so API initiated blocks
// are always represented in the same list as node observations.
func (a *SuspiciousIPAdmin) Block(ctx context.Context, value string) (*model.SuspiciousIP, error) {
	if err := suspiciousIPPermitted(ctx); err != nil {
		return nil, err
	}
	ip, err := normalizeSuspiciousIP(value)
	if err != nil {
		return nil, err
	}
	// block_ip.sh currently manages an IPv4 ipset and iptables rules.
	if net.ParseIP(ip).To4() == nil {
		return nil, fmt.Errorf("block_ip.sh supports IPv4 addresses only")
	}
	_, db := GetContextDB(ctx)
	now := time.Now().UTC()
	if err = db.Exec(`INSERT INTO suspicious_ips (uuid, ip, status, creater, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (ip) DO NOTHING`, uuid.New().String(), ip,
		model.SuspiciousIPPending, GetMemberShip(ctx).UserID, now, now).Error; err != nil {
		return nil, err
	}
	entry := &model.SuspiciousIP{}
	if err = db.Where("ip = ?", ip).Take(entry).Error; err != nil {
		return nil, err
	}
	execute := a.execute
	if execute == nil {
		execute = HyperExecute
	}
	dispatchErr := execute(ctx, "toall=", fmt.Sprintf("/opt/cloudland/scripts/kvm/operation/block_ip.sh '%s' 'block_dst'", ip))
	updates := map[string]interface{}{"status": model.SuspiciousIPBlocked, "blocked_at": now, "block_error": ""}
	if dispatchErr != nil {
		updates = map[string]interface{}{"status": model.SuspiciousIPBlockFailed, "block_error": dispatchErr.Error()}
	}
	if err = db.Model(entry).Updates(updates).Error; err != nil {
		return entry, err
	}
	entry, err = a.Get(ctx, entry.UUID)
	if err != nil {
		return nil, err
	}
	return entry, dispatchErr
}
