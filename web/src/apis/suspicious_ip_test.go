package apis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	. "web/src/common"
	"web/src/model"
)

type suspiciousIPTransport func(*http.Request) (*http.Response, error)

func (f suspiciousIPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSuspiciousIPAPI(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.SuspiciousIP{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Admin}).SetContext(SetContextDB(context.Background(), db))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(ctx) })
	base := "/api/v1/check_connection"
	router.GET(base+"/suspicious", suspiciousIPAPI.List)
	router.POST(base+"/suspicious", suspiciousIPAPI.Create)
	router.GET(base+"/suspicious/:uuid", suspiciousIPAPI.Get)
	router.PATCH(base+"/suspicious/:uuid", suspiciousIPAPI.Patch)
	router.DELETE(base+"/suspicious/:uuid", suspiciousIPAPI.Delete)
	router.POST(base+"/suspicious/:uuid/block", suspiciousIPAPI.BlockRecord)
	router.POST(base+"/suspicious/:uuid/unblock", suspiciousIPAPI.UnblockRecord)
	router.POST(base+"/block", suspiciousIPAPI.Block)
	router.POST(base+"/unblock", suspiciousIPAPI.Unblock)
	request := func(method, path, body string, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	w := request("POST", "/suspicious", `{"ip":"192.0.2.1","note":"first"}`, 201)
	var entry SuspiciousIPResponse
	if err = json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	id := entry.UUID
	w = request("GET", "/suspicious/"+id, "", 200)
	if strings.Contains(w.Body.String(), `"status":`) {
		t.Fatal("detail response includes status")
	}
	w = request("PATCH", "/suspicious/"+id, `{"note":"updated"}`, 200)
	if !strings.Contains(w.Body.String(), `"note":"updated"`) {
		t.Fatal(w.Body.String())
	}
	request("POST", "/suspicious", `{"ip":"not-an-ip"}`, 400)
	request("GET", "/suspicious?limit=-1", "", 400)
	request("GET", "/suspicious?offset=-1", "", 400)

	// Intercept the actual SCI HTTP request. No network or block scripts run.
	previous := http.DefaultClient.Transport
	defer func() { http.DefaultClient.Transport = previous }()
	commands := 0
	failRequest := false
	expectedCommands := []string{
		"/opt/cloudland/scripts/kvm/operation/block_ip.sh '192.0.2.1' 'block_dst' '900'",
		"/opt/cloudland/scripts/kvm/operation/block_ip.sh '198.51.100.20' 'block_dst'",
		"/opt/cloudland/scripts/kvm/operation/block_ip.sh '192.0.2.1' 'block_dst'",
		"/opt/cloudland/scripts/kvm/operation/block_ip.sh '203.0.113.2' 'block_dst' '0'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '192.0.2.1'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '192.0.2.1'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '198.51.100.20'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '203.0.113.55'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '192.0.2.1'",
		"/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '192.0.2.1'",
	}
	http.DefaultClient.Transport = suspiciousIPTransport(func(r *http.Request) (*http.Response, error) {
		var command ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		if commands >= len(expectedCommands) || command.Control != "toall=" || command.Command != expectedCommands[commands] {
			t.Fatalf("bad broadcast: %+v", command)
		}
		commands++
		if failRequest {
			return nil, errors.New("SCI unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Status":"accepted"}`))}, nil
	})
	for _, value := range []string{"-1", "2147484", "0.5", `"invalid"`} {
		request("POST", "/suspicious/"+id+"/block", `{"timeout":`+value+`}`, 400)
		request("POST", "/block", `{"ip":"192.0.2.1","timeout":`+value+`}`, 400)
	}
	request("POST", "/suspicious/"+id+"/block", `{"timeout":900}`, 202)
	request("POST", "/block", `{"ip":"198.51.100.20"}`, 202)
	request("POST", "/suspicious/"+id+"/block", "", 202)
	w = request("GET", "/suspicious", "", 200)
	if strings.Contains(w.Body.String(), `"status":`) {
		t.Fatal("list response includes status")
	}
	var list struct {
		Total   int                    `json:"total"`
		Entries []SuspiciousIPResponse `json:"entries"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 2 {
		t.Fatalf("API block missing from list: %s %v", w.Body.String(), err)
	}
	for _, e := range list.Entries {
		if e.BlockedAt == nil {
			t.Fatalf("missing block submission time: %+v", e)
		}
	}
	failRequest = true
	request("POST", "/block", `{"ip":"203.0.113.2","timeout":0}`, 502)
	w = request("GET", "/suspicious?status=block_failed", "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 3 {
		t.Fatalf("obsolete status parameter filtered results: %s %v", w.Body.String(), err)
	}
	if !strings.Contains(w.Body.String(), "203.0.113.2") {
		t.Fatal("failed block not retained")
	}
	if commands != 4 {
		t.Fatalf("expected 4 block calls, got %d", commands)
	}
	request("POST", "/suspicious/"+id+"/unblock", "", 400)
	failRequest = false
	request("POST", "/suspicious/"+id+"/unblock", "", 202)
	w = request("GET", "/suspicious/"+id, "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &entry); err != nil || entry.BlockedAt != nil || entry.Note != "updated" {
		t.Fatalf("unblock removed record or left block metadata: %s %v", w.Body.String(), err)
	}
	request("POST", "/unblock", `{"ip":"198.51.100.20"}`, 202)
	request("POST", "/unblock", `{"ip":"203.0.113.55"}`, 202)
	w = request("GET", "/suspicious?ip=203.0.113.55", "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 0 {
		t.Fatal("unblocking unknown IP created a suspicious record")
	}
	request("POST", "/unblock", `{"ip":"invalid"}`, 400)
	request("POST", "/suspicious/missing/unblock", "", 404)
	failRequest = true
	request("DELETE", "/suspicious/"+id, "", 400)
	request("GET", "/suspicious/"+id, "", 200)
	failRequest = false
	request("DELETE", "/suspicious/"+id, "", 204)
	request("GET", "/suspicious/"+id, "", 404)
	ctx = (&MemberShip{Role: model.Writer}).SetContext(ctx)
	request("GET", "/suspicious", "", 403)
	request("POST", "/block", `{"ip":"192.0.2.4"}`, 403)
	request("POST", "/suspicious", `{"ip":"192.0.2.4"}`, 403)
	request("PATCH", "/suspicious/missing", `{"note":"bad"}`, 403)
	request("DELETE", "/suspicious/missing", "", 403)
	request("POST", "/unblock", `{"ip":"192.0.2.4"}`, 403)
	request("POST", "/suspicious/missing/unblock", "", 403)
	if commands != 10 {
		t.Fatalf("unexpected block/unblock calls: %d", commands)
	}
}
