/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// type JSONTime time.Time

type BaseReference struct {
	ID   string `json:"id" binding:"omitempty,uuid"`
	Name string `json:"name" binding:"omitempty,min=2,max=36"`
}

type ResourceReference struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Owner     string `json:"owner,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type BaseID struct {
	ID string `json:"id" binding:"required,uuid"`
}

type APIError struct {
	ErrorCode    int    `json:"error_code"`
	ErrorCodeStr string `json:"error_code_str,omitempty"`
	ErrorMessage string `json:"error_message"`
}

func ErrorResponse(c *gin.Context, code int, errorMsg string, err error) {
	logger.Errorf("%s, %v\n", errorMsg, err)
	if err != nil {
		var clErr *CLError
		if errors.As(err, &clErr) {
			c.JSON(code, &APIError{
				ErrorCode:    int(clErr.Code),
				ErrorCodeStr: clErr.Code.String(),
				ErrorMessage: clErr.Error(),
			})
			return
		}
		errorMsg = errorMsg + ": " + err.Error()
	}
	c.JSON(code, &APIError{
		ErrorCode:    code,
		ErrorMessage: errorMsg,
	})
}

func (e *APIError) Error() string {
	return fmt.Sprintf("CLError: code=%d, message=%s", e.ErrorCode, e.ErrorMessage)
}

/*
type Marshaler interface {
    MarshalJSON() ([]byte, error)
}

func (t JSONTime)MarshalJSON() ([]byte, error) {
    //do your serializing here
    stamp := fmt.Sprintf("\"%s\"", time.Time(t).Format("Mon Jan _2"))
    return []byte(stamp), nil
}
*/

var (
	pushServiceClientMu sync.Mutex
	pushServiceClients  = map[bool]*http.Client{}
)

func getPushServiceHTTPClient(insecure bool) *http.Client {
	pushServiceClientMu.Lock()
	defer pushServiceClientMu.Unlock()
	if client, ok := pushServiceClients[insecure]; ok {
		return client
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: insecure,
			},
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
		},
	}
	pushServiceClients[insecure] = client
	return client
}

// SendVMInspectionNotification posts a VM inspection threat event to the
// configured pushservice ingestion endpoint.
func SendVMInspectionNotification(ctx context.Context, event interface{}) error {
	if !viper.GetBool("callback.enabled") || event == nil {
		return nil
	}
	url := strings.TrimSpace(viper.GetString("callback.url"))
	if url == "" {
		return fmt.Errorf("callback url not configured")
	}
	apiKey := strings.TrimSpace(viper.GetString("callback.api_key"))
	if apiKey == "" {
		return fmt.Errorf("callback api key not configured")
	}
	timeout := time.Duration(viper.GetInt("callback.timeout")) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CloudLand-Callback/1.0")
	req.Header.Set("X-PS-API-Key", apiKey)
	resp, err := getPushServiceHTTPClient(viper.GetBool("callback.tls_insecure_skip_verify")).Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
