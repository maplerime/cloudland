package routes

import (
	"context"
	"errors"
	"testing"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

func TestInstanceInterfaceCreateFailure(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantErr := errors.New("injected interface insert failure")
	attempts := 0
	db.Callback().Create().Before("gorm:create").Register("test:fail_interface_insert", func(scope *gorm.Scope) {
		if _, ok := scope.Value.(*model.Interface); ok {
			attempts++
			scope.Err(wantErr)
		}
	})
	ctx := SetContextDB(context.Background(), db)
	ctx = (&MemberShip{UserID: 1, OrgID: 1, Role: model.Admin}).SetContext(ctx)
	subnet := &model.Subnet{Model: model.Model{ID: 1}, Name: "primary", Type: "internal"}
	instance := &model.Instance{Model: model.Model{ID: 1}}

	iface, err := CreateInterface(ctx, subnet, instance.ID, 1, -1, 1000, 1000, "", "", "eth0", "instance", nil, false)
	if err != wantErr || iface != nil {
		t.Fatalf("failed insert returned iface=%+v err=%v; want nil and original error", iface, err)
	}

	for _, subnetCount := range []int{1, 2} {
		info := &InterfaceInfo{Subnets: []*model.Subnet{subnet}, Count: 1}
		if subnetCount == 2 {
			info.Subnets = append(info.Subnets, &model.Subnet{Model: model.Model{ID: 2}, Name: "fallback", Type: "internal"})
		}
		attempts = 0
		iface, primary, err := instanceAdmin.createInterface(ctx, info, instance, "eth0")
		if err != wantErr || iface != nil || primary != nil {
			t.Fatalf("createInterface swallowed insert failure: iface=%+v subnet=%+v err=%v", iface, primary, err)
		}
		if attempts != subnetCount {
			t.Fatalf("attempted %d subnets, want %d", attempts, subnetCount)
		}
		// A single requested IP used to let secondary allocation overwrite the
		// insert error with nil, followed by a panic while building metadata.
		_, metadata, err := instanceAdmin.buildMetadata(ctx, info, nil, "", 22, nil, instance, 0, 0, 0, 0, "", nil)
		if err != wantErr || metadata != "" {
			t.Fatalf("buildMetadata did not propagate insert failure: metadata=%q err=%v", metadata, err)
		}
	}
}
