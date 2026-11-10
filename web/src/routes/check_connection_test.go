package routes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"strings"
	"testing"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/unknwon/i18n"
)

func TestCheckConnectionBatch(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.CheckConnection{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Admin}).SetContext(SetContextDB(context.Background(), db))
	calls := 0
	a := &CheckConnectionAdmin{execute: func(_ context.Context, control, command string) error {
		calls++
		if control != "toall=" || (command != "/opt/cloudland/scripts/kvm/check_connection.sh '192.0.2.1'" && command != "/opt/cloudland/scripts/kvm/check_connection.sh '2001:db8::1'") {
			t.Fatalf("unexpected broadcast: %q %q", control, command)
		}
		if calls == 1 {
			return errors.New("SCI unavailable")
		}
		return nil
	}}
	// Validate the whole batch before persisting or broadcasting any address.
	for _, role := range []model.Role{model.None, model.Reader, model.Writer, model.Owner} {
		denied := (&MemberShip{UserID: 7, OrgID: 8, Role: role}).SetContext(ctx)
		if _, err = a.Create(denied, []string{"192.0.2.1"}); err == nil {
			t.Fatalf("role %s can submit global check", role)
		}
	}
	if _, err = a.Create(ctx, []string{"192.0.2.1", "192.0.2.2'; touch /tmp/injected"}); err == nil {
		t.Fatal("accepted invalid IP")
	}
	var count int
	db.Model(&model.CheckConnection{}).Count(&count)
	if count != 0 || calls != 0 {
		t.Fatal("invalid batch had side effects")
	}
	entries, err := a.Create(ctx, []string{"192.0.2.1", "2001:0db8::1", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || calls != 2 || entries[0].Status != "failed" || entries[1].Status != "submitted" || entries[1].IP != "2001:db8::1" {
		t.Fatalf("unexpected entries: %+v, calls=%d", entries, calls)
	}
	other := (&MemberShip{UserID: 9, OrgID: 10, Role: model.Writer}).SetContext(ctx)
	total, _, err := a.List(other, 0, 50)
	if err != nil || total != 0 {
		t.Fatalf("organization leak: %d %v", total, err)
	}
	reader := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Reader}).SetContext(ctx)
	if _, err = a.Create(reader, []string{"192.0.2.3"}); err == nil {
		t.Fatal("reader can create")
	}
	total, stored, err := a.List(ctx, 0, 50)
	if err != nil || total != 2 {
		t.Fatalf("list: %d %v", total, err)
	}
	for _, e := range stored {
		if e.Owner != 8 || e.Creater != 7 {
			t.Fatalf("invalid persisted record: %+v", e)
		}
		for _, expected := range entries {
			if e.UUID == expected.UUID && (e.Status != expected.Status || e.DispatchError != expected.DispatchError) {
				t.Fatalf("dispatch status not persisted: %+v", e)
			}
		}
	}
}

func TestCheckConnectionInput(t *testing.T) {
	for _, input := range [][]string{nil, {""}, {"example.com"}, {"192.0.2.1/24"}, {"::1%eth0"}, make([]string, MaxCheckConnectionIPs+1)} {
		if _, err := NormalizeCheckConnectionIPs(input); err == nil {
			t.Fatal(fmt.Sprintf("accepted %q", input))
		}
	}
}

func TestCheckConnectionDelete(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.CheckConnection{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Admin}).SetContext(SetContextDB(context.Background(), db))
	entries := []*model.CheckConnection{
		{Owner: 8, IP: "192.0.2.1", Status: "submitted"}, {Owner: 8, IP: "192.0.2.1", Status: "submitted"},
		{Owner: 9, IP: "192.0.2.1", Status: "submitted"}, {Owner: 8, IP: "192.0.2.2", Status: "submitted"},
	}
	for _, entry := range entries {
		if err = db.Create(entry).Error; err != nil {
			t.Fatal(err)
		}
	}
	commands := []string{}
	fail := false
	a := &CheckConnectionAdmin{execute: func(_ context.Context, control, command string) error {
		if control != "toall=" {
			t.Fatal(control)
		}
		commands = append(commands, command)
		if fail {
			return errors.New("SCI unavailable")
		}
		return nil
	}}
	for _, role := range []model.Role{model.None, model.Reader, model.Writer, model.Owner} {
		denied := (&MemberShip{OrgID: 8, Role: role}).SetContext(ctx)
		if err = a.Delete(denied, entries[0].UUID); err == nil {
			t.Fatalf("role %s can delete global check", role)
		}
	}
	if err = a.Delete(ctx, entries[2].UUID); err == nil {
		t.Fatal("cross-organization delete allowed")
	}
	for _, entry := range entries[:2] {
		if err = a.Delete(ctx, entry.UUID); err != nil {
			t.Fatal(err)
		}
	}
	if len(commands) != 0 {
		t.Fatal("IP still requested by another record was removed from nodes")
	}
	other := (&MemberShip{OrgID: 9, Role: model.Admin}).SetContext(ctx)
	if err = a.Delete(other, entries[2].UUID); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0] != "/opt/cloudland/scripts/kvm/check_connection.sh '192.0.2.1' 'delete'" {
		t.Fatalf("wrong removal: %q", commands)
	}
	fail = true
	if err = a.Delete(ctx, entries[3].UUID); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	var stored model.CheckConnection
	if err = db.Where("uuid = ?", entries[3].UUID).Take(&stored).Error; err != nil {
		t.Fatal("failed cleanup deleted record")
	}
	fail = false
	if err = a.Delete(ctx, entries[3].UUID); err != nil {
		t.Fatal(err)
	}
}

func TestCheckConnectionInstanceCleanup(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.CheckConnection{}, &model.Interface{}, &model.Address{}, &model.FloatingIp{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := SetContextDB(context.Background(), db)
	iface := &model.Interface{Instance: 11}
	if err = db.Create(iface).Error; err != nil {
		t.Fatal(err)
	}
	for _, addr := range []*model.Address{{Address: "192.0.2.1", Interface: iface.ID}, {Address: "2001:db8::10", SecondInterface: iface.ID}} {
		if err = db.Create(addr).Error; err != nil {
			t.Fatal(err)
		}
	}
	fip := &model.FloatingIp{InstanceID: 11, FipAddress: "198.51.100.10", IntAddress: "192.0.2.1"}
	if err = db.Create(fip).Error; err != nil {
		t.Fatal(err)
	}
	for i, ip := range []string{"192.0.2.1", "2001:db8::10", "198.51.100.10", "203.0.113.99"} {
		if err = db.Create(&model.CheckConnection{Owner: int64(i + 1), IP: ip, Status: "submitted"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	commands := map[string]bool{}
	a := &CheckConnectionAdmin{execute: func(_ context.Context, control, command string) error {
		if control != "toall=" {
			t.Fatal(control)
		}
		commands[command] = true
		return nil
	}}
	ips, err := a.instanceIPs(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	// The normal deletion flow detaches floating IPs before final cleanup.
	if err = db.Model(fip).Update("instance_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err = a.DeleteByIPs(ctx, ips); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"192.0.2.1", "2001:db8::10", "198.51.100.10"} {
		if !commands[fmt.Sprintf("/opt/cloudland/scripts/kvm/check_connection.sh '%s' 'delete'", ip)] {
			t.Fatalf("IP %s was not removed", ip)
		}
	}
	var entries []model.CheckConnection
	if err = db.Find(&entries).Error; err != nil || len(entries) != 1 || entries[0].IP != "203.0.113.99" {
		t.Fatalf("unrelated checks changed: %+v %v", entries, err)
	}
	if err = a.DeleteByInstance(ctx, &model.Instance{Model: model.Model{ID: 11}}); err != nil || len(commands) != 3 {
		t.Fatalf("duplicate callback cleanup: %v %q", err, commands)
	}
}

func TestCheckConnectionTemplate(t *testing.T) {
	content, err := os.ReadFile("../../templates/check_connection.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("check_connection").Parse(string(content))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tmpl.Parse(`{{define "_head"}}{{end}}{{define "_left"}}{{end}}{{define "_pagination"}}{{end}}{{define "_suspicious_ips"}}{{end}}{{define "_check_connection_tabs"}}{{end}}{{define "_footer"}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ lang, title, status, otherTitle string }{
		{"zh-CN", "连接检查", "下发失败", "Check Connections"},
		{"en-US", "Check Connections", "Failed", "连接检查"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			if !i18n.IsExist(tc.lang) {
				if err := i18n.SetMessage(tc.lang, "../../conf/locale/locale_"+tc.lang+".ini"); err != nil {
					t.Fatal(err)
				}
			}
			var rendered bytes.Buffer
			err := tmpl.Execute(&rendered, map[string]interface{}{"i18n": i18n.Locale{Lang: tc.lang}, "Total": 1, "CanWrite": true, "Checks": []*model.CheckConnection{{IP: "192.0.2.1", Status: "failed", DispatchError: "<unavailable>"}}})
			if err != nil {
				t.Fatal(err)
			}
			output := rendered.String()
			for _, expected := range []string{"&lt;unavailable&gt;", "192.0.2.1", tc.title, tc.status} {
				if !strings.Contains(output, expected) {
					t.Fatalf("rendered page missing %q", expected)
				}
			}
			if strings.Contains(output, tc.otherTitle) || strings.Contains(output, "Check_Connection_") {
				t.Fatal("page contains mixed languages or untranslated keys")
			}
		})
	}
}
