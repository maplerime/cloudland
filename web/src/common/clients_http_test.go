package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHyperExecuteHTTPStatus(t *testing.T) {
	previous := remoteExecPath
	defer func() { remoteExecPath = previous }()
	status := http.StatusServiceUnavailable
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Control != "toall=" {
			t.Errorf("unexpected execute request: %+v %v", request, err)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"Status":"submitted"}`))
	}))
	defer server.Close()
	remoteExecPath = server.URL
	if err := HyperExecute(context.Background(), "toall=", "check_connection.sh"); err == nil {
		t.Fatal("HTTP failure was treated as a successful dispatch")
	}
	status = http.StatusOK
	if err := HyperExecute(context.Background(), "toall=", "check_connection.sh"); err != nil {
		t.Fatal(err)
	}
}
