package routes

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unknwon/i18n"
	macaron "gopkg.in/macaron.v1"
	. "web/src/common"
	"web/src/model"
)

func TestSuspiciousIPTemplates(t *testing.T) {
	tmpl, err := template.ParseFiles("../../templates/check_connection.tmpl", "../../templates/_suspicious_ips.tmpl", "../../templates/suspicious_ips_edit.tmpl", "../../templates/_check_connection_tabs.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tmpl.Parse(`{{define "_head"}}{{end}}{{define "_left"}}{{end}}{{define "_pagination"}}{{end}}{{define "_suspicious_ips"}}{{template "_suspicious_ips.tmpl" .}}{{end}}{{define "_check_connection_tabs"}}{{template "_check_connection_tabs.tmpl" .}}{{end}}{{define "_footer"}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	node := int32(4)
	entry := &model.SuspiciousIP{IP: "192.0.2.1", Status: model.SuspiciousIPBlocked, Note: "<note>", LastSeenAt: &now, LastSeenNode: &node, BlockedAt: &now}
	for _, tc := range []struct{ lang, title string }{{"zh-CN", "可疑 IP"}, {"en-US", "Suspicious IPs"}} {
		if !i18n.IsExist(tc.lang) {
			if err := i18n.SetMessage(tc.lang, "../../conf/locale/locale_"+tc.lang+".ini"); err != nil {
				t.Fatal(err)
			}
		}
		data := map[string]interface{}{"i18n": i18n.Locale{Lang: tc.lang}, "Total": 1, "Limit": int64(20), "IP": "", "Entries": []*model.SuspiciousIP{entry}, "Tab": "suspicious", "CanManageSuspicious": true, "Entry": entry, "Link": "/check_connection/suspicious/new"}
		for _, name := range []string{"check_connection.tmpl", "suspicious_ips_edit.tmpl"} {
			var out bytes.Buffer
			if err = tmpl.ExecuteTemplate(&out, name, data); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.title) || !strings.Contains(out.String(), "&lt;note&gt;") || strings.Contains(out.String(), "Suspicious_IP_") {
				t.Fatalf("untranslated or unsafe template: %s", out.String())
			}
			if strings.Contains(out.String(), `name="status"`) || strings.Contains(out.String(), "<th>"+i18n.Tr(tc.lang, "Status")+"</th>") {
				t.Fatal("page still displays status")
			}
			if strings.Contains(out.String(), "/blocked-ips") {
				t.Fatal("suspicious IP management links to blocked-ips")
			}
		}
	}
}

func TestCheckConnectionTabs(t *testing.T) {
	ctx, db := suspiciousIPTestContext(t)
	if err := db.AutoMigrate(&model.CheckConnection{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CheckConnection{Owner: 8, IP: "192.0.2.250", Status: "submitted"}).Error; err != nil {
		t.Fatal(err)
	}
	ips := []string{}
	for i := 1; i <= 25; i++ {
		ips = append(ips, fmt.Sprintf("198.51.100.%d", i))
	}
	if err := RecordObservedSuspiciousIPs(ctx, 3, ips); err != nil {
		t.Fatal(err)
	}
	if !i18n.IsExist("en-US") {
		if err := i18n.SetMessage("en-US", "../../conf/locale/locale_en-US.ini"); err != nil {
			t.Fatal(err)
		}
	}
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{{
		"GetString": func(string) string { return "" }, "Title": strings.Title, "HasPrefix": strings.HasPrefix,
	}}}))
	m.Use(func(c *macaron.Context) {
		c.Req.Request = c.Req.WithContext(ctx)
		c.Data["i18n"] = i18n.Locale{Lang: "en-US"}
		c.Data["Link"] = c.Req.URL.Path
	})
	m.Get("/check_connection", checkConnectionView.List)
	m.Get("/check_connection/suspicious", suspiciousIPView.List)
	m.Post("/check_connection/suspicious/:uuid/block", suspiciousIPView.Block)
	m.Post("/check_connection/suspicious/:uuid/unblock", suspiciousIPView.Unblock)
	previousAdmin := suspiciousIPAdmin
	t.Cleanup(func() { suspiciousIPAdmin = previousAdmin })
	var blockCommands []string
	suspiciousIPAdmin = &SuspiciousIPAdmin{execute: func(_ context.Context, control, command string) error {
		if control != "toall=" {
			t.Fatalf("unexpected block control: %s", control)
		}
		blockCommands = append(blockCommands, command)
		return nil
	}}
	request := func(path string, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Fatalf("tab page: %d %s", w.Code, w.Body.String())
		}
		return w
	}
	output := request("/check_connection", 200).Body.String()
	if !strings.Contains(output, `class="item active" href="/check_connection"`) || !strings.Contains(output, "192.0.2.250") || strings.Contains(output, `id="suspicious-ips"`) {
		t.Fatal("connections tab did not isolate check requests")
	}
	output = request("/check_connection/suspicious?offset=20&limit=10&ip=198.51.100.", 200).Body.String()
	for _, expected := range []string{`id="suspicious-ips"`, "198.51.100.5", "offset=10", "limit=10", "ip=198.51.100.", `class="item active" href="/check_connection/suspicious"`, `class="pagination-select"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("suspicious tab missing %q", expected)
		}
	}
	if strings.Contains(output, "<td>198.51.100.25</td>") {
		t.Fatal("suspicious list ignored its offset")
	}
	if strings.Contains(output, "192.0.2.250") || strings.Contains(output, `name="ips"`) {
		t.Fatal("suspicious tab contains the connection check form or records")
	}
	entry := &model.SuspiciousIP{}
	if err := db.Where("ip = ?", "198.51.100.1").Take(entry).Error; err != nil {
		t.Fatal(err)
	}
	postAction := func(action, body string, code int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/check_connection/suspicious/"+entry.UUID+"/"+action, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		m.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("web block returned %d: %s", w.Code, w.Body.String())
		}
		if code == 302 && w.Header().Get("Location") != "/check_connection/suspicious" {
			t.Fatal("block did not return to suspicious IP tab")
		}
	}
	postAction("block", "timeout=-1", 400)
	postAction("block", "timeout=1800", 302)
	if len(blockCommands) != 1 || blockCommands[0] != "/opt/cloudland/scripts/kvm/operation/block_ip.sh '198.51.100.1' 'block_dst' '1800'" {
		t.Fatalf("web timeout not dispatched correctly: %q", blockCommands)
	}
	postAction("unblock", "", 302)
	if len(blockCommands) != 2 || blockCommands[1] != "/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '198.51.100.1'" {
		t.Fatalf("web unblock not dispatched correctly: %q", blockCommands)
	}
	if _, err := suspiciousIPAdmin.Get(ctx, entry.UUID); err != nil {
		t.Fatal("web unblock deleted record")
	}
	ctx = (&MemberShip{UserID: 7, OrgID: 8, Role: model.Writer}).SetContext(SetContextDB(context.Background(), db))
	output = request("/check_connection", 200).Body.String()
	if strings.Contains(output, `href="/check_connection/suspicious"`) || !strings.Contains(output, "192.0.2.250") {
		t.Fatal("admin tab visible to writer or check list missing")
	}
	request("/check_connection/suspicious", 403)
	postAction("block", "timeout=1800", 403)
	postAction("unblock", "", 403)
	if len(blockCommands) != 2 {
		t.Fatal("non-admin issued a block")
	}
}
