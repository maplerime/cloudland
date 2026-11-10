package routes

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/unknwon/i18n"
	"web/src/model"
)

func TestSuspiciousIPTemplates(t *testing.T) {
	tmpl, err := template.ParseFiles("../../templates/suspicious_ips.tmpl", "../../templates/suspicious_ips_edit.tmpl", "../../templates/_blocked_ip_tabs.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tmpl.Parse(`{{define "_head"}}{{end}}{{define "_left"}}{{end}}{{define "_pagination"}}{{end}}{{define "_blocked_ip_tabs"}}{{template "_blocked_ip_tabs.tmpl" .}}{{end}}{{define "_footer"}}{{end}}`)
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
		data := map[string]interface{}{"i18n": i18n.Locale{Lang: tc.lang}, "Tab": "suspicious", "Total": 1, "IP": "", "Status": "", "Entries": []*model.SuspiciousIP{entry}, "Entry": entry, "Link": "/blocked-ips/suspicious/new"}
		for _, name := range []string{"suspicious_ips.tmpl", "suspicious_ips_edit.tmpl"} {
			var out bytes.Buffer
			if err = tmpl.ExecuteTemplate(&out, name, data); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.title) || !strings.Contains(out.String(), "&lt;note&gt;") || strings.Contains(out.String(), "Suspicious_IP_") {
				t.Fatalf("untranslated or unsafe template: %s", out.String())
			}
		}
	}
}
