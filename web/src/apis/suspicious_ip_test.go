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
	router.POST(base+"/block", suspiciousIPAPI.Block)
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
	http.DefaultClient.Transport = suspiciousIPTransport(func(r *http.Request) (*http.Response, error) {
		var command ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		isBlock := strings.Contains(command.Command, "/operation/block_ip.sh '") && strings.HasSuffix(command.Command, " 'block_dst'")
		isUnblock := command.Command == "/opt/cloudland/scripts/kvm/operation/unblock_ip.sh '192.0.2.1'"
		if command.Control != "toall=" || (!isBlock && !isUnblock) {
			t.Fatalf("bad broadcast: %+v", command)
		}
		commands++
		if failRequest {
			return nil, errors.New("SCI unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Status":"accepted"}`))}, nil
	})
	request("POST", "/suspicious/"+id+"/block", "", 202)
	request("POST", "/block", `{"ip":"198.51.100.20"}`, 202)
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
	request("POST", "/block", `{"ip":"203.0.113.2"}`, 502)
	w = request("GET", "/suspicious?status=block_failed", "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 3 {
		t.Fatalf("obsolete status parameter filtered results: %s %v", w.Body.String(), err)
	}
	if !strings.Contains(w.Body.String(), "203.0.113.2") {
		t.Fatal("failed block not retained")
	}
	if commands != 3 {
		t.Fatalf("expected 3 block calls, got %d", commands)
	}
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
	if commands != 5 {
		t.Fatalf("unexpected block/unblock calls: %d", commands)
	}
}
