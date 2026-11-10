package routes

import (
	"context"
	"errors"
	"testing"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	. "web/src/common"
	"web/src/model"
)

func suspiciousIPTestContext(t *testing.T) (context.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.AutoMigrate(&model.SuspiciousIP{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Admin}).SetContext(SetContextDB(context.Background(), db))
	return ctx, db
}

func TestSuspiciousIPRecordAndBlock(t *testing.T) {
	ctx, _ := suspiciousIPTestContext(t)
	if err := RecordObservedSuspiciousIPs(ctx, 3, []string{"192.0.2.10", "192.0.2.10", "2001:0db8::1"}); err != nil {
		t.Fatal(err)
	}
	a := &SuspiciousIPAdmin{execute: func(_ context.Context, control, command string) error {
		if control != "toall=" || command != "/opt/cloudland/scripts/kvm/operation/block_ip.sh '192.0.2.10' 'block_dst'" {
			t.Fatalf("wrong block command: %s %s", control, command)
		}
		return nil
	}}
	entry, err := a.Block(ctx, "192.0.2.10")
	if err != nil || entry.Status != model.SuspiciousIPBlocked || entry.BlockedAt == nil {
		t.Fatalf("block: %+v %v", entry, err)
	}
	entry, err = a.Update(ctx, entry.UUID, entry.IP, "operator note")
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordObservedSuspiciousIPs(ctx, 4, []string{entry.IP}); err != nil {
		t.Fatal(err)
	}
	entry, err = a.Get(ctx, entry.UUID)
	if err != nil || entry.Status != model.SuspiciousIPBlocked || entry.Note != "operator note" || *entry.LastSeenNode != 4 {
		t.Fatalf("observation overwrote manual state: %+v %v", entry, err)
	}
	total, _, err := a.List(ctx, 0, 50, "192.0.2", model.SuspiciousIPBlocked)
	if err != nil || total != 1 {
		t.Fatalf("filtered list: %d %v", total, err)
	}
	if _, err = a.Update(ctx, entry.UUID, "192.0.2.11", ""); err == nil {
		t.Fatal("changed blocked address")
	}
	if _, err = a.Block(ctx, "2001:db8::1"); err == nil {
		t.Fatal("IPv4 block script accepted IPv6")
	}
}

func TestSuspiciousIPDirectBlockAndFailure(t *testing.T) {
	ctx, _ := suspiciousIPTestContext(t)
	failure := errors.New("SCI unavailable")
	a := &SuspiciousIPAdmin{execute: func(context.Context, string, string) error { return failure }}
	entry, err := a.Block(ctx, "198.51.100.1")
	if !errors.Is(err, failure) || entry.Status != model.SuspiciousIPBlockFailed || entry.BlockError == "" {
		t.Fatalf("failure not retained: %+v %v", entry, err)
	}
	a.execute = func(context.Context, string, string) error { return nil }
	entry, err = a.Block(ctx, entry.IP)
	if err != nil || entry.Status != model.SuspiciousIPBlocked || entry.BlockError != "" {
		t.Fatalf("success not persisted: %+v %v", entry, err)
	}
	total, entries, err := a.List(ctx, 0, 50, "", "")
	if err != nil || total != 1 || entries[0].IP != entry.IP {
		t.Fatalf("direct block absent from list: %+v %v", entries, err)
	}
}

func TestSuspiciousIPCRUDAndPermissions(t *testing.T) {
	ctx, db := suspiciousIPTestContext(t)
	a := &SuspiciousIPAdmin{}
	entry, err := a.Create(ctx, "2001:0db8::1", "note")
	if err != nil || entry.IP != "2001:db8::1" {
		t.Fatalf("create: %+v %v", entry, err)
	}
	entry, err = a.Update(ctx, entry.UUID, "192.0.2.1", "changed")
	if err != nil || entry.IP != "192.0.2.1" || entry.Note != "changed" {
		t.Fatalf("update: %+v %v", entry, err)
	}
	denied := (&MemberShip{Role: model.Writer}).SetContext(ctx)
	if _, _, err = a.List(denied, 0, 10, "", ""); !errors.Is(err, ErrSuspiciousIPPermission) {
		t.Fatal("non-admin list allowed")
	}
	if _, err = a.Create(denied, "192.0.2.2", ""); !errors.Is(err, ErrSuspiciousIPPermission) {
		t.Fatal("non-admin create allowed")
	}
	if _, err = a.Update(denied, entry.UUID, entry.IP, ""); !errors.Is(err, ErrSuspiciousIPPermission) {
		t.Fatal("non-admin update allowed")
	}
	if err = a.Delete(denied, entry.UUID); !errors.Is(err, ErrSuspiciousIPPermission) {
		t.Fatal("non-admin delete allowed")
	}
	if _, err = a.Block(denied, entry.IP); !errors.Is(err, ErrSuspiciousIPPermission) {
		t.Fatal("non-admin block allowed")
	}
	if err = a.Delete(ctx, entry.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Get(ctx, entry.UUID); !gorm.IsRecordNotFoundError(err) {
		t.Fatalf("deleted record found: %v", err)
	}
	if err = RecordObservedSuspiciousIPs(ctx, 2, []string{entry.IP}); err != nil {
		t.Fatal(err)
	}
	var count int
	db.Model(&model.SuspiciousIP{}).Count(&count)
	if count != 1 {
		t.Fatalf("rediscovery failed: %d", count)
	}
	if err = RecordObservedSuspiciousIPs(ctx, 2, []string{"192.0.2.2", "bad"}); err == nil {
		t.Fatal("invalid report accepted")
	}
	db.Model(&model.SuspiciousIP{}).Count(&count)
	if count != 1 {
		t.Fatal("invalid batch partially saved")
	}
}
