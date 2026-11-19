/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

History:
   Date     Who ID    Description
   -------- --- ---   -----------
   01/13/19 nanjj  Initial code

*/

package rpcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/spf13/viper"
	. "web/src/common"
	"web/src/model"
)

func TestTargetMigrationRecovery(t *testing.T) {
	var requests []ExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		requests = append(requests, req)
		fmt.Fprint(w, `{ "Status": "accepted" }`)
	}))
	defer server.Close()
	oldEndpoint := viper.Get("sci.endpoint")
	viper.Set("sci.endpoint", server.URL)
	defer viper.Set("sci.endpoint", oldEndpoint)

	for _, name := range []string{"same_target", "migrating_grace", "warm", "completed", "wrong_target", "shutoff", "paused", "completed_phase", "newer_migration", "source_unavailable", "source_report"} {
		t.Run(name, func(t *testing.T) {
			requests = nil
			db, err := gorm.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB().SetMaxOpenConns(1)
			if err = db.AutoMigrate(&model.Migration{}, &model.Task{}, &model.Hyper{}, &model.Instance{}, &model.Volume{}).Error; err != nil {
				t.Fatal(err)
			}
			ctx := SetContextDB(context.Background(), db)
			instance := &model.Instance{Hyper: 2, Status: model.InstanceStatusRunning}
			if name == "migrating_grace" {
				instance.Status = model.InstanceStatusMigrating
			}
			migration := &model.Migration{Type: "cold", Status: "target_prepared", SourceHyper: 1, TargetHyper: 2}
			reportedStatus := "running"
			task := &model.Task{Name: "Prepare_Source", Status: "in_progress"}
			source := &model.Hyper{Hostid: 1, Hostname: "source", Status: 1}
			switch name {
			case "warm":
				migration.Type = "warm"
			case "completed":
				migration.Status = "completed"
			case "wrong_target":
				migration.TargetHyper = 3
			case "shutoff":
				reportedStatus = "shut_off"
			case "paused":
				reportedStatus = "paused"
			case "completed_phase":
				task.Status = "completed"
			case "source_unavailable":
				source.Status = 10
			}
			mustCreate := func(value interface{}) {
				t.Helper()
				if err := db.Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			mustCreate(instance)
			migration.InstanceID = instance.ID
			mustCreate(migration)
			task.Mission = migration.ID
			mustCreate(task)
			mustCreate(source)
			mustCreate(&model.Hyper{Hostid: 2, Hostname: "target", Status: 1})
			if name == "newer_migration" {
				mustCreate(&model.Migration{InstanceID: instance.ID, Type: "cold", Status: "completed", SourceHyper: 2, TargetHyper: 3})
			}
			// These cases exercise the real periodic-report entry point, with
			// unchanged ownership and an unexpired migrating grace period.
			if name == "same_target" || name == "migrating_grace" || name == "shutoff" || name == "paused" {
				_, err = InstanceStatus(ctx, []string{"inst_status", "2", fmt.Sprintf("%d %s", instance.ID, reportedStatus)})
			} else {
				reportedInstance := *instance
				reportedInstance.Status = model.InstanceStatus(reportedStatus)
				if name == "source_report" {
					reportedInstance.Hyper = 1
				}
				err = syncMigration(ctx, &reportedInstance)
			}
			if name == "source_unavailable" {
				if err == nil || !strings.Contains(err.Error(), "Source hyper") {
					t.Fatalf("expected source dispatch error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := 0
			if name == "same_target" || name == "source_report" || name == "shutoff" || name == "paused" {
				want = 1
			}
			if len(requests) != want {
				t.Fatalf("got %d source commands, want %d", len(requests), want)
			}
			if want == 1 {
				if requests[0].Control != "inter=1" || !strings.Contains(requests[0].Command, "/source_migration.sh") || !strings.Contains(requests[0].Command, "'cold'") {
					t.Fatalf("unexpected source command: %+v", requests[0])
				}
			}
			if name == "shutoff" || name == "paused" {
				if err := db.First(instance, instance.ID).Error; err != nil {
					t.Fatal(err)
				}
				if instance.Status.String() != reportedStatus {
					t.Fatalf("got instance status %s, want %s", instance.Status, reportedStatus)
				}
			}
			if err := db.First(task, task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if name != "completed_phase" && task.Status != "in_progress" {
				t.Fatalf("recovery prematurely completed source phase: %s", task.Status)
			}
		})
	}
}
