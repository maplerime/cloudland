package rpcs

import (
	"context"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"reflect"
	"testing"
	. "web/src/common"
	"web/src/model"
)

func TestObserveConnectionReport(t *testing.T) {
	if Get("observe_connection") == nil {
		t.Fatal("RPC not registered")
	}
	cmd, args := DecodeCommand("observe_connection.sh '3' '192.0.2.10' '198.51.100.20' '192.0.2.10'")
	if cmd != "observe_connection" || !reflect.DeepEqual(args, []string{"observe_connection.sh", "3", "192.0.2.10", "198.51.100.20", "192.0.2.10"}) {
		t.Fatalf("wrong report decoding: %s %q", cmd, args)
	}
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.SuspiciousIP{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := SetContextDB(context.Background(), db)
	status, err := ObserveConnection(ctx, args)
	if err != nil || status == "" {
		t.Fatalf("record: %q %v", status, err)
	}
	var entries []model.SuspiciousIP
	if err = db.Order("ip").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 unique records: %+v", entries)
	}
	for _, e := range entries {
		if e.Status != model.SuspiciousIPPending || e.LastSeenNode == nil || *e.LastSeenNode != 3 || e.LastSeenAt == nil || e.BlockedAt != nil {
			t.Fatalf("observation unexpectedly blocked or omitted metadata: %+v", e)
		}
	}
}

func TestObserveConnectionRejectInvalidReport(t *testing.T) {
	for _, args := range [][]string{nil, {"observe_connection.sh", "3"}, {"observe_connection.sh", "bad", "192.0.2.1"}, {"observe_connection.sh", "-1", "192.0.2.1"}, {"observe_connection.sh", "3", "192.0.2.1", "1.2.3.4';touch /tmp/injected"}, {"observe_connection.sh", "3", ""}} {
		_, err := ObserveConnection(context.Background(), args)
		if err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
