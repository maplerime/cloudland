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

// Resource is the received resource object.
type Resource struct {
	Type   string            `json:"type"`           // Resource type
	ID     string            `json:"id"`             // Resource UUID
	Region string            `json:"region"`         // Resource region
	Name   string            `json:"name,omitempty"` // Resource name
	Tags   map[string]string `json:"tags,omitempty"` // Resource tags
}

// Cloudland event structure sent to callback URL.
type Event struct {
	EventType  string                 `json:"event_type"`  // Event type (e.g., "instance.created")
	Source     string                 `json:"source"`      // Source system (e.g., "cloudland", "monitoring")
	OccurredAt time.Time              `json:"occurred_at"` // When the event occurred
	TenantID   string                 `json:"tenant_id"`   // The tenantID in Cloudland
	Resource   Resource               `json:"resource"`
	Data       map[string]interface{} `json:"data"`               // Event payload as JSON
	Metadata   map[string]interface{} `json:"metadata,omitempty"` // Additional metadata
	RetryCount int                    `json:"-"`                  // Internal retry count (not serialized)
}

var (
	// Statistics.
	totalReceived uint64
	totalSuccess  uint64
	totalFailed   uint64
)

// handleCallback handles callback request.
func handleCallback(w http.ResponseWriter, r *http.Request) {
	// POST only.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		atomic.AddUint64(&totalFailed, 1)
		return
	}

	// Read body.
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body: %v\n", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		atomic.AddUint64(&totalFailed, 1)
		return
	}
	defer r.Body.Close()

	// Parse JSON.
	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("ERROR: Failed to parse JSON: %v\n", err)
		log.Printf("       Raw body: %s\n", string(body))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		atomic.AddUint64(&totalFailed, 1)
		return
	}

	// Increase received count.
	received := atomic.AddUint64(&totalReceived, 1)

	// Print event.
	printEvent(received, &event)

	// Success response.
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

// printEvent prints event details.
func printEvent(count uint64, event *Event) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("Event #%d received at %s\n", count, time.Now().Format("2006-01-02 15:04:05.000"))
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("  Event Type      : %s\n", event.EventType)
	fmt.Printf("  Source          : %s\n", event.Source)
	fmt.Printf("  Resource Type   : %s\n", event.Resource.Type)
	fmt.Printf("  Resource UUID   : %s\n", event.Resource.ID)
	fmt.Printf("  Resource Region : %s\n", event.Resource.Region)
	fmt.Printf("  Tenant ID       : %s\n", event.TenantID)
	fmt.Println()
	fmt.Printf("  OccurredAt     : %s\n", event.OccurredAt.Format("2006-01-02 15:04:05.000"))

	if len(event.Data) > 0 {
		fmt.Println("  Data          :")
		for key, value := range event.Data {
			fmt.Printf("    - %-12s: %v\n", key, value)
		}
	}
	fmt.Println(strings.Repeat("=", 80))
}

// handleStats handles stats request.
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

// handleHealth handles health check.
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
	// CLI flags.
	port := flag.Int("port", 8080, "HTTP server port")
	host := flag.String("host", "0.0.0.0", "HTTP server host")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	startTime = time.Now()

	// Setup logging.
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Register routes.
	http.HandleFunc("/api/v1/resource-changes", handleCallback)
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/health", handleHealth)

	// Startup information.
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
	fmt.Printf("\nWaiting for events...\n")

	// Periodic stats logging.
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

	// Start HTTP server.
	log.Fatal(http.ListenAndServe(addr, nil))
}
