/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
)

var trafficBillingAdmin = &TrafficBillingAdmin{}

type TrafficBillingAdmin struct{}

// Create marks an instance as traffic billing: it is called, so it is traffic
// billing -- no judgement about whether it "should" be is made here. It pushes
// the vm_traffic_billing_map metric to the instance's current hypervisor via
// SCI/HyperExecute (mirroring vm_instance_map), then records the mark in the
// shared DB so the two control nodes (behind the same nginx/HA pair) agree on
// state, and so migration can look up and carry the mark forward.
func (a *TrafficBillingAdmin) Create(ctx context.Context, instanceUUID string) (entry *model.TrafficBillingMapping, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	// Check for an existing mark up front and bail out immediately with an
	// explicit message -- do not silently no-op, and do not re-push the metric
	// or touch the compute node for an instance that is already marked. This
	// is a plain (scoped) query: Delete always hard-deletes (see
	// trafficBillingDelete), so once any pre-existing soft-deleted leftover
	// rows have been purged (a one-time manual cleanup), no row this query
	// misses can ever exist again -- there is no ongoing soft-delete case for
	// this code to defend against.
	if _, getErr := trafficBillingGetByUUID(db, instanceUUID); getErr == nil {
		err = NewCLError(ErrTrafficBillingAlreadyMarked, fmt.Sprintf("instance %s has already been set as traffic billing mode", instanceUUID), nil)
		return
	}
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, instanceUUID)
	if err != nil {
		logger.Errorf("Failed to get instance %s, %v", instanceUUID, err)
		return
	}
	if instance.Hyper < 0 {
		err = NewCLError(ErrInstanceInvalidState, "Instance has no hypervisor assigned, cannot mark for traffic billing", nil)
		return
	}
	// DB write first, metric push last -- a module-level invariant shared with
	// Delete below (DB change always precedes the compute-node side-effect).
	// If the DB write fails, nothing has been pushed anywhere. If the push
	// then fails, consistency comes from the transaction: every caller today
	// invokes Create as a top-level operation (newTransaction == true above),
	// so setting err here makes the deferred EndTransaction roll back this
	// INSERT along with everything else -- no separate compensating delete is
	// needed, and one used to sit here uselessly (it ran inside the same
	// transaction it was about to be rolled back with). If a future caller
	// ever nests Create inside its own outer transaction, that caller owns
	// deciding whether this failure aborts its transaction too.
	entry, err = trafficBillingCreate(db, instanceUUID, memberShip.UserID)
	if err != nil {
		logger.Error("DB failed to create traffic billing mapping", err)
		return
	}
	domain := fmt.Sprintf("inst-%d", instance.ID)
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/generate_vm_traffic_billing_map.sh 'add' '%s'", domain)
	if hyperErr := HyperExecute(ctx, control, command); hyperErr != nil {
		logger.Errorf("Failed to push traffic billing metric for instance %s: %v", instanceUUID, hyperErr)
		err = hyperErr
		entry = nil
		return
	}
	return
}

// Delete cancels the traffic billing mark: it removes the metric from the
// instance's current hypervisor (if the instance can still be resolved) and
// deletes the DB record. Once removed, the downstream 15m JOIN naturally
// excludes this VM -- there is no separate "cancel" step beyond this.
func (a *TrafficBillingAdmin) Delete(ctx context.Context, instanceUUID string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	// Check the mapping exists BEFORE doing anything else. Idempotency means
	// safely repeating a delete that already succeeded -- it does not mean
	// "succeed regardless of what uuid was passed in". By only proceeding to
	// resolve the instance and dispatch the compute-node command once we
	// already know there is a DB row to un-mark, reaching that dispatch step
	// implies the record existed; there's nothing to roll back if a later
	// step fails, and a never-marked (or typo'd) uuid is rejected up front
	// instead of silently reporting success for a no-op.
	if _, getErr := trafficBillingGetByUUID(db, instanceUUID); getErr != nil {
		err = NewCLError(ErrTrafficBillingNotFound, fmt.Sprintf("instance %s is not currently marked as traffic billing", instanceUUID), nil)
		return
	}
	// DB delete first, metric push last -- the same module-level invariant as
	// Create (DB write always precedes the compute-node side-effect; see its
	// matching comment). If the DB delete then fails, nothing has been pushed
	// anywhere yet. If the push then fails, consistency comes from the
	// transaction, not from an application-level "restore": every caller
	// today invokes Delete as a top-level operation (newTransaction == true
	// above), so setting err here makes the deferred EndTransaction roll back
	// this DELETE too. A compensating re-create used to sit here, but it ran
	// inside the same transaction it was about to be rolled back with (so it
	// never actually persisted), and had it ever persisted it would have
	// inserted a brand-new row via trafficBillingCreate -- model.Model's
	// BeforeCreate always mints a fresh UUID, so it could not have restored
	// the original record's identity anyway.
	if err = trafficBillingDelete(db, instanceUUID); err != nil {
		logger.Error("DB failed to delete traffic billing mapping", err)
		return
	}
	instance, getErr := instanceAdmin.GetInstanceByUUID(ctx, instanceUUID)
	if getErr != nil {
		logger.Infof("Instance %s not found while unmarking traffic billing, nothing to push: %v", instanceUUID, getErr)
		return
	}
	if instance.Hyper < 0 {
		return
	}
	domain := fmt.Sprintf("inst-%d", instance.ID)
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/generate_vm_traffic_billing_map.sh 'remove' '%s'", domain)
	if hyperErr := HyperExecute(ctx, control, command); hyperErr != nil {
		logger.Errorf("Failed to push traffic billing metric removal for instance %s: %v", instanceUUID, hyperErr)
		err = hyperErr
		return
	}
	return
}

// DeleteByInstanceUUID clears the DB mapping only, without pushing a
// HyperExecute remove command and without the Admin permission gate that
// Delete has. It exists for internal cleanup call sites -- namely instance
// deletion (mirrors ipWhitelistAdmin.DeleteByInstanceUUID) -- where the
// compute node's own metric file is already being removed by clear_vm.sh
// itself, so pushing a second remove here would just be a redundant remote
// call, and the caller (a regular tenant deleting their own instance) is not
// necessarily an Admin.
func (a *TrafficBillingAdmin) DeleteByInstanceUUID(ctx context.Context, instanceUUID string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if err = trafficBillingDelete(db, instanceUUID); err != nil {
		logger.Error("DB failed to delete traffic billing mapping", err)
		return
	}
	return
}

// List returns the currently marked traffic-billing instances (maintenance/UI use).
func (a *TrafficBillingAdmin) List(ctx context.Context, offset, limit int64, query string) (total int64, entries []*model.TrafficBillingMapping, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	likePattern := ""
	if query != "" {
		likePattern = "%" + query + "%"
	}
	if total, err = trafficBillingCount(db, likePattern); err != nil {
		logger.Error("DB failed to count traffic billing mappings", err)
		return
	}
	if entries, err = trafficBillingList(db, likePattern, offset, limit); err != nil {
		logger.Error("DB failed to list traffic billing mappings", err)
		return
	}
	return
}

// GetByInstanceUUID returns the mapping record for a single instance, used by
// the REST API's GET .../traffic-billing/:uuid single-item lookup.
func (a *TrafficBillingAdmin) GetByInstanceUUID(ctx context.Context, instanceUUID string) (entry *model.TrafficBillingMapping, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	found, getErr := trafficBillingGetByUUID(db, instanceUUID)
	if getErr != nil {
		err = NewCLError(ErrTrafficBillingNotFound, fmt.Sprintf("instance %s is not currently marked as traffic billing", instanceUUID), getErr)
		return
	}
	entry = found
	return
}

// BroadcastSync pushes the complete "should be traffic billing" domain list
// from the DB to EVERY compute node at once (HyperExecute "toall="), mirroring
// IPWhitelistAdmin.broadcastAll (which reads via GetContextDB, not a
// transaction -- this does the same, since it's a pure read followed by a
// network broadcast, never a DB write). Each node's own
// generate_vm_traffic_billing_map.sh 'sync' action rebuilds its local
// vm_traffic_billing_map.prom from that list -- this is a real two-way
// reconciliation: a domain the DB no longer has gets dropped wherever it
// happens to still be sitting on disk, and a domain the DB has gets
// (re)added on whichever node actually still has that VM. The DB is always
// the source of truth here -- this never accepts an externally supplied list
// to reconcile the DB against, and takes no input. Both the UI's Refresh
// button and the REST API's POST /api/v1/traffic-billing/sync call this same
// method; "sync" only ever means DB-to-compute-node in this system.
//
// Deliberately operator-triggered ONLY. There is no scheduler, ticker or cron
// calling this, and that is a decision rather than a gap: this is the one path
// that can rebuild a node's vm_traffic_billing_map.prom from nothing, but it
// is a fleet-wide fan-out ("toall="), and the events that can desync the
// metric (VM migration, node rebuild) occur on a months-scale cadence. Paying
// a cluster-wide broadcast every N hours to cover something that rare is not a
// proportionate cost; ops triggers a resync when such an event happens. Note
// also that generate_vm_traffic_billing_map.sh's "gc" action -- which
// report_rc.sh's halfday_job does run periodically -- is NOT a substitute: it
// only drops domains already present in the local file and can never add one
// back (that is why the script has no "full" action, unlike
// generate_vm_instance_map.sh). Please do not "fix" the absence of a periodic
// sync by adding one.
func (a *TrafficBillingAdmin) BroadcastSync(ctx context.Context) (err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	entries, err := trafficBillingListAll(db)
	if err != nil {
		logger.Error("DB failed to list traffic billing mappings for sync", err)
		return
	}
	instanceUUIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		instanceUUIDs = append(instanceUUIDs, e.InstanceUUID)
	}
	// Resolve every instance in one query instead of one GetInstanceByUUID
	// call per entry (N+1) -- domain only needs the numeric instance ID, none
	// of GetInstanceByUUID's preloads/permission filtering.
	instanceIDByUUID, err := trafficBillingResolveInstanceIDs(db, instanceUUIDs)
	if err != nil {
		logger.Error("DB failed to resolve instances for traffic billing sync", err)
		return
	}
	type mappingEntry struct {
		Domain     string `json:"domain"`
		InstanceID string `json:"instance_id"`
	}
	payload := struct {
		Mappings []mappingEntry `json:"mappings"`
	}{Mappings: make([]mappingEntry, 0, len(entries))}
	for _, e := range entries {
		instanceID, ok := instanceIDByUUID[e.InstanceUUID]
		if !ok {
			logger.Errorf("BroadcastSync: instance %s no longer resolvable, excluding from payload", e.InstanceUUID)
			continue
		}
		payload.Mappings = append(payload.Mappings, mappingEntry{
			Domain:     fmt.Sprintf("inst-%d", instanceID),
			InstanceID: e.InstanceUUID,
		})
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal traffic billing sync payload", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(jsonData)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/generate_vm_traffic_billing_map.sh 'sync' '%s'", encoded)
	if err = HyperExecute(ctx, "toall=", command); err != nil {
		logger.Error("HyperExecute broadcast of traffic billing sync failed", err)
		return
	}
	return
}

// Everything below is the ONLY place in this file that touches *gorm.DB
// directly. Every Admin method above goes through exactly one of these --
// no method does its own inline db.Where/db.Create/db.Find, so there is a
// single, auditable spot per operation for how TrafficBillingMapping rows
// are read or written.

func trafficBillingGetByUUID(db *gorm.DB, instanceUUID string) (*model.TrafficBillingMapping, error) {
	entry := &model.TrafficBillingMapping{}
	err := db.Where("instance_uuid = ?", instanceUUID).Take(entry).Error
	return entry, err
}

// TrafficBillingIsMarked reports whether instanceUUID is currently marked for
// traffic billing. Exported and deliberately NOT permission-gated (unlike
// TrafficBillingAdmin's methods): it exists for internal call sites that
// already hold their own DB handle and are not acting on behalf of an
// authenticated caller -- migration.go's target_migration.sh dispatch
// (package routes) and migrate_vm.go's complete_migration.sh dispatch
// (package rpcs, whose RPC-callback ctx carries no MemberShip at all) both
// need this exact check inline before invoking migration scripts that have no
// reliable local way to know it themselves.
func TrafficBillingIsMarked(db *gorm.DB, instanceUUID string) (bool, error) {
	_, err := trafficBillingGetByUUID(db, instanceUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func trafficBillingCreate(db *gorm.DB, instanceUUID string, creater int64) (*model.TrafficBillingMapping, error) {
	entry := &model.TrafficBillingMapping{
		Model:        model.Model{Creater: creater},
		InstanceUUID: instanceUUID,
	}
	err := db.Create(entry).Error
	return entry, err
}

func trafficBillingDelete(db *gorm.DB, instanceUUID string) error {
	// Unscoped: model.Model embeds a DeletedAt field, so a plain Delete() here
	// would be gorm's soft-delete (an UPDATE ... SET deleted_at=now()), leaving
	// the row (and its unique instance_uuid constraint) physically in place --
	// invisible to later Find/Take, but still blocking a fresh Create for the
	// same instance_uuid with a duplicate-key error. This must be a real DELETE.
	return db.Unscoped().Where("instance_uuid = ?", instanceUUID).Delete(&model.TrafficBillingMapping{}).Error
}

// likePattern is a full "%...%" LIKE pattern (or "" for no filter), always
// passed through as a bind parameter -- never interpolated into the WHERE
// clause itself -- so caller-supplied search text can't inject SQL.
func trafficBillingCount(db *gorm.DB, likePattern string) (int64, error) {
	q := db.Model(&model.TrafficBillingMapping{})
	if likePattern != "" {
		q = q.Where("instance_uuid LIKE ?", likePattern)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func trafficBillingList(db *gorm.DB, likePattern string, offset, limit int64) ([]*model.TrafficBillingMapping, error) {
	q := db
	if likePattern != "" {
		q = q.Where("instance_uuid LIKE ?", likePattern)
	}
	entries := []*model.TrafficBillingMapping{}
	err := q.Offset(offset).Limit(limit).Find(&entries).Error
	return entries, err
}

func trafficBillingListAll(db *gorm.DB) ([]*model.TrafficBillingMapping, error) {
	var entries []*model.TrafficBillingMapping
	err := db.Find(&entries).Error
	return entries, err
}

// trafficBillingResolveInstanceIDs batches instance-UUID-to-ID resolution for
// BroadcastSync, replacing what would otherwise be one query per mapping entry.
func trafficBillingResolveInstanceIDs(db *gorm.DB, instanceUUIDs []string) (map[string]int64, error) {
	var instances []*model.Instance
	if err := db.Where("uuid IN (?)", instanceUUIDs).Find(&instances).Error; err != nil {
		return nil, err
	}
	idByUUID := make(map[string]int64, len(instances))
	for _, inst := range instances {
		idByUUID[inst.UUID] = inst.ID
	}
	return idByUUID, nil
}
