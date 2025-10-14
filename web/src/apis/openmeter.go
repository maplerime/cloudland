package apis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"web/src/routes"

	"sync"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// OpenMeterAPI handles OpenMeter related operations
type OpenMeterAPI struct{}

var openMeterAPI = &OpenMeterAPI{}

// OpenMeterQueryRequest represents a query request to OpenMeter
type OpenMeterQueryRequest struct {
	InstanceID string `json:"instance_id"`
	Subject    string `json:"subject"`
	User       string `json:"user"`
	Password   string `json:"password"`
	Database   string `json:"database"`
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Limit      int    `json:"limit"`
}

// OpenMeterEvent represents a single metric event from OpenMeter
type OpenMeterEvent struct {
	ID         string                 `json:"id"`
	Source     string                 `json:"source"`
	Type       string                 `json:"type"`
	Subject    string                 `json:"subject"`
	Time       string                 `json:"time"`
	Data       map[string]interface{} `json:"data"`
	IngestedAt string                 `json:"ingested_at"`
	StoredAt   string                 `json:"stored_at"`
}

// MetricProcessingResult represents the processed metric result
type MetricProcessingResult struct {
	Subject       string                 `json:"subject"`
	InstanceID    string                 `json:"instance_id"`
	ProcessedData interface{}            `json:"processed_data"`
	Summary       map[string]interface{} `json:"summary"`
	RawEvents     []OpenMeterEvent       `json:"raw_events,omitempty"`
}

// VMStateSegment represents a VM state duration segment
type VMStateSegment struct {
	State     string  `json:"state"`
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	Duration  float64 `json:"duration_minutes"`
}

// ResourceUsageSegment represents a resource usage duration segment
type ResourceUsageSegment struct {
	Value     interface{} `json:"value"`
	StartTime string      `json:"start_time"`
	EndTime   string      `json:"end_time"`
	Duration  float64     `json:"duration_minutes"`
	Unit      string      `json:"unit"`
}

// TrafficCalculationResult represents traffic calculation result
type TrafficCalculationResult struct {
	TotalBytes     int64                   `json:"total_bytes"`
	Segments       []TrafficSegment        `json:"segments"`
	Resets         []TrafficReset          `json:"resets"`
	HostMigrations []HostMigrationDetected `json:"host_migrations"`
	DataQuality    map[string]interface{}  `json:"data_quality"`
}

// TrafficSegment represents a traffic segment between resets
type TrafficSegment struct {
	StartTime   int64   `json:"start_time"`
	EndTime     int64   `json:"end_time"`
	StartValue  int64   `json:"start_value"`
	EndValue    int64   `json:"end_value"`
	IncrementBy int64   `json:"increment_bytes"`
	Duration    float64 `json:"duration_minutes"`
}

// TrafficReset represents a detected counter reset
type TrafficReset struct {
	Timestamp               int64  `json:"timestamp"`
	BeforeValue             int64  `json:"before_value"`
	AfterValue              int64  `json:"after_value"`
	ResetReason             string `json:"reset_reason"`
	DurationAffectedMinutes int    `json:"duration_affected_minutes"`
}

// HostMigrationDetected represents a detected host migration
type HostMigrationDetected struct {
	Timestamp       int64  `json:"timestamp"`
	FromHost        string `json:"from_host"`
	ToHost          string `json:"to_host"`
	MigrationType   string `json:"migration_type"`
	DowntimeSeconds int    `json:"downtime_seconds"`
	DataLossBytes   int64  `json:"data_loss_bytes"`
	Reason          string `json:"reason"`
}

// DataLossInfo represents data loss information
type DataLossInfo struct {
	DataLossBytes int64  `json:"data_loss_bytes"`
	Reason        string `json:"reason"`
}

// PrometheusConfig represents the Prometheus configuration structure
type PrometheusConfig struct {
	ScrapeConfigs []ScrapeConfig `yaml:"scrape_configs"`
}

// ScrapeConfig represents a scrape configuration
type ScrapeConfig struct {
	JobName       string         `yaml:"job_name"`
	StaticConfigs []StaticConfig `yaml:"static_configs"`
}

// StaticConfig represents static configuration targets
type StaticConfig struct {
	Targets []string `yaml:"targets"`
}

// PrometheusTargetFilter holds the valid targets from Prometheus configuration
type PrometheusTargetFilter struct {
	NodeExporterTargets    []string
	LibvirtExporterTargets []string
	lastUpdated            time.Time
	cacheDuration          time.Duration
}

// AggregatedDataPoint represents aggregated traffic data from multiple network interfaces at a specific timestamp
type AggregatedDataPoint struct {
	Timestamp    string
	TotalValue   int64
	Host         string
	EventCount   int
	Devices      []string
	OriginalTime string
}

// TrafficAggregationResult represents the result from traffic aggregation query
type TrafficAggregationResult struct {
	TotalRecords     int64  `json:"total_records"`
	MinValue         int64  `json:"min_value"`
	MaxValue         int64  `json:"max_value"`
	TotalIncrement   int64  `json:"total_increment"`
	ResetCount       int64  `json:"reset_count"`
	UniqueTimestamps int64  `json:"unique_timestamps"`
	Subject          string `json:"subject"`
	InstanceID       string `json:"instance_id"`
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
}

var (
	targetFilterCache *PrometheusTargetFilter
	targetFilterMutex sync.Mutex
)

func init() {
	targetFilterCache = &PrometheusTargetFilter{
		cacheDuration: 5 * time.Minute, // Cache for 5 minutes
	}
}

// getPrometheusConfig reads and parses the Prometheus configuration file
func (o *OpenMeterAPI) getPrometheusConfig() (*PrometheusConfig, error) {
	// Use the fixed path for Prometheus configuration
	configPath := "/etc/prometheus/prometheus.yml"

	// Use the ReadFile function from alarm.go to handle remote Prometheus servers
	data, err := routes.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read prometheus config file: %v", err)
	}
	var config PrometheusConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse prometheus config: %v", err)
	}
	log.Printf("Prometheus config loaded: %v", config)
	return &config, nil
}

// getTargetsFromPrometheusConfig extracts targets from Prometheus configuration
func (o *OpenMeterAPI) getTargetsFromPrometheusConfig(jobName string) ([]string, error) {
	config, err := o.getPrometheusConfig()
	if err != nil {
		return nil, err
	}

	var targets []string
	for _, scrapeConfig := range config.ScrapeConfigs {
		if scrapeConfig.JobName == jobName {
			for _, staticConfig := range scrapeConfig.StaticConfigs {
				targets = append(targets, staticConfig.Targets...)
			}
		}
	}

	return targets, nil
}

// getPrometheusTargetFilter returns cached or fresh Prometheus target filter
func (o *OpenMeterAPI) getPrometheusTargetFilter() (*PrometheusTargetFilter, error) {
	targetFilterMutex.Lock()
	defer targetFilterMutex.Unlock()

	now := time.Now()
	if targetFilterCache.lastUpdated.Add(targetFilterCache.cacheDuration).After(now) {
		// Return cached data if still valid
		return targetFilterCache, nil
	}

	// Refresh target filter from Prometheus configuration
	// Try specific job names for node exporter
	var nodeTargets []string
	nodeJobNames := []string{"prometheus_node_exporter"}
	for _, jobName := range nodeJobNames {
		if targets, err := o.getTargetsFromPrometheusConfig(jobName); err == nil && len(targets) > 0 {
			nodeTargets = targets
			log.Printf("Found node exporter targets under job '%s': %v", jobName, targets)
			break
		}
	}
	if len(nodeTargets) == 0 {
		log.Printf("No node exporter targets found under any job name")
	}

	// Try specific job names for libvirt exporter
	var libvirtTargets []string
	libvirtJobNames := []string{"prometheus_libvirt_exporter"}
	for _, jobName := range libvirtJobNames {
		if targets, err := o.getTargetsFromPrometheusConfig(jobName); err == nil && len(targets) > 0 {
			libvirtTargets = targets
			log.Printf("Found libvirt exporter targets under job '%s': %v", jobName, targets)
			break
		}
	}
	if len(libvirtTargets) == 0 {
		log.Printf("No libvirt exporter targets found under any job name")
	}

	targetFilterCache.NodeExporterTargets = nodeTargets
	targetFilterCache.LibvirtExporterTargets = libvirtTargets
	targetFilterCache.lastUpdated = now

	log.Printf("Updated Prometheus target filter - Node targets: %d, Libvirt targets: %d",
		len(nodeTargets), len(libvirtTargets))

	return targetFilterCache, nil
}

// isValidTarget checks if a target (host/IP) is valid according to Prometheus configuration
func (o *OpenMeterAPI) isValidTarget(target string, targetFilter *PrometheusTargetFilter) bool {
	if target == "" {
		return false
	}

	// Extract IP/hostname from target (remove port if present)
	hostPart := target
	if colonIndex := strings.LastIndex(target, ":"); colonIndex > 0 {
		hostPart = target[:colonIndex]
	}

	// Check against node exporter targets
	for _, nodeTarget := range targetFilter.NodeExporterTargets {
		nodeHostPart := nodeTarget
		if colonIndex := strings.LastIndex(nodeTarget, ":"); colonIndex > 0 {
			nodeHostPart = nodeTarget[:colonIndex]
		}
		if hostPart == nodeHostPart {
			return true
		}
	}

	// Check against libvirt exporter targets
	for _, libvirtTarget := range targetFilter.LibvirtExporterTargets {
		libvirtHostPart := libvirtTarget
		if colonIndex := strings.LastIndex(libvirtTarget, ":"); colonIndex > 0 {
			libvirtHostPart = libvirtTarget[:colonIndex]
		}
		if hostPart == libvirtHostPart {
			return true
		}
	}

	return false
}

// filterEventsByPrometheusTargets filters events based on Prometheus target configuration
// Returns filtered events and a flag indicating if any events were filtered due to target mismatch
func (o *OpenMeterAPI) filterEventsByPrometheusTargets(events []OpenMeterEvent, targetFilter *PrometheusTargetFilter) ([]OpenMeterEvent, bool) {
	if len(targetFilter.NodeExporterTargets) == 0 && len(targetFilter.LibvirtExporterTargets) == 0 {
		log.Printf("No Prometheus targets configured, returning all events")
		return events, false
	}

	var filteredEvents []OpenMeterEvent
	filteredCount := 0
	hasTargetMismatch := false

	for _, event := range events {
		// Extract instance/node information from event data
		if event.Data != nil {
			// Try to extract instance or node identifier from labels
			if labels, ok := event.Data["labels"].(map[string]interface{}); ok {
				// Check for instance field (common in libvirt metrics)
				if instance, ok := labels["instance"].(string); ok {
					if o.isValidTarget(instance, targetFilter) {
						filteredEvents = append(filteredEvents, event)
						continue
					}
					filteredCount++
					hasTargetMismatch = true
					continue
				}

				// Check for node field (common in node exporter metrics)
				if node, ok := labels["node"].(string); ok {
					if o.isValidTarget(node, targetFilter) {
						filteredEvents = append(filteredEvents, event)
						continue
					}
					filteredCount++
					hasTargetMismatch = true
					continue
				}

				// Check for hostname field
				if hostname, ok := labels["hostname"].(string); ok {
					if o.isValidTarget(hostname, targetFilter) {
						filteredEvents = append(filteredEvents, event)
						continue
					}
					filteredCount++
					hasTargetMismatch = true
					continue
				}

				// Check for job field to determine if this is a node-level metric
				if job, ok := labels["job"].(string); ok {
					// For node-level metrics, check if any target matches
					if strings.Contains(job, "node") {
						// This is likely a node-level metric, be more permissive
						filteredEvents = append(filteredEvents, event)
						continue
					}
				}
			}

			// If no target field found, include the event (might be instance-level data)
			filteredEvents = append(filteredEvents, event)
		} else {
			// If no data field, include the event
			filteredEvents = append(filteredEvents, event)
		}
	}

	log.Printf("Filtered events: %d kept, %d removed based on Prometheus targets",
		len(filteredEvents), filteredCount)

	return filteredEvents, hasTargetMismatch
}

// QueryOpenMeterMetrics queries metrics from OpenMeter with comprehensive processing
func (o *OpenMeterAPI) QueryOpenMeterMetrics(c *gin.Context) {
	// Extract and validate required parameters
	req := OpenMeterQueryRequest{
		InstanceID: c.Query("instance_id"),
		Subject:    c.DefaultQuery("subject", "vm_instance_map"),
		User:       c.DefaultQuery("user", "default"),
		Password:   c.DefaultQuery("password", "default"),
		Database:   c.DefaultQuery("database", "openmeter"),
		Limit:      100,
	}

	log.Printf("QueryOpenMeterMetrics - Request: InstanceID=%s, Subject=%s\n",
		req.InstanceID, req.Subject)

	// Validate instance_id is required
	if req.InstanceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "instance_id parameter is required",
		})
		return
	}

	// Parse limit parameter
	if limitStr := c.Query("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			req.Limit = limit
		}
	}

	// Handle time range - only apply time filter if both start and end are provided
	if startStr := c.Query("start"); startStr != "" {
		if start, err := strconv.ParseInt(startStr, 10, 64); err == nil {
			req.Start = start
		} else {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid start timestamp format",
			})
			return
		}
	}

	if endStr := c.Query("end"); endStr != "" {
		if end, err := strconv.ParseInt(endStr, 10, 64); err == nil {
			req.End = end
		} else {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid end timestamp format",
			})
			return
		}
	}

	// Validate time range cannot exceed one month (only if both start and end are provided)
	if req.Start > 0 && req.End > 0 {
		oneMonth := int64(30 * 24 * 60 * 60) // 30 days
		if req.End-req.Start > oneMonth {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "time range cannot exceed one month",
			})
			return
		}
	}

	// Get configuration
	meteringHost := viper.GetString("metering.host")
	meteringPort := viper.GetString("metering.port")

	if meteringHost == "" || meteringPort == "" {
		log.Printf("OpenMeter configuration not found, using default values")
		meteringHost = "104.192.86.49"
		meteringPort = "8123"
	}

	log.Printf(" ClickHouse connection - Host=%s, Port=%s\n", meteringHost, meteringPort)

	// Build ClickHouse query URL
	clickHouseURL := fmt.Sprintf("http://%s:%s/", meteringHost, meteringPort)

	// Build SQL query
	sqlQuery := o.buildSQLQuery(req)

	// Prepare query parameters
	params := url.Values{}
	params.Add("user", req.User)
	params.Add("password", req.Password)
	params.Add("database", req.Database)

	// Make HTTP request to ClickHouse
	fullURL := fmt.Sprintf("%s?%s", clickHouseURL, params.Encode())

	// Debug logging
	log.Printf("OpenMeter Debug - SQL Query: %s", sqlQuery)
	log.Printf("OpenMeter Debug - Full URL: %s", fullURL)

	resp, err := http.Post(fullURL, "text/plain", strings.NewReader(sqlQuery))
	if err != nil {
		log.Printf("Failed to query ClickHouse: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to query metrics database",
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to read metrics response",
		})
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("ClickHouse query failed with status %d: %s", resp.StatusCode, string(body))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "metrics query failed",
			"details": string(body),
		})
		return
	}

	// Debug logging
	log.Printf("ClickHouse response - status: %d, body length: %d", resp.StatusCode, len(body))

	// Parse raw events
	events, err := o.parseJSONEachRowResponse(body)
	if err != nil {
		log.Printf("Failed to parse ClickHouse response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to parse metrics response",
		})
		return
	}

	// Debug logging
	log.Printf("Parsed events count: %d\n", len(events))

	if len(events) == 0 {
		log.Printf("No data found for instance_id: %s, subject: %s", req.InstanceID, req.Subject)
		c.JSON(http.StatusNotFound, gin.H{
			"status":  "error",
			"code":    "NO_DATA_FOUND",
			"message": "No metric data found for the specified criteria",
			"details": fmt.Sprintf("No data found for instance_id '%s' and subject '%s'. Please verify the instance_id exists and has data for the specified time range.", req.InstanceID, req.Subject),
			"query_params": gin.H{
				"instance_id": req.InstanceID,
				"subject":     req.Subject,
				"start_time":  req.Start,
				"end_time":    req.End,
			},
		})
		return
	}
	// Apply Prometheus target filtering if enabled
	filteredEvents := events
	if c.Query("disable_prometheus_filter") != "true" {
		targetFilter, err := o.getPrometheusTargetFilter()
		if err != nil {
			log.Printf("Failed to get Prometheus target Filter err: %v, targetFilter: %v", err, targetFilter)
			// Continue without filtering rather than failing completely
		} else {
			filteredEvents, hasTargetMismatch := o.filterEventsByPrometheusTargets(events, targetFilter)
			// Check for target mismatch in two scenarios:
			// 1. We have events but all were filtered out due to target mismatch
			// 2. We have no events at all, but the instance_id is not in Prometheus targets
			shouldReturnError := false
			if len(events) > 0 && len(filteredEvents) == 0 && hasTargetMismatch {
				shouldReturnError = true
			} else if len(events) == 0 && req.InstanceID != "" && !o.isValidTarget(req.InstanceID, targetFilter) {
				shouldReturnError = true
			}
			if shouldReturnError {
				c.JSON(http.StatusBadRequest, gin.H{
					"status":  "error",
					"details": fmt.Sprintf("The target instance_id '%s' is not found in the current zone's Prometheus configuration. Please verify the instance_id is correct and belongs to this zone.", req.InstanceID),
					"available_targets": map[string]interface{}{
						"node_exporter_targets":    targetFilter.NodeExporterTargets,
						"libvirt_exporter_targets": targetFilter.LibvirtExporterTargets,
					},
				})
				return
			}
			log.Printf("Prometheus filtering applied - filter: %v, events: %d -> %d", targetFilter, len(events), len(filteredEvents))
		}
	} else {
		log.Printf("Prometheus filtering disabled by request parameter")
	}

	// Process metrics based on subject type
	result, err := o.processMetricsBySubject(req.Subject, req.InstanceID, filteredEvents, req.Start, req.End)
	if err != nil {
		log.Printf("Failed to process metrics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to process metrics",
			"details": err.Error(),
		})
		return
	}

	// Include raw events if requested
	if c.Query("include_raw") == "true" {
		result.RawEvents = filteredEvents
	}

	// Return response
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   result,
		"metadata": gin.H{
			"query_params": gin.H{
				"instance_id": req.InstanceID,
				"subject":     req.Subject,
				"start_time":  req.Start,
				"end_time":    req.End,
				"limit":       req.Limit,
			},
			"clickhouse_host": meteringHost,
			"clickhouse_port": meteringPort,
			"database":        req.Database,
		},
	})
}

// buildSQLQuery builds the ClickHouse SQL query
func (o *OpenMeterAPI) buildSQLQuery(req OpenMeterQueryRequest) string {
	timeFilter := ""

	// Set default time range if not provided (last 1 hour)
	now := time.Now().Unix()
	startTime := req.Start
	endTime := req.End

	if startTime == 0 && endTime == 0 {
		// Default to last 1 hour
		startTime = now - 3600
		endTime = now
	} else if startTime == 0 {
		// If only end time provided, default start to 1 hour before end
		startTime = endTime - 3600
	} else if endTime == 0 {
		// If only start time provided, default end to now
		endTime = now
	}

	// Update request with computed times for metadata
	req.Start = startTime
	req.End = endTime

	// Use optimized query for traffic metrics
	if req.Subject == "domain_north_south_inbound_bytes_total" || req.Subject == "domain_north_south_outbound_bytes_total" {
		return o.buildTrafficAggregationQuery(req)
	}

	timeFilter = fmt.Sprintf("AND toUnixTimestamp(time) BETWEEN %d AND %d", startTime, endTime)

	query := fmt.Sprintf(`
SELECT
  id,
  source,
  type,
  subject,
  time,
  data,
  ingested_at,
  stored_at
FROM om_events
WHERE subject = '%s'
  AND JSONExtractString(toString(JSONExtract(data, 'labels', 'JSON')), 'domain') = (
    SELECT any(JSONExtractString(toString(JSONExtract(data, 'labels', 'JSON')), 'domain'))
    FROM om_events
    WHERE subject = 'vm_instance_map'
      AND JSONExtractString(toString(JSONExtract(data, 'labels', 'JSON')), 'instance_id') = '%s'
  )
  %s
ORDER BY time ASC
LIMIT %d
FORMAT JSONEachRow
`, req.Subject, req.InstanceID, timeFilter, req.Limit)

	return strings.TrimSpace(query)
}

// buildTrafficAggregationQuery builds optimized aggregation query for traffic metrics
// Supports both inbound and outbound traffic queries through req.Subject parameter
// Features: Migration support, multi-interface handling, counter reset detection, data deduplication
func (o *OpenMeterAPI) buildTrafficAggregationQuery(req OpenMeterQueryRequest) string {
	timeFilter := fmt.Sprintf("AND time >= toDateTime(%d) AND time <= toDateTime(%d)", req.Start, req.End)

	query := fmt.Sprintf(`
SET allow_experimental_window_functions = 1;

WITH
  -- Collect domain + compute_ip with time window (migration compatible)
  vm_locations AS (
    SELECT
      JSON_VALUE(data, '$.labels.domain') AS domain,
      arrayElement(splitByChar(':', JSON_VALUE(data, '$.labels.instance')), 2) AS compute_ip
    FROM om_events
    WHERE subject = 'vm_instance_map'
      AND JSON_VALUE(data, '$.labels.instance_id') = '%s'
      %s
    GROUP BY domain, compute_ip
  ),

  -- Raw traffic data extraction (using JSON_VALUE for better vectorization)
  traffic_raw AS (
    SELECT
      time,
      toUnixTimestamp(time) AS unix_time,
      toFloat64(JSON_VALUE(data, '$.values[0][1]')) AS value,
      JSON_VALUE(data, '$.labels.domain') AS domain,
      coalesce(JSON_VALUE(data, '$.labels.interface'),
               JSON_VALUE(data, '$.labels.device'),
               'default') AS interface_id,
      arrayElement(splitByChar(':', JSON_VALUE(data, '$.labels.instance')), 2) AS compute_ip
    FROM om_events
    WHERE subject = '%s'
      %s
      AND JSON_VALUE(data, '$.labels.domain') IN (SELECT domain FROM vm_locations)
  ),

  -- ★ Critical deduplication step: keep "max value" for same (domain, interface_id, compute_ip, unix_time)
  --    Most robust for monotonic counters; multiple samples in same second won't affect diff logic
  dedup_raw AS (
    SELECT
      unix_time,
      domain,
      interface_id,
      compute_ip,
      max(value) AS value
    FROM traffic_raw
    GROUP BY
      unix_time, domain, interface_id, compute_ip
  ),

  -- Window-based difference calculation with deterministic ordering
  interface_increments AS (
    SELECT
      unix_time,
      domain,
      interface_id,
      compute_ip,
      value,
      lagInFrame(value) OVER (PARTITION BY domain, interface_id, compute_ip ORDER BY unix_time) AS prev_value,
      CASE
        WHEN prev_value IS NULL THEN 0
        WHEN value < prev_value THEN value      -- Counter wrap-around/restart
        ELSE value - prev_value
      END AS increment,
      CASE
        WHEN prev_value IS NOT NULL AND value < prev_value THEN 1 ELSE 0
      END AS is_reset
    FROM dedup_raw
  ),

  time_aggregated AS (
    SELECT
      unix_time,
      sum(increment)          AS total_increment_at_time,
      sum(is_reset)           AS resets_at_time,
      uniqExact(interface_id) AS active_interfaces,
      uniqExact(domain)       AS active_domains
    FROM interface_increments
    GROUP BY unix_time
  )

SELECT
  '%s' as instance_id,
  (SELECT count() FROM time_aggregated)                      AS total_records,
  (SELECT sum(total_increment_at_time) FROM time_aggregated) AS total_bytes,
  (SELECT sum(resets_at_time) FROM time_aggregated)          AS reset_count,
  (SELECT count() FROM time_aggregated)                      AS unique_timestamps,
  (SELECT max(active_interfaces) FROM time_aggregated)       AS max_device_count,
  1                                                          AS hypervisor_count,
  '%s'                                                       AS subject,
  %d                                                         AS start_time,
  %d                                                         AS end_time
FORMAT JSONEachRow
`, req.InstanceID, timeFilter, req.Subject, timeFilter, req.InstanceID, req.Subject, req.Start, req.End)

	return strings.TrimSpace(query)
}

// parseJSONEachRowResponse parses the JSONEachRow format response from ClickHouse
func (o *OpenMeterAPI) parseJSONEachRowResponse(body []byte) ([]OpenMeterEvent, error) {
	var events []OpenMeterEvent

	// Split response by lines
	lines := bytes.Split(body, []byte("\n"))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Parse each line as JSON
		var rawEvent map[string]interface{}
		if err := json.Unmarshal(line, &rawEvent); err != nil {
			log.Printf("Failed to parse event line: %v", err)
			continue
		}

		// Convert to OpenMeterEvent
		event := OpenMeterEvent{
			ID:         getStringValue(rawEvent, "id"),
			Source:     getStringValue(rawEvent, "source"),
			Type:       getStringValue(rawEvent, "type"),
			Subject:    getStringValue(rawEvent, "subject"),
			Time:       getStringValue(rawEvent, "time"),
			IngestedAt: getStringValue(rawEvent, "ingested_at"),
			StoredAt:   getStringValue(rawEvent, "stored_at"),
		}

		// Parse data field
		if dataStr := getStringValue(rawEvent, "data"); dataStr != "" {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(dataStr), &data); err == nil {
				event.Data = data
			}
		}

		events = append(events, event)
	}

	return events, nil
}

// getStringValue safely gets string value from map
func getStringValue(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// processMetricsBySubject processes metrics based on subject type
func (o *OpenMeterAPI) processMetricsBySubject(subject, instanceID string, events []OpenMeterEvent, queryStart, queryEnd int64) (*MetricProcessingResult, error) {
	result := &MetricProcessingResult{
		Subject:    subject,
		InstanceID: instanceID,
	}

	switch subject {
	case "libvirt_domain_info_vstate":
		processedData, summary, err := o.processVMStateMetrics(events, queryStart, queryEnd)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary

	case "domain_north_south_inbound_bytes_total", "domain_north_south_outbound_bytes_total":
		processedData, summary, err := o.processTrafficMetrics(events, subject, queryStart, queryEnd)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary

	case "libvirt_domain_info_virtual_cpus":
		processedData, summary, err := o.processCPUMetrics(events, queryStart, queryEnd)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary

	case "libvirt_domain_info_maximum_memory_bytes":
		processedData, summary, err := o.processMemoryMetrics(events, queryStart, queryEnd)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary

	case "libvirt_domain_block_stats_capacity_bytes":
		processedData, summary, err := o.processBlockStorageMetrics(events, queryStart, queryEnd)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary

	default:
		// For unknown subjects, return raw data with basic processing
		processedData, summary, err := o.processGenericMetrics(events)
		if err != nil {
			return nil, err
		}
		result.ProcessedData = processedData
		result.Summary = summary
	}

	return result, nil
}

// processVMStateMetrics processes VM state metrics
// Rules:
// 1. Merge consecutive periods with the same state
// 2. Calculate total uptime duration for 'running' state
// 3. Provide detailed state transition timeline
func (o *OpenMeterAPI) processVMStateMetrics(events []OpenMeterEvent, queryStart, queryEnd int64) (map[string]interface{}, map[string]interface{}, error) {
	if len(events) == 0 {
		return nil, map[string]interface{}{
			"total_uptime_minutes": 0,
			"total_uptime_hours":   0,
			"state_counts":         map[string]int{},
			"monitoring_periods":   0,
		}, nil
	}

	// Sort events by time (oldest first)
	sortedEvents := make([]OpenMeterEvent, len(events))
	copy(sortedEvents, events)
	sort.Slice(sortedEvents, func(i, j int) bool {
		return sortedEvents[i].Time < sortedEvents[j].Time
	})

	var stateSegments []map[string]interface{}
	var stateTransitions []map[string]interface{}
	var currentState string
	var currentStartTime string
	totalUptimeMinutes := 0.0
	stateCounts := make(map[string]int)
	stateDurations := make(map[string]map[string]interface{})

	// Get query period for calculating percentages
	var queryStartTime, queryEndTime string
	if queryStart > 0 && queryEnd > 0 {
		queryStartTime = time.Unix(queryStart, 0).Format("2006-01-02 15:04:05")
		queryEndTime = time.Unix(queryEnd, 0).Format("2006-01-02 15:04:05")
	} else {
		queryStartTime = sortedEvents[0].Time
		queryEndTime = sortedEvents[len(sortedEvents)-1].Time
	}
	queryPeriodMinutes := o.calculateTimeDifference(queryStartTime, queryEndTime)

	for i, event := range sortedEvents {
		// Extract state from metric value
		state := o.extractVMStateFromEvent(event)
		if state == "" {
			continue
		}

		// If this is the first event or state changed
		if currentState == "" || currentState != state {
			// Record state transition
			if currentState != "" {
				stateTransitions = append(stateTransitions, map[string]interface{}{
					"from_state": currentState,
					"to_state":   state,
					"timestamp":  event.Time,
				})

				// Close previous segment
				duration := o.calculateTimeDifference(currentStartTime, event.Time)
				stateSegments = append(stateSegments, map[string]interface{}{
					"state":            currentState,
					"start_time":       o.convertTimeStringToUnix(currentStartTime),
					"end_time":         o.convertTimeStringToUnix(event.Time),
					"duration_minutes": duration,
				})

				// Track durations for summary
				if stateDurations[currentState] == nil {
					stateDurations[currentState] = map[string]interface{}{
						"total_minutes": 0.0,
						"total_hours":   0.0,
						"percentage":    0.0,
					}
				}
				currentDuration := stateDurations[currentState]["total_minutes"].(float64)
				stateDurations[currentState]["total_minutes"] = currentDuration + duration
				stateDurations[currentState]["total_hours"] = (currentDuration + duration) / 60

				// Add to uptime if previous state was running
				if currentState == "running" {
					totalUptimeMinutes += duration
				}
			}

			// Count state occurrences
			stateCounts[state]++

			// Start new segment
			currentState = state
			// For the first event, use query start time if available
			if currentStartTime == "" && queryStart > 0 {
				currentStartTime = queryStartTime
			} else if currentStartTime == "" {
				currentStartTime = event.Time
			} else {
				currentStartTime = event.Time
			}
		}

		// Handle the last event - extend to query end time
		if i == len(sortedEvents)-1 {
			// Use query end time or current time
			endTime := queryEndTime
			if queryEndTime == "" {
				endTime = time.Now().Format("2006-01-02 15:04:05")
			}

			duration := o.calculateTimeDifference(currentStartTime, endTime)
			stateSegments = append(stateSegments, map[string]interface{}{
				"state":            currentState,
				"start_time":       o.convertTimeStringToUnix(currentStartTime),
				"end_time":         o.convertTimeStringToUnix(endTime),
				"duration_minutes": duration,
			})

			// Track durations for summary
			if stateDurations[currentState] == nil {
				stateDurations[currentState] = map[string]interface{}{
					"total_minutes": 0.0,
					"total_hours":   0.0,
					"percentage":    0.0,
				}
			}
			currentDuration := stateDurations[currentState]["total_minutes"].(float64)
			stateDurations[currentState]["total_minutes"] = currentDuration + duration
			stateDurations[currentState]["total_hours"] = (currentDuration + duration) / 60

			// Add to uptime if current state is running
			if currentState == "running" {
				totalUptimeMinutes += duration
			}
		}
	}

	// Calculate percentages
	if queryPeriodMinutes > 0 {
		for state, durations := range stateDurations {
			totalMinutes := durations["total_minutes"].(float64)
			stateDurations[state]["percentage"] = (totalMinutes / queryPeriodMinutes) * 100
		}
	}

	processedData := map[string]interface{}{
		"state_segments":    stateSegments,
		"state_transitions": stateTransitions,
	}

	summary := map[string]interface{}{
		"state_counts":         stateCounts,
		"state_durations":      stateDurations,
		"monitoring_periods":   len(stateSegments),
		"total_uptime_hours":   totalUptimeMinutes / 60,
		"total_uptime_minutes": totalUptimeMinutes,
		"query_period_minutes": queryPeriodMinutes,
	}

	return processedData, summary, nil
}

// processTrafficMetrics processes network traffic metrics
// Traffic metrics now always use aggregated results from buildTrafficAggregationQuery
func (o *OpenMeterAPI) processTrafficMetrics(events []OpenMeterEvent, subject string, queryStart, queryEnd int64) (*TrafficCalculationResult, map[string]interface{}, error) {
	if len(events) == 0 {
		return &TrafficCalculationResult{}, map[string]interface{}{
			"total_bytes": 0,
			"total_mb":    0,
		}, nil
	}

	// For traffic metrics, we now always use aggregated results from buildTrafficAggregationQuery
	// The query structure ensures we get exactly one event with aggregated data
	if events[0].Data != nil {
		return o.parseTrafficAggregationResult(events[0], subject, queryStart, queryEnd)
	}

	// This should not happen for traffic metrics, but provide a fallback
	return &TrafficCalculationResult{}, map[string]interface{}{
		"total_bytes": 0,
		"total_mb":    0,
	}, fmt.Errorf("unexpected data structure for traffic metrics")
}

// parseTrafficAggregationResult parses the aggregated traffic result from ClickHouse
func (o *OpenMeterAPI) parseTrafficAggregationResult(event OpenMeterEvent, subject string, queryStart, queryEnd int64) (*TrafficCalculationResult, map[string]interface{}, error) {
	// Extract values from the aggregated result
	var totalBytes int64 = 0
	resetCount := int64(0)
	uniqueTimestamps := int64(0)

	// Extract total_bytes (unified field name from query)
	if val, ok := event.Data["total_bytes"].(float64); ok {
		totalBytes = int64(val)
	}

	if val, ok := event.Data["reset_count"].(float64); ok {
		resetCount = int64(val)
	}
	if val, ok := event.Data["unique_timestamps"].(float64); ok {
		uniqueTimestamps = int64(val)
	}

	// Calculate duration for rate calculation
	durationMinutes := float64(queryEnd-queryStart) / 60.0
	var averageRateMbps float64
	if durationMinutes > 0 {
		averageRateMbps = (float64(totalBytes) / (1024 * 1024)) / (durationMinutes / 60) // MB per second
	}

	// Create result structure matching the expected format
	result := &TrafficCalculationResult{
		TotalBytes:     totalBytes,
		Segments:       []TrafficSegment{},
		Resets:         []TrafficReset{},
		HostMigrations: []HostMigrationDetected{},
		DataQuality: map[string]interface{}{
			"missing_intervals":     0,
			"continuous_monitoring": resetCount == 0,
			"interface_aggregation": true,
		},
	}

	// Extract device count for multi-interface tracking
	maxDeviceCount := int64(1)
	if val, ok := event.Data["max_device_count"].(float64); ok {
		maxDeviceCount = int64(val)
	}

	// Create summary matching the expected JSON format
	summary := map[string]interface{}{
		"total_bytes":           totalBytes,
		"total_mb":              float64(totalBytes) / (1024 * 1024),
		"total_gb":              float64(totalBytes) / (1024 * 1024 * 1024),
		"total_kb":              float64(totalBytes) / 1024,
		"monitoring_periods":    1, // Aggregated result represents one period
		"sdn_reset_count":       resetCount,
		"host_migrations_count": 0, // Not detected in aggregated query
		"average_rate_mbps":     averageRateMbps,
		"interface_aggregation": true,
		"unique_timestamps":     uniqueTimestamps,
		"max_device_count":      maxDeviceCount,
		"multi_interface_vm":    maxDeviceCount > 1,
	}

	return result, summary, nil
}

// processCPUMetrics processes CPU allocation metrics
// Rules:
// 1. Track CPU count changes over time
// 2. Calculate duration for each CPU allocation level
// 3. Do not merge segments with different CPU counts
// 4. Provide detailed timeline of CPU allocation changes
func (o *OpenMeterAPI) processCPUMetrics(events []OpenMeterEvent, queryStart, queryEnd int64) (map[string]interface{}, map[string]interface{}, error) {
	return o.processResourceMetrics(events, "virtual_cpus", "cpus", queryStart, queryEnd, func(event OpenMeterEvent) interface{} {
		return o.extractCPUCountFromEvent(event)
	})
}

// processMemoryMetrics processes memory allocation metrics
// Rules: Same as CPU metrics but for memory allocation
func (o *OpenMeterAPI) processMemoryMetrics(events []OpenMeterEvent, queryStart, queryEnd int64) (map[string]interface{}, map[string]interface{}, error) {
	return o.processResourceMetrics(events, "memory", "bytes", queryStart, queryEnd, func(event OpenMeterEvent) interface{} {
		return o.extractMemoryFromEvent(event)
	})
}

// processBlockStorageMetrics processes block storage capacity metrics
// Rules: Handle multiple disk devices separately and aggregate them
func (o *OpenMeterAPI) processBlockStorageMetrics(events []OpenMeterEvent, queryStart, queryEnd int64) (map[string]interface{}, map[string]interface{}, error) {
	if len(events) == 0 {
		return nil, map[string]interface{}{
			"total_storage_bytes": 0,
			"total_storage_gb":    0,
			"device_count":        0,
			"devices":             []map[string]interface{}{},
		}, nil
	}

	// Group events by device
	deviceEvents := make(map[string][]OpenMeterEvent)
	for _, event := range events {
		device := o.extractTargetDeviceFromEvent(event)
		if device == "" {
			device = "unknown"
		}
		deviceEvents[device] = append(deviceEvents[device], event)
	}

	// Process each device separately
	deviceResults := make(map[string]interface{})
	totalStorageBytes := int64(0)
	var allDevicesInfo []map[string]interface{}

	for device, devEvents := range deviceEvents {
		// Process this device's metrics
		processedData, summary, err := o.processResourceMetrics(devEvents, "block_storage", "bytes", queryStart, queryEnd, func(event OpenMeterEvent) interface{} {
			return o.extractBlockStorageFromEvent(event)
		})
		if err != nil {
			continue
		}

		deviceResults[device] = map[string]interface{}{
			"device":         device,
			"processed_data": processedData,
			"summary":        summary,
		}

		// Calculate current storage for this device (use latest value)
		if len(devEvents) > 0 {
			latestValue := o.extractBlockStorageFromEvent(devEvents[len(devEvents)-1])
			if storageBytes, ok := latestValue.(int64); ok {
				totalStorageBytes += storageBytes

				deviceInfo := map[string]interface{}{
					"device":        device,
					"storage_bytes": storageBytes,
					"storage_gb":    float64(storageBytes) / (1024 * 1024 * 1024),
					"storage_mb":    float64(storageBytes) / (1024 * 1024),
				}
				allDevicesInfo = append(allDevicesInfo, deviceInfo)
			}
		}
	}

	processedData := map[string]interface{}{
		"devices":          deviceResults,
		"device_breakdown": allDevicesInfo,
	}

	summary := map[string]interface{}{
		"total_storage_bytes": totalStorageBytes,
		"total_storage_gb":    float64(totalStorageBytes) / (1024 * 1024 * 1024),
		"total_storage_mb":    float64(totalStorageBytes) / (1024 * 1024),
		"device_count":        len(deviceEvents),
		"devices":             allDevicesInfo,
		"resource_type":       "block_storage_multi_device",
		"unit":                "bytes",
	}

	return processedData, summary, nil
}

// processResourceMetrics is a generic function for processing resource allocation metrics
// Rules:
// 1. Track resource value changes over time
// 2. Calculate duration for each resource allocation level
// 3. Do not merge segments with different values
// 4. Provide detailed timeline of resource allocation changes
func (o *OpenMeterAPI) processResourceMetrics(events []OpenMeterEvent, resourceType, unit string, queryStart, queryEnd int64, extractValueFunc func(OpenMeterEvent) interface{}) (map[string]interface{}, map[string]interface{}, error) {
	if len(events) == 0 {
		return nil, map[string]interface{}{
			"total_duration_minutes": 0,
			"total_duration_hours":   0,
			"resource_changes":       0,
			"monitoring_periods":     0,
			"value_distribution":     []map[string]interface{}{},
			"resource_type":          resourceType,
			"unit":                   unit,
		}, nil
	}

	// Sort events by time (oldest first)
	sortedEvents := make([]OpenMeterEvent, len(events))
	copy(sortedEvents, events)
	sort.Slice(sortedEvents, func(i, j int) bool {
		return sortedEvents[i].Time < sortedEvents[j].Time
	})

	var resourceSegments []map[string]interface{}
	var resourceChanges []map[string]interface{}
	var currentValue interface{}
	var currentStartTime string
	totalDurationMinutes := 0.0
	changeCount := 0
	valueDistribution := make(map[string]map[string]interface{})

	// Get query period for percentage calculations
	var queryStartTime, queryEndTime string
	if queryStart > 0 && queryEnd > 0 {
		queryStartTime = time.Unix(queryStart, 0).Format("2006-01-02 15:04:05")
		queryEndTime = time.Unix(queryEnd, 0).Format("2006-01-02 15:04:05")
	} else {
		queryStartTime = sortedEvents[0].Time
		queryEndTime = sortedEvents[len(sortedEvents)-1].Time
	}
	queryPeriodMinutes := o.calculateTimeDifference(queryStartTime, queryEndTime)

	for i, event := range sortedEvents {
		value := extractValueFunc(event)
		if value == nil {
			continue
		}

		// If this is the first event or value changed
		if currentValue == nil || !o.compareValues(currentValue, value) {
			// Record resource change
			if currentValue != nil {
				resourceChanges = append(resourceChanges, map[string]interface{}{
					"from_value":    currentValue,
					"to_value":      value,
					"timestamp":     event.Time,
					"change_reason": "resource_adjustment",
				})

				// Close previous segment
				duration := o.calculateTimeDifference(currentStartTime, event.Time)

				segment := map[string]interface{}{
					"value":            currentValue,
					"start_time":       currentStartTime,
					"end_time":         event.Time,
					"duration_minutes": duration,
				}

				// Add additional fields for memory and storage
				if resourceType == "memory" || resourceType == "block_storage" {
					if intVal, ok := currentValue.(int64); ok {
						segment["value_gb"] = float64(intVal) / (1024 * 1024 * 1024)
					}
				}

				resourceSegments = append(resourceSegments, segment)
				totalDurationMinutes += duration

				// Track value distribution
				currentValueKey := valueToString(currentValue)
				if valueDistribution[currentValueKey] == nil {
					valueDistribution[currentValueKey] = map[string]interface{}{
						"duration_minutes": 0.0,
						"duration_hours":   0.0,
						"percentage":       0.0,
					}

					// Add resource-specific metrics
					switch resourceType {
					case "virtual_cpus":
						if intVal, ok := currentValue.(int64); ok {
							valueDistribution[currentValueKey]["cpus"] = intVal
							valueDistribution[currentValueKey]["cpu_hours"] = 0.0
						}
					case "memory":
						if intVal, ok := currentValue.(int64); ok {
							valueDistribution[currentValueKey]["memory_gb"] = float64(intVal) / (1024 * 1024 * 1024)
							valueDistribution[currentValueKey]["memory_gb_hours"] = 0.0
						}
					case "block_storage":
						if intVal, ok := currentValue.(int64); ok {
							valueDistribution[currentValueKey]["storage_gb"] = float64(intVal) / (1024 * 1024 * 1024)
							valueDistribution[currentValueKey]["storage_gb_hours"] = 0.0
						}
					}
				}

				currentDuration := valueDistribution[currentValueKey]["duration_minutes"].(float64)
				valueDistribution[currentValueKey]["duration_minutes"] = currentDuration + duration
				valueDistribution[currentValueKey]["duration_hours"] = (currentDuration + duration) / 60

				// Calculate resource-hours
				switch resourceType {
				case "virtual_cpus":
					if intVal, ok := currentValue.(int64); ok {
						currentCpuHours := valueDistribution[currentValueKey]["cpu_hours"].(float64)
						valueDistribution[currentValueKey]["cpu_hours"] = currentCpuHours + (duration/60)*float64(intVal)
					}
				case "memory", "block_storage":
					if intVal, ok := currentValue.(int64); ok {
						gbValue := float64(intVal) / (1024 * 1024 * 1024)
						var fieldName string
						if resourceType == "memory" {
							fieldName = "memory_gb_hours"
						} else {
							fieldName = "storage_gb_hours"
						}
						currentGbHours := valueDistribution[currentValueKey][fieldName].(float64)
						valueDistribution[currentValueKey][fieldName] = currentGbHours + (duration/60)*gbValue
					}
				}

				changeCount++
			}

			// Start new segment
			currentValue = value
			// For the first event, use query start time if available
			if currentStartTime == "" && queryStart > 0 {
				currentStartTime = queryStartTime
			} else if currentStartTime == "" {
				currentStartTime = event.Time
			} else {
				currentStartTime = event.Time
			}
		}

		// Handle the last event
		if i == len(sortedEvents)-1 {
			// Use query end time or current time
			endTime := queryEndTime
			if queryEndTime == "" {
				endTime = time.Now().Format("2006-01-02 15:04:05")
			}

			duration := o.calculateTimeDifference(currentStartTime, endTime)
			formattedValue, _ := formatValue(currentValue)
			valueKey := valueToString(currentValue)

			// Convert time strings to Unix timestamps
			layout := "2006-01-02 15:04:05"
			var startUnix, endUnix int64
			if t, err := time.Parse(layout, currentStartTime); err == nil {
				startUnix = t.Unix()
			}
			if t, err := time.Parse(layout, endTime); err == nil {
				endUnix = t.Unix()
			}

			segment := map[string]interface{}{
				"value":            formattedValue,
				"start_time":       startUnix,
				"end_time":         endUnix,
				"duration_minutes": duration,
			}

			// Add additional fields for memory and storage
			if resourceType == "memory" || resourceType == "block_storage" {
				if intVal, ok := currentValue.(int64); ok {
					segment["value_gb"] = float64(intVal) / (1024 * 1024 * 1024)
				}
			}

			resourceSegments = append(resourceSegments, segment)
			totalDurationMinutes += duration

			// Track value distribution for final segment
			if valueDistribution[valueKey] == nil {
				valueDistribution[valueKey] = map[string]interface{}{
					"duration_minutes": 0.0,
					"duration_hours":   0.0,
					"percentage":       0.0,
				}

				// Add resource-specific metrics
				switch resourceType {
				case "virtual_cpus":
					if _, ok := currentValue.(int64); ok {
						valueDistribution[valueKey]["cpu_hours"] = 0.0
					}
				case "memory":
					if _, ok := currentValue.(int64); ok {
						valueDistribution[valueKey]["memory_gb_hours"] = 0.0
					}
				case "block_storage":
					if _, ok := currentValue.(int64); ok {
						valueDistribution[valueKey]["storage_gb_hours"] = 0.0
					}
				}
			}

			currentDuration := valueDistribution[valueKey]["duration_minutes"].(float64)
			valueDistribution[valueKey]["duration_minutes"] = currentDuration + duration
			valueDistribution[valueKey]["duration_hours"] = (currentDuration + duration) / 60

			// Calculate resource-hours for final segment
			switch resourceType {
			case "virtual_cpus":
				if intVal, ok := currentValue.(int64); ok {
					currentCpuHours := valueDistribution[valueKey]["cpu_hours"].(float64)
					valueDistribution[valueKey]["cpu_hours"] = currentCpuHours + (duration/60)*float64(intVal)
				}
			case "memory", "block_storage":
				if intVal, ok := currentValue.(int64); ok {
					gbValue := float64(intVal) / (1024 * 1024 * 1024)
					var fieldName string
					if resourceType == "memory" {
						fieldName = "memory_gb_hours"
					} else {
						fieldName = "storage_gb_hours"
					}
					currentGbHours := valueDistribution[valueKey][fieldName].(float64)
					valueDistribution[valueKey][fieldName] = currentGbHours + (duration/60)*gbValue
				}
			}
		}
	}

	// Calculate percentages and totals
	var totalResourceHours float64
	var averageResource float64

	if queryPeriodMinutes > 0 {
		for valueKey, distribution := range valueDistribution {
			totalMinutes := distribution["duration_minutes"].(float64)
			valueDistribution[valueKey]["percentage"] = (totalMinutes / queryPeriodMinutes) * 100

			// Add to totals
			switch resourceType {
			case "virtual_cpus":
				if cpuHours, ok := distribution["cpu_hours"].(float64); ok {
					totalResourceHours += cpuHours
				}
			case "memory":
				if memHours, ok := distribution["memory_gb_hours"].(float64); ok {
					totalResourceHours += memHours
				}
			case "block_storage":
				if storageHours, ok := distribution["storage_gb_hours"].(float64); ok {
					totalResourceHours += storageHours
				}
			}
		}

		if queryPeriodMinutes > 0 {
			averageResource = totalResourceHours / (queryPeriodMinutes / 60)
		}
	}

	processedData := map[string]interface{}{
		"resource_segments": resourceSegments,
		"resource_changes":  resourceChanges,
	}

	// Convert value_distribution from map to array format
	var valueDistributionArray []map[string]interface{}
	for valueStr, data := range valueDistribution {
		item := make(map[string]interface{})

		// Copy common fields
		item["duration_hours"] = data["duration_hours"]
		item["duration_minutes"] = data["duration_minutes"]
		item["percentage"] = data["percentage"]

		// Add resource-specific value field
		switch resourceType {
		case "virtual_cpus":
			if val, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
				item["vcpus"] = val
			}
			if cpuHours, exists := data["cpu_hours"]; exists {
				item["cpu_hours"] = cpuHours
			}
		case "memory":
			if val, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
				item["memory_bytes"] = val
				item["memory_gb"] = float64(val) / (1024 * 1024 * 1024)
			}
			if memoryGbHours, exists := data["memory_gb_hours"]; exists {
				item["memory_gb_hours"] = memoryGbHours
			}
		case "block_storage":
			if val, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
				item["storage_bytes"] = val
				item["storage_gb"] = float64(val) / (1024 * 1024 * 1024)
			}
			if storageGbHours, exists := data["storage_gb_hours"]; exists {
				item["storage_gb_hours"] = storageGbHours
			}
		default:
			// For generic resources, try to parse the value
			if val, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
				item["value"] = val
			} else if val, err := strconv.ParseFloat(valueStr, 64); err == nil {
				item["value"] = val
			} else {
				item["value"] = valueStr
			}
		}

		valueDistributionArray = append(valueDistributionArray, item)
	}

	// Sort by value for consistent output
	sort.Slice(valueDistributionArray, func(i, j int) bool {
		var iVal, jVal float64

		switch resourceType {
		case "virtual_cpus":
			if iv, ok := valueDistributionArray[i]["vcpus"].(int64); ok {
				iVal = float64(iv)
			}
			if jv, ok := valueDistributionArray[j]["vcpus"].(int64); ok {
				jVal = float64(jv)
			}
		case "memory":
			if iv, ok := valueDistributionArray[i]["memory_gb"].(float64); ok {
				iVal = iv
			}
			if jv, ok := valueDistributionArray[j]["memory_gb"].(float64); ok {
				jVal = jv
			}
		case "block_storage":
			if iv, ok := valueDistributionArray[i]["storage_gb"].(float64); ok {
				iVal = iv
			}
			if jv, ok := valueDistributionArray[j]["storage_gb"].(float64); ok {
				jVal = jv
			}
		default:
			switch iv := valueDistributionArray[i]["value"].(type) {
			case int64:
				iVal = float64(iv)
			case float64:
				iVal = iv
			}
			switch jv := valueDistributionArray[j]["value"].(type) {
			case int64:
				jVal = float64(jv)
			case float64:
				jVal = jv
			}
		}

		return iVal < jVal
	})

	summary := map[string]interface{}{
		"resource_changes":       changeCount,
		"resource_type":          resourceType,
		"total_duration_hours":   totalDurationMinutes / 60,
		"total_duration_minutes": totalDurationMinutes,
		"monitoring_periods":     len(resourceSegments),
		"unit":                   unit,
		"value_distribution":     valueDistributionArray,
	}

	// Add resource-specific summary fields
	switch resourceType {
	case "virtual_cpus":
		summary["total_cpu_hours"] = totalResourceHours
		summary["average_cpus"] = averageResource
	case "memory":
		summary["total_memory_gb_hours"] = totalResourceHours
		summary["average_memory_gb"] = averageResource
	case "block_storage":
		summary["total_storage_gb_hours"] = totalResourceHours
		summary["average_storage_gb"] = averageResource
	}

	return processedData, summary, nil
}

// processGenericMetrics processes unknown metric types with basic aggregation
func (o *OpenMeterAPI) processGenericMetrics(events []OpenMeterEvent) ([]OpenMeterEvent, map[string]interface{}, error) {
	summary := map[string]interface{}{
		"total_events": len(events),
		"processing":   "generic",
		"note":         "Unknown metric type, returning raw events",
	}

	return events, summary, nil
}

// Helper functions for extracting values from events

// extractVMStateFromEvent extracts VM state from metric event
func (o *OpenMeterAPI) extractVMStateFromEvent(event OpenMeterEvent) string {
	// Extract state from metric values
	if values, ok := event.Data["values"].([]interface{}); ok && len(values) > 0 {
		if valueArray, ok := values[0].([]interface{}); ok && len(valueArray) > 1 {
			if stateValue, ok := valueArray[1].(string); ok {
				// Map numeric state to string
				switch stateValue {
				case "1":
					return "running"
				case "2":
					return "blocked"
				case "3":
					return "paused"
				case "4":
					return "shutdown"
				case "5":
					return "shutoff"
				case "6":
					return "crashed"
				default:
					return "unknown"
				}
			}
		}
	}
	return ""
}

// extractHostFromEvent extracts host information from metric event
func (o *OpenMeterAPI) extractHostFromEvent(event OpenMeterEvent) string {
	if labels, ok := event.Data["labels"].(map[string]interface{}); ok {
		if hypervisor, ok := labels["hypervisor"].(string); ok {
			return hypervisor
		}
		if instance, ok := labels["instance"].(string); ok {
			return instance
		}
	}
	return ""
}

// extractCPUCountFromEvent extracts CPU count from metric event
func (o *OpenMeterAPI) extractCPUCountFromEvent(event OpenMeterEvent) interface{} {
	if values, ok := event.Data["values"].([]interface{}); ok && len(values) > 0 {
		if valueArray, ok := values[0].([]interface{}); ok && len(valueArray) > 1 {
			if valueStr, ok := valueArray[1].(string); ok {
				if value, err := strconv.Atoi(valueStr); err == nil {
					return value
				}
			}
		}
	}
	return nil
}

// extractMemoryFromEvent extracts memory bytes from metric event
func (o *OpenMeterAPI) extractMemoryFromEvent(event OpenMeterEvent) interface{} {
	if values, ok := event.Data["values"].([]interface{}); ok && len(values) > 0 {
		if valueArray, ok := values[0].([]interface{}); ok && len(valueArray) > 1 {
			if valueStr, ok := valueArray[1].(string); ok {
				if value, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
					return value
				}
			}
		}
	}
	return nil
}

// extractBlockStorageFromEvent extracts block storage bytes from metric event
func (o *OpenMeterAPI) extractBlockStorageFromEvent(event OpenMeterEvent) interface{} {
	return o.extractMemoryFromEvent(event) // Same extraction logic as memory
}

// extractTargetDeviceFromEvent extracts target device name from metric event labels
func (o *OpenMeterAPI) extractTargetDeviceFromEvent(event OpenMeterEvent) string {
	if labels, ok := event.Data["labels"].(map[string]interface{}); ok {
		if device, ok := labels["target_device"].(string); ok {
			return device
		}
	}
	return ""
}

// convertTimeStringToUnix converts time string to Unix timestamp
func (o *OpenMeterAPI) convertTimeStringToUnix(timeStr string) int64 {
	layout := "2006-01-02 15:04:05"
	if t, err := time.Parse(layout, timeStr); err == nil {
		return t.Unix()
	}
	return time.Now().Unix()
}

// formatValue formats a value for consistent output
func formatValue(value interface{}) (interface{}, interface{}) {
	return value, value
}

// valueToString converts a value to string for map key usage
func valueToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// calculateTimeDifferenceFromUnix calculates time difference in minutes between two Unix timestamps
func (o *OpenMeterAPI) calculateTimeDifferenceFromUnix(startTimestamp, endTimestamp int64) float64 {
	if startTimestamp == 0 || endTimestamp == 0 {
		return 0
	}
	duration := time.Duration(endTimestamp-startTimestamp) * time.Second
	return duration.Minutes()
}

// calculateTimeDifference calculates time difference in minutes between two time strings
func (o *OpenMeterAPI) calculateTimeDifference(startTime, endTime string) float64 {
	layout := "2006-01-02 15:04:05"

	start, err1 := time.Parse(layout, startTime)
	end, err2 := time.Parse(layout, endTime)

	if err1 != nil || err2 != nil {
		return 0
	}

	duration := end.Sub(start)
	return duration.Minutes()
}

// compareValues compares two values for equality
func (o *OpenMeterAPI) compareValues(a, b interface{}) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// QueryInstanceMetricsBySubject queries metrics for a specific subject using path parameters
func (o *OpenMeterAPI) QueryInstanceMetricsBySubject(c *gin.Context) {
	instanceID := c.Param("instance_id")
	subject := c.Param("subject")

	// Debug logging
	log.Printf("QueryInstanceMetricsBySubject - InstanceID=%s, Subject=%s", instanceID, subject)

	if instanceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "instance_id parameter is required",
		})
		return
	}

	if subject == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "subject parameter is required",
		})
		return
	}

	// Convert path parameters to query parameters
	c.Request.URL.RawQuery = fmt.Sprintf("instance_id=%s&subject=%s&%s",
		url.QueryEscape(instanceID), url.QueryEscape(subject), c.Request.URL.RawQuery)

	// Call the main query method
	o.QueryOpenMeterMetrics(c)
}

// GetAvailableSubjects returns available metric subjects
func (o *OpenMeterAPI) GetAvailableSubjects(c *gin.Context) {
	subjects := []string{
		"vm_instance_map",
		"libvirt_domain_info_vstate",
		"domain_north_south_inbound_bytes_total",
		"domain_north_south_outbound_bytes_total",
		"libvirt_domain_info_virtual_cpus",
		"libvirt_domain_info_maximum_memory_bytes",
		"libvirt_domain_block_stats_capacity_bytes",
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"subjects": subjects,
		"count":    len(subjects),
	})
}
