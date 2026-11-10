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

	. "web/src/common"
	"web/src/model"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

func TestCheckConnectionAPI(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.AutoMigrate(&model.CheckConnection{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, owner := range []int64{8, 9} {
		if err = db.Create(&model.CheckConnection{Owner: owner, IP: "192.0.2.10", Status: "submitted"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := (&MemberShip{UserID: 7, OrgID: 8, Role: model.Writer}).SetContext(SetContextDB(context.Background(), db))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(ctx) })
	router.GET("/check_connection", checkConnectionAPI.List)
	router.POST("/check_connection", checkConnectionAPI.Create)
	router.DELETE("/check_connection/:uuid", checkConnectionAPI.Delete)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/check_connection", `{"ips":[]}`, 400},
		{http.MethodPost, "/check_connection", `{"ips":["192.0.2.1","invalid"]}`, 400},
		{http.MethodGet, "/check_connection?limit=-1", "", 400},
		{http.MethodGet, "/check_connection", "", 200},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var response struct {
				Total   int                       `json:"total"`
				Entries []CheckConnectionResponse `json:"entries"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Total != 1 || len(response.Entries) != 1 {
				t.Fatalf("invalid scoped response: %s %v", w.Body.String(), err)
			}
		}
	}
	ctx = (&MemberShip{UserID: 7, OrgID: 8, Role: model.Reader}).SetContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/check_connection", strings.NewReader(`{"ips":["192.0.2.1"]}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader submission returned %d", w.Code)
	}
	var owned, other model.CheckConnection
	db.Where("owner = ?", 8).Take(&owned)
	db.Where("owner = ?", 9).Take(&other)
	deleteRecord := func(id string, code int) {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/check_connection/"+id, nil))
		if w.Code != code {
			t.Fatalf("delete returned %d: %s", w.Code, w.Body.String())
		}
	}
	deleteRecord(owned.UUID, 403)
	previous := http.DefaultClient.Transport
	defer func() { http.DefaultClient.Transport = previous }()
	commands := 0
	fail := false
	http.DefaultClient.Transport = checkConnectionTransport(func(r *http.Request) (*http.Response, error) {
		var command ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		if command.Control != "toall=" || command.Command != "/opt/cloudland/scripts/kvm/check_connection.sh '192.0.2.10' 'delete'" {
			t.Fatalf("wrong cleanup: %+v", command)
		}
		commands++
		if fail {
			return nil, errors.New("SCI unavailable")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Status":"accepted"}`))}, nil
	})
	ctx = (&MemberShip{OrgID: 8, Role: model.Writer}).SetContext(ctx)
	deleteRecord(other.UUID, 400)
	deleteRecord(owned.UUID, 204)
	if commands != 0 {
		t.Fatal("shared IP was removed while another check still uses it")
	}
	ctx = (&MemberShip{OrgID: 9, Role: model.Writer}).SetContext(ctx)
	fail = true
	deleteRecord(other.UUID, 400)
	var retained model.CheckConnection
	if err := db.Where("uuid = ?", other.UUID).Take(&retained).Error; err != nil {
		t.Fatal("failed cleanup removed record")
	}
	fail = false
	deleteRecord(other.UUID, 204)
	if commands != 2 {
		t.Fatalf("cleanup requests: %d", commands)
	}
}

type checkConnectionTransport func(*http.Request) (*http.Response, error)

func (f checkConnectionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
