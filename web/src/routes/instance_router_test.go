package routes

import (
	"testing"

	"web/src/model"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

func TestLoadInstanceRouter(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AutoMigrate(&model.Router{}, &model.Instance{}).Error; err != nil {
		t.Fatal(err)
	}
	active := &model.Router{Name: "active"}
	deleted := &model.Router{Name: "deleted"}
	for _, router := range []*model.Router{active, deleted} {
		if err := db.Create(router).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(deleted).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		routerID int64
		wantVPC  bool
	}{
		{"no VPC", 0, false},
		{"active VPC", active.ID, true},
		{"deleted VPC", deleted.ID, false},
		{"missing VPC", deleted.ID + 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := &model.Instance{Hostname: tc.name + "-unknown", RouterID: tc.routerID}
			if err := db.Create(instance).Error; err != nil {
				t.Fatal(err)
			}
			if err := loadInstanceRouter(db, instance); err != nil {
				t.Fatal(err)
			}
			if (instance.Router != nil) != tc.wantVPC {
				t.Fatalf("unexpected VPC: %+v", instance.Router)
			}
			if tc.wantVPC && instance.Router.ID != tc.routerID {
				t.Fatalf("loaded wrong VPC: %+v", instance.Router)
			}
			var stored model.Instance
			if err := db.First(&stored, instance.ID).Error; err != nil {
				t.Fatal(err)
			}
			if instance.RouterID != tc.routerID || stored.RouterID != tc.routerID {
				t.Fatal("VPC reference needed for cleanup was changed")
			}
		})
	}
	var stored model.Router
	if err := db.Unscoped().First(&stored, deleted.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DeletedAt == nil {
		t.Fatal("deleted VPC was restored")
	}
	// Query failures must still be reported, rather than treated as missing VPCs.
	if err := db.DropTable(&model.Router{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := loadInstanceRouter(db, &model.Instance{RouterID: active.ID}); err == nil {
		t.Fatal("database query error was ignored")
	}
}
