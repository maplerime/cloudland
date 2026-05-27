/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// StartWorkers starts multiple worker goroutines.
func StartWorkers(ctx context.Context, count int) {
	if count <= 0 {
		count = 1
	}
	logger.Infof("Starting %d callback workers", count)

	for i := 0; i < count; i++ {
		go worker(ctx, i)
	}
}

func worker(ctx context.Context, id int) {
	logger.Infof("Callback worker %d started", id)

	ch := GetEventQueue()
	if ch == nil {
		logger.Errorf("Callback worker %d: event queue is nil", id)
		return
	}

	client := getHTTPClient()

	for {
		select {
		case <-ctx.Done():
			logger.Infof("Callback worker %d stopped (ctx canceled: %v)", id, ctx.Err())
			return

		case event, ok := <-ch:
			if !ok {
				logger.Infof("Callback worker %d stopped (queue closed)", id)
				return
			}
			if event == nil {
				continue
			}

			// Respect dynamic enabled switch.
			if !IsEnabled() {
				// Drop immediately when disabled.
				continue
			}

			// Dynamically read url/timeout/retry_* (hot-reload).
			callbackURL := GetCallbackURL()
			if callbackURL == "" {
				logger.Errorf("Worker %d: callback URL not configured; drop: %s/%s",
					id, event.EventType, event.Resource.ID)
				continue
			}
			apiKey := GetAPIKey()
			if apiKey == "" {
				// Do not send without api_key.
				logger.Errorf("Worker %d: api_key not configured; drop: %s/%s",
					id, event.EventType, event.Resource.ID)
				continue
			}
			reqTimeout := time.Duration(GetTimeout()) * time.Second
			if reqTimeout <= 0 {
				reqTimeout = 30 * time.Second
			}

			retryMax := GetRetryMax()
			if retryMax < 0 {
				retryMax = 0
			}
			retryInterval := time.Duration(GetRetryInterval()) * time.Second
			if retryInterval <= 0 {
				retryInterval = 5 * time.Second
			}

			// Initial send attempt.
			if err := sendEvent(ctx, client, callbackURL, apiKey, reqTimeout, event); err != nil {
				logger.Errorf("Worker %d: send failed: %s/%s err=%v",
					id, event.EventType, event.Resource.ID, err)

				// Retry count is controlled by retry_max.
				if event.RetryCount >= retryMax {
					logger.Warningf("Worker %d: drop after %d retries: %s/%s",
						id, retryMax, event.EventType, event.Resource.ID)
					continue
				}

				// Do not block worker: clone and requeue via AfterFunc.
				next := event.Clone()
				next.RetryCount++

				delay := retryInterval
				// Optional linear backoff: 5s, 10s, 15s...
				delay = delay * time.Duration(next.RetryCount)

				// Cap the delay to avoid too long waits (60s).
				if delay > 60*time.Second {
					delay = 60 * time.Second
				}

				logger.Infof("Worker %d: retry scheduled in %s (%d/%d): %s/%s",
					id, delay, next.RetryCount, retryMax, next.EventType, next.Resource.ID)

				time.AfterFunc(delay, func() {
					// Do not requeue during shutdown or when disabled.
					if ctx.Err() != nil || !IsEnabled() {
						return
					}
					_ = PushEvent(next) // Non-blocking push; drop if it fails.
				})
				continue
			}

			logger.Infof("Worker %d: sent OK: %s/%s", id, event.EventType, event.Resource.ID)
		}
	}
}

func sendEvent(
	parent context.Context,
	client *http.Client,
	url string,
	apiKey string,
	reqTimeout time.Duration,
	event *Event,
) error {
	if client == nil {
		return fmt.Errorf("http client is nil")
	}
	if event == nil {
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	ctx := parent
	if reqTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, reqTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CloudLand-Callback/1.0")
	req.Header.Set("X-PS-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Shared HTTP client (singleton).
var (
	httpClientOnce sync.Once
	httpClient     *http.Client
)

func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		insecure := viper.GetBool("callback.tls_insecure_skip_verify")
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: insecure,
			},
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     90 * time.Second,

			ResponseHeaderTimeout: 15 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
		httpClient = &http.Client{Transport: transport}
	})
	return httpClient
}
