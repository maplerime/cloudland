/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

Purpose: Simple HTTP server for testing callback functionality
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// ResourceChangeEvent 接收的资源变化事件结构
type ResourceChangeEvent struct {
	ResourceType   string                 `json:"resource_type"`
	ResourceUUID   string                 `json:"resource_uuid"`
	ResourceID     int64                  `json:"resource_id"`
	Status         string                 `json:"status"`
	PreviousStatus string                 `json:"previous_status,omitempty"`
	Timestamp      time.Time              `json:"timestamp"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

var (
	// 统计信息
	totalReceived uint64
	totalSuccess  uint64
	totalFailed   uint64
)

// handleCallback 处理回调请求
func handleCallback(w http.ResponseWriter, r *http.Request) {
	// 只接受 POST 请求
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		atomic.AddUint64(&totalFailed, 1)
		return
	}

	// 读取请求体
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body: %v\n", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		atomic.AddUint64(&totalFailed, 1)
		return
	}
	defer r.Body.Close()

	// 解析 JSON
	var event ResourceChangeEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("ERROR: Failed to parse JSON: %v\n", err)
		log.Printf("       Raw body: %s\n", string(body))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		atomic.AddUint64(&totalFailed, 1)
		return
	}

	// 增加接收计数
	received := atomic.AddUint64(&totalReceived, 1)

	// 打印事件信息
	printEvent(received, &event)

	// 成功响应
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"status":  "ok",
		"message": "Event received successfully",
		"count":   received,
	}
	json.NewEncoder(w).Encode(response)

	atomic.AddUint64(&totalSuccess, 1)
}

// printEvent 打印事件详细信息
func printEvent(count uint64, event *ResourceChangeEvent) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("Event #%d received at %s\n", count, time.Now().Format("2006-01-02 15:04:05.000"))
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("  Resource Type : %s\n", event.ResourceType)
	fmt.Printf("  Resource UUID : %s\n", event.ResourceUUID)
	fmt.Printf("  Resource ID   : %d\n", event.ResourceID)
	fmt.Printf("  Status        : %s", event.Status)
	if event.PreviousStatus != "" {
		fmt.Printf(" (previous: %s)", event.PreviousStatus)
	}
	fmt.Println()
	fmt.Printf("  Timestamp     : %s\n", event.Timestamp.Format("2006-01-02 15:04:05.000"))

	if len(event.Metadata) > 0 {
		fmt.Println("  Metadata      :")
		for key, value := range event.Metadata {
			fmt.Printf("    - %-12s: %v\n", key, value)
		}
	}
	fmt.Println(strings.Repeat("=", 80))
}

// handleStats 处理统计请求
func handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"total_received": atomic.LoadUint64(&totalReceived),
		"total_success":  atomic.LoadUint64(&totalSuccess),
		"total_failed":   atomic.LoadUint64(&totalFailed),
		"uptime":         time.Since(startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// handleHealth 健康检查
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]string{
		"status": "healthy",
		"time":   time.Now().Format("2006-01-02 15:04:05"),
	}
	json.NewEncoder(w).Encode(response)
}

var startTime time.Time

func main() {
	// 命令行参数
	port := flag.Int("port", 8080, "HTTP server port")
	host := flag.String("host", "0.0.0.0", "HTTP server host")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	startTime = time.Now()

	// 设置日志
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// 注册路由
	http.HandleFunc("/api/v1/resource-changes", handleCallback)
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/health", handleHealth)

	// 打印启动信息
	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("CloudLand Callback Test Server")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Server starting at: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("Listening on:       http://%s\n", addr)
	fmt.Println()
	fmt.Println("Available endpoints:")
	fmt.Printf("  POST   /api/v1/resource-changes  - Receive callback events\n")
	fmt.Printf("  GET    /stats                     - View statistics\n")
	fmt.Printf("  GET    /health                    - Health check\n")
	fmt.Println()
	fmt.Printf("Verbose mode:       %v\n", *verbose)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("\nWaiting for events...\n")

	// 启动定时统计输出
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if *verbose {
				log.Printf("Stats: Received=%d, Success=%d, Failed=%d\n",
					atomic.LoadUint64(&totalReceived),
					atomic.LoadUint64(&totalSuccess),
					atomic.LoadUint64(&totalFailed))
			}
		}
	}()

	// 启动 HTTP 服务器
	log.Fatal(http.ListenAndServe(addr, nil))
}
