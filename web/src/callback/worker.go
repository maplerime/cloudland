/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// StartWorkers 启动多个 worker goroutine
func StartWorkers(ctx context.Context, count int) {
	logger.Infof("Starting %d callback workers", count)
	for i := 0; i < count; i++ {
		go worker(ctx, i)
	}
}

// worker 处理队列中的事件
func worker(ctx context.Context, id int) {
	logger.Infof("Callback worker %d started", id)

	// 获取配置
	callbackURL := GetCallbackURL()
	timeout := time.Duration(GetTimeout()) * time.Second
	retryMax := GetRetryMax()
	retryInterval := time.Duration(GetRetryInterval()) * time.Second

	// 验证 URL 配置
	if callbackURL == "" {
		logger.Errorf("Callback worker %d: callback URL is not configured", id)
		return
	}

	// 创建 HTTP 客户端
	client := &http.Client{
		Timeout: timeout,
	}

	// 事件处理循环
	for {
		select {
		case <-ctx.Done():
			// 收到退出信号
			logger.Infof("Callback worker %d stopped", id)
			return

		case event := <-GetEventQueue():
			// 处理事件
			if event == nil {
				continue
			}

			// 发送事件
			if err := sendEvent(client, callbackURL, event); err != nil {
				logger.Errorf("Worker %d: Failed to send event: %s/%s, error: %v",
					id, event.ResourceType, event.ResourceUUID, err)

				// 重试逻辑
				if event.RetryCount < retryMax {
					event.RetryCount++
					logger.Infof("Worker %d: Retrying event (%d/%d): %s/%s",
						id, event.RetryCount, retryMax, event.ResourceType, event.ResourceUUID)
					time.Sleep(retryInterval)
					PushEvent(event) // 重新入队
				} else {
					logger.Errorf("Worker %d: Event dropped after %d retries: %s/%s",
						id, retryMax, event.ResourceType, event.ResourceUUID)
				}
			} else {
				logger.Infof("Worker %d: Event sent successfully: %s/%s -> %s",
					id, event.ResourceType, event.ResourceUUID, event.Status)
			}
		}
	}
}

// sendEvent 发送事件到 callback URL
func sendEvent(client *http.Client, url string, event *ResourceChangeEvent) error {
	// 序列化事件为 JSON
	jsonData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CloudLand-Callback/1.0")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback returned status %d", resp.StatusCode)
	}

	return nil
}
