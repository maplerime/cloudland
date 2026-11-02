package apis

import (
	"context"
	"encoding/json"
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
}
