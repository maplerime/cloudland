package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
)

// ============================================================================
// N9E Client - Nightingale API communication
// ============================================================================

// N9EClient handles Nightingale API communication with JWT authentication
type N9EClient struct {
	clusterURL         string
	username           string
	password           string
	userGroupID        int64            // User group ID for creating business groups
	templatePath       string           // Path to PromQL template files
	datasourceName     string           // Datasource name (e.g., "VictoriaMetrics")
	datasourceID       int64            // Cached datasource ID
	businessGroupCache map[string]int64 // owner -> business_group_id cache
	cacheMutex         sync.RWMutex     // Mutex for cache operations
	httpClient         *http.Client
	token              string
	tokenExpiry        time.Time
	tokenMutex         sync.RWMutex
	notifyRuleName     string // Notify rule name from config (e.g. "cloudland_guest_alert")
	notifyRuleID       int64  // Cached notify rule ID (0 = not yet resolved)
}

// N9EDatasourceQuery represents a single datasource filter in datasource_queries
type N9EDatasourceQuery struct {
	MatchType int     `json:"match_type"` // 0 = exact match
	Op        string  `json:"op"`         // "in"
	Values    []int64 `json:"values"`
}

// N9EAlertRule represents the structure for creating/updating alert rules in N9E
type N9EAlertRule struct {
	RuleName          string               `json:"name"`
	GroupID           int64                `json:"group_id"`
	Cate              string               `json:"cate"`
	Prod              string               `json:"prod"`
	DatasourceIDs     []int64              `json:"datasource_ids"`
	DatasourceQueries []N9EDatasourceQuery `json:"datasource_queries"`
	PromForDuration   int                  `json:"prom_for_duration"`  // seconds
	PromEvalInterval  int                  `json:"prom_eval_interval"` // seconds
	Severity          int                  `json:"severity"`           // 1=Critical, 2=Warning, 3=Info
	Disabled          int                  `json:"disabled"`           // 0=enabled, 1=disabled
	NotifyRepeatStep  int                  `json:"notify_repeat_step"` // minutes, 0=repeat every eval
	RuleConfig        N9ERuleConfig        `json:"rule_config"`
	NotifyRecovered   int                  `json:"notify_recovered"`
	EnableStime       string               `json:"enable_stime"`
	EnableEtime       string               `json:"enable_etime"`
	EnableDaysOfWeek  []string             `json:"enable_days_of_week"`
	NotifyChannels    []string             `json:"notify_channels,omitempty"`
	Callbacks         []string             `json:"callbacks,omitempty"`
	NotifyRuleIDs     []int64              `json:"notify_rule_ids,omitempty"`
	NotifyVersion     int                  `json:"notify_version"` // 0=旧版(notify_groups), 1=新版(notify_rule_ids)
}

// N9ERuleConfig represents the rule_config structure
type N9ERuleConfig struct {
	Queries []N9EQuery `json:"queries"`
}

// N9EQuery represents a single query in rule_config
type N9EQuery struct {
	PromQL   string `json:"prom_ql"`
	Severity int    `json:"severity"`
}

// N9ETokenResponse represents the JWT token response from N9E
type N9ETokenResponse struct {
	Dat struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
	} `json:"dat"`
}

// N9ERuleResponse represents the response when creating/querying alert rules
// N9E returns: {"dat":{"rule_name":""},"err":""}
type N9ERuleResponse struct {
	Dat map[string]string `json:"dat"` // rule_name -> error message (empty if success)
	Err string            `json:"err"`
}

// N9ERuleItem represents a single rule in the list response
type N9ERuleItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	GroupID  int64  `json:"group_id"`
	Disabled int    `json:"disabled"`
}

// N9ERuleListResponse represents the response when listing alert rules
type N9ERuleListResponse struct {
	Dat []N9ERuleItem `json:"dat"`
	Err string        `json:"err"`
}

// N9ENotifyRuleItem represents a single notify rule in the list response
type N9ENotifyRuleItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// N9ENotifyRuleListResponse represents the response when listing notify rules
type N9ENotifyRuleListResponse struct {
	Dat []N9ENotifyRuleItem `json:"dat"`
	Err string              `json:"err"`
}

// NewN9EClient creates a new Nightingale API client
func NewN9EClient(clusterURL, username, password string, userGroupID int64, templatePath, datasourceName, notifyRuleName string) *N9EClient {
	client := &N9EClient{
		clusterURL:         clusterURL,
		username:           username,
		password:           password,
		userGroupID:        userGroupID,
		templatePath:       templatePath,
		datasourceName:     datasourceName,
		notifyRuleName:     notifyRuleName,
		businessGroupCache: make(map[string]int64),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Initialize datasource ID cache at startup
	ctx := context.Background()
	if id, err := client.GetDataSourceByName(ctx, datasourceName); err == nil {
		client.datasourceID = id
		log.Printf("[N9E] Datasource initialized: %s (id=%d)", datasourceName, id)
	} else {
		log.Printf("[N9E] WARNING: Failed to initialize datasource %s: %v", datasourceName, err)
	}

	return client
}

// GetNotifyRuleIDByName queries N9E for notify rules and returns the ID matching c.notifyRuleName.
// The result is cached so that N9E is only queried once per process lifetime.
func (c *N9EClient) GetNotifyRuleIDByName(ctx context.Context) (int64, error) {
	c.cacheMutex.RLock()
	if c.notifyRuleID > 0 {
		id := c.notifyRuleID
		c.cacheMutex.RUnlock()
		return id, nil
	}
	c.cacheMutex.RUnlock()

	token, err := c.getToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get auth token: %v", err)
	}

	listURL := fmt.Sprintf("%s/api/n9e/notify-rules", c.clusterURL)
	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create notify-rules request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("notify-rules request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var listResp N9ENotifyRuleListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return 0, fmt.Errorf("failed to decode notify-rules response: %v", err)
	}
	if listResp.Err != "" {
		return 0, fmt.Errorf("N9E notify-rules API error: %s", listResp.Err)
	}

	for _, item := range listResp.Dat {
		if item.Name == c.notifyRuleName {
			c.cacheMutex.Lock()
			c.notifyRuleID = item.ID
			c.cacheMutex.Unlock()
			log.Printf("[N9E] Resolved notify rule %q -> id=%d", c.notifyRuleName, item.ID)
			return item.ID, nil
		}
	}

	return 0, fmt.Errorf("notify rule %q not found in N9E", c.notifyRuleName)
}

// authenticate performs initial authentication with N9E API
func (c *N9EClient) authenticate(ctx context.Context) error {
	authURL := fmt.Sprintf("%s/api/n9e/auth/login", c.clusterURL)

	payload := map[string]string{
		"username": c.username,
		"password": c.password,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal auth payload: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", authURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create auth request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp N9ETokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode auth response: %v", err)
	}

	c.tokenMutex.Lock()
	c.token = tokenResp.Dat.AccessToken
	// Set expiry 5 minutes before actual expiry for safety
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.Dat.ExpiresIn-300) * time.Second)
	c.tokenMutex.Unlock()

	log.Printf("N9E authentication successful, token expires at: %v", c.tokenExpiry)
	return nil
}

// getToken returns a valid JWT token, refreshing if necessary
func (c *N9EClient) getToken(ctx context.Context) (string, error) {
	c.tokenMutex.RLock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		token := c.token
		c.tokenMutex.RUnlock()
		return token, nil
	}
	c.tokenMutex.RUnlock()

	// Token expired or not available, re-authenticate
	if err := c.authenticate(ctx); err != nil {
		return "", err
	}

	c.tokenMutex.RLock()
	defer c.tokenMutex.RUnlock()
	return c.token, nil
}

// CreateAlertRule creates a new alert rule in N9E
// Returns the N9E-assigned rule ID
func (c *N9EClient) CreateAlertRule(ctx context.Context, rule N9EAlertRule) (int64, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get auth token: %v", err)
	}

	// Validate required fields
	if rule.GroupID == 0 {
		return 0, fmt.Errorf("GroupID is required")
	}

	// Idempotent check: if rule with same name already exists, return its ID
	if existingID, err := c.GetAlertRuleByName(ctx, rule.GroupID, rule.RuleName); err == nil {
		log.Printf("[N9E] Alert rule %q already exists in group %d (id=%d), returning existing ID", rule.RuleName, rule.GroupID, existingID)
		return existingID, nil
	}

	// N9E API expects: /api/n9e/busi-group/{group_id}/alert-rules
	createURL := fmt.Sprintf("%s/api/n9e/busi-group/%d/alert-rules", c.clusterURL, rule.GroupID)

	// Set default values for optional fields
	if rule.Cate == "" {
		rule.Cate = "prometheus"
	}
	if rule.Prod == "" {
		rule.Prod = "metric"
	}
	if len(rule.EnableDaysOfWeek) == 0 {
		rule.EnableDaysOfWeek = []string{"0", "1", "2", "3", "4", "5", "6"}
	}
	if rule.EnableStime == "" {
		rule.EnableStime = "00:00"
	}
	if rule.EnableEtime == "" {
		rule.EnableEtime = "00:00"
	}
	if rule.NotifyRecovered == 0 {
		rule.NotifyRecovered = 1
	}
	if rule.PromEvalInterval == 0 {
		rule.PromEvalInterval = 15
	}

	// Inject datasource ID if configured
	dsID := c.datasourceID
	if dsID == 0 && c.datasourceName != "" {
		if id, err := c.GetDataSourceByName(ctx, c.datasourceName); err == nil && id > 0 {
			dsID = id
		} else {
			log.Printf("[N9E] Warning: could not resolve datasource %q: %v", c.datasourceName, err)
		}
	}
	if dsID != 0 {
		if len(rule.DatasourceIDs) == 0 {
			rule.DatasourceIDs = []int64{dsID}
		}
		if len(rule.DatasourceQueries) == 0 {
			rule.DatasourceQueries = []N9EDatasourceQuery{
				{MatchType: 0, Op: "in", Values: []int64{dsID}},
			}
		}
		log.Printf("[N9E] Bound datasource %q (id=%d) to alert rule %q", c.datasourceName, dsID, rule.RuleName)
	}

	// Inject notify rule if configured
	if c.notifyRuleName != "" {
		if notifyID, err := c.GetNotifyRuleIDByName(ctx); err == nil && notifyID > 0 {
			rule.NotifyRuleIDs = []int64{notifyID}
			rule.NotifyVersion = 1
			log.Printf("[N9E] Bound notify rule %q (id=%d) to alert rule %q", c.notifyRuleName, notifyID, rule.RuleName)
		} else {
			log.Printf("[N9E] Warning: could not resolve notify rule %q: %v", c.notifyRuleName, err)
		}
	}

	// Log the PromQL being sent (for debugging)
	if len(rule.RuleConfig.Queries) > 0 {
		log.Printf("Creating N9E rule: %s, PromQL length: %d bytes", rule.RuleName, len(rule.RuleConfig.Queries[0].PromQL))
		log.Printf("PromQL preview: %.200s...", rule.RuleConfig.Queries[0].PromQL)
	}

	// N9E expects an array of rules
	rulesArray := []N9EAlertRule{rule}
	jsonData, err := json.Marshal(rulesArray)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal rule: %v", err)
	}

	// Log the JSON payload size
	log.Printf("JSON payload size: %d bytes", len(jsonData))

	req, err := http.NewRequestWithContext(ctx, "POST", createURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("create rule request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Printf("N9E API error response (status %d): %s", resp.StatusCode, string(body))
		return 0, fmt.Errorf("create rule failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Log response for debugging
	log.Printf("N9E API response (status %d, length %d bytes): %.500s", resp.StatusCode, len(body), string(body))

	var ruleResp N9ERuleResponse
	if err := json.Unmarshal(body, &ruleResp); err != nil {
		log.Printf("Failed to decode N9E response, raw body: %s", string(body))
		return 0, fmt.Errorf("failed to decode response: %v", err)
	}

	if ruleResp.Err != "" {
		return 0, fmt.Errorf("N9E API error: %s", ruleResp.Err)
	}

	// Check if rule creation succeeded
	if errMsg, ok := ruleResp.Dat[rule.RuleName]; ok && errMsg != "" {
		return 0, fmt.Errorf("N9E rule creation error: %s", errMsg)
	}

	log.Printf("Created N9E alert rule: %s, querying for ID...", rule.RuleName)

	// Query N9E to get the auto-assigned rule ID
	ruleID, err := c.GetAlertRuleByName(ctx, rule.GroupID, rule.RuleName)
	if err != nil {
		log.Printf("Warning: Rule created but failed to get ID: %v", err)
		return 0, fmt.Errorf("rule created but failed to get ID: %v", err)
	}

	log.Printf("N9E rule '%s' created successfully with ID: %d", rule.RuleName, ruleID)
	return ruleID, nil
}

// DeleteAlertRule deletes an alert rule from N9E
func (c *N9EClient) DeleteAlertRule(ctx context.Context, businessGroupID, ruleID int64) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get auth token: %v", err)
	}

	// N9E API expects batch delete: DELETE /api/n9e/busi-group/{group_id}/alert-rules with {"ids":[rule_id]}
	deleteURL := fmt.Sprintf("%s/api/n9e/busi-group/%d/alert-rules", c.clusterURL, businessGroupID)

	requestBody := map[string][]int64{
		"ids": {ruleID},
	}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create delete request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete rule request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete rule failed with status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Deleted N9E alert rule ID: %d", ruleID)
	return nil
}

// GetAlertRule retrieves an alert rule from N9E and returns raw JSON data
func (c *N9EClient) GetAlertRule(ctx context.Context, ruleID int64) (map[string]interface{}, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %v", err)
	}

	getURL := fmt.Sprintf("%s/api/n9e/alert-rule/%d", c.clusterURL, ruleID)

	req, err := http.NewRequestWithContext(ctx, "GET", getURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get rule request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get rule failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response as generic JSON
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode rule: %v", err)
	}

	// Check if there's an error in the response
	if errMsg, ok := result["err"].(string); ok && errMsg != "" {
		return nil, fmt.Errorf("N9E API error: %s", errMsg)
	}

	return result, nil
}

// GetAlertRuleByName queries N9E for a rule by name and returns its ID
// Uses the busi-groups API to get all rules and filter by name
func (c *N9EClient) GetAlertRuleByName(ctx context.Context, groupID int64, ruleName string) (int64, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get auth token: %v", err)
	}

	// Query the list of rules for the business group using busi-groups API
	listURL := fmt.Sprintf("%s/api/n9e/busi-groups/alert-rules?gids=%d", c.clusterURL, groupID)

	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create list request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list rules request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list rules failed with status %d: %s", resp.StatusCode, string(body))
	}

	var listResp N9ERuleListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return 0, fmt.Errorf("failed to decode list response: %v", err)
	}

	if listResp.Err != "" {
		return 0, fmt.Errorf("N9E API error: %s", listResp.Err)
	}

	// Find the rule by name
	for _, rule := range listResp.Dat {
		if rule.Name == ruleName {
			log.Printf("Found N9E rule '%s' with ID: %d", ruleName, rule.ID)
			return rule.ID, nil
		}
	}

	return 0, fmt.Errorf("rule not found: %s", ruleName)
}

// ============================================================================
// Datasource Management - Query and cache datasource information
// ============================================================================

// N9EDataSource represents a datasource in N9E
type N9EDataSource struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	PluginType  string `json:"plugin_type"`
	Description string `json:"description"`
}

// N9EDataSourceListResponse represents the response from datasource list API
type N9EDataSourceListResponse struct {
	Data  []N9EDataSource `json:"data"`
	Error string          `json:"error"`
}

// QueryDataSources queries all datasources from N9E
func (c *N9EClient) QueryDataSources(ctx context.Context) ([]N9EDataSource, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	apiURL := fmt.Sprintf("%s/api/n9e/datasource/list", c.clusterURL)
	// N9E datasource list API requires POST with empty JSON body
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query datasources: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result N9EDataSourceListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("N9E API error: %s", result.Error)
	}

	log.Printf("[N9E] Queried %d datasources", len(result.Data))
	return result.Data, nil
}

// GetDataSourceByName queries datasource by name and returns its ID
func (c *N9EClient) GetDataSourceByName(ctx context.Context, name string) (int64, error) {
	// Check if we have a cached datasource ID and name matches
	if c.datasourceID != 0 && c.datasourceName == name {
		return c.datasourceID, nil
	}

	log.Printf("[N9E] Querying datasource: %s", name)
	datasources, err := c.QueryDataSources(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query datasources: %w", err)
	}

	for _, ds := range datasources {
		if ds.Name == name {
			log.Printf("[N9E] Datasource found: %s (id=%d)", name, ds.ID)
			// Update cache
			c.datasourceID = ds.ID
			c.datasourceName = name
			return ds.ID, nil
		}
	}

	return 0, fmt.Errorf("datasource not found: %s", name)
}

// ============================================================================
// Business Group Management - Query, create, and cache business groups
// ============================================================================

// N9EBusinessGroup represents a business group in N9E
type N9EBusinessGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// N9EBusinessGroupListResponse represents the response from business group list API
type N9EBusinessGroupListResponse struct {
	Dat []N9EBusinessGroup `json:"dat"`
	Err string             `json:"err"`
}

// N9EBusinessGroupMember represents a member in business group
type N9EBusinessGroupMember struct {
	UserGroupID int64  `json:"user_group_id"`
	PermFlag    string `json:"perm_flag"`
}

// N9ECreateBusinessGroupRequest represents the request to create a business group
type N9ECreateBusinessGroupRequest struct {
	Name    string                   `json:"name"`
	Members []N9EBusinessGroupMember `json:"members"`
}

// N9ECreateBusinessGroupResponse represents the response from creating a business group
type N9ECreateBusinessGroupResponse struct {
	Dat interface{} `json:"dat"`
	Err string      `json:"err"`
}

// QueryBusinessGroups queries all business groups from N9E
func (c *N9EClient) QueryBusinessGroups(ctx context.Context) ([]N9EBusinessGroup, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	apiURL := fmt.Sprintf("%s/api/n9e/busi-groups", c.clusterURL)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query business groups: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result N9EBusinessGroupListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Err != "" {
		return nil, fmt.Errorf("N9E API error: %s", result.Err)
	}

	return result.Dat, nil
}

// GetBusinessGroupByName queries business group by name (owner UUID) and returns its ID
func (c *N9EClient) GetBusinessGroupByName(ctx context.Context, name string) (int64, error) {
	// Check cache first
	c.cacheMutex.RLock()
	if id, exists := c.businessGroupCache[name]; exists {
		c.cacheMutex.RUnlock()
		return id, nil
	}
	c.cacheMutex.RUnlock()

	log.Printf("[N9E] Querying business group for owner: %s", name)
	groups, err := c.QueryBusinessGroups(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query business groups: %w", err)
	}

	for _, group := range groups {
		if group.Name == name {
			// Update cache
			c.cacheMutex.Lock()
			c.businessGroupCache[name] = group.ID
			c.cacheMutex.Unlock()
			return group.ID, nil
		}
	}

	return 0, fmt.Errorf("business group not found: %s", name)
}

// CreateBusinessGroup creates a new business group for the given owner
func (c *N9EClient) CreateBusinessGroup(ctx context.Context, regionName string) (int64, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get token: %w", err)
	}

	// Create request with regionName as name and associate with user group
	reqBody := N9ECreateBusinessGroupRequest{
		Name: regionName,
		Members: []N9EBusinessGroupMember{
			{
				UserGroupID: c.userGroupID,
				PermFlag:    "rw",
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("[N9E] Creating business group: %s", regionName)
	apiURL := fmt.Sprintf("%s/api/n9e/busi-groups", c.clusterURL)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to create business group: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result N9ECreateBusinessGroupResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Err != "" {
		return 0, fmt.Errorf("N9E API error: %s", result.Err)
	}

	log.Printf("[N9E] Business group created successfully: %s", regionName)

	// Query back to get the ID
	id, err := c.GetBusinessGroupByName(ctx, regionName)
	if err != nil {
		return 0, fmt.Errorf("failed to query created business group: %w", err)
	}

	log.Printf("[N9E] Business group ID: %d", id)
	return id, nil
}

// GetOrCreateBusinessGroup gets or creates a business group for the given region
// Business group name is the region identifier (console.host from config.toml)
func (c *N9EClient) GetOrCreateBusinessGroup(ctx context.Context, regionName string) (int64, error) {
	// Try to get existing group (checks cache first)
	id, err := c.GetBusinessGroupByName(ctx, regionName)
	if err == nil {
		return id, nil
	}

	// Business group doesn't exist, create it
	id, err = c.CreateBusinessGroup(ctx, regionName)
	if err != nil {
		// If creation failed, try querying again (maybe created by concurrent request)
		if id2, err2 := c.GetBusinessGroupByName(ctx, regionName); err2 == nil {
			log.Printf("[N9E] Business group created by concurrent request: %s (id=%d)", regionName, id2)
			return id2, nil
		}
		return 0, fmt.Errorf("failed to create business group: %w", err)
	}

	return id, nil
}

// DeleteBusinessGroupByID deletes a business group from N9E by its numeric ID
func (c *N9EClient) DeleteBusinessGroupByID(ctx context.Context, id int64) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	deleteURL := fmt.Sprintf("%s/api/n9e/busi-group/%d", c.clusterURL, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete business group request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete business group failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Invalidate cache
	c.cacheMutex.Lock()
	for name, cachedID := range c.businessGroupCache {
		if cachedID == id {
			delete(c.businessGroupCache, name)
			break
		}
	}
	c.cacheMutex.Unlock()

	log.Printf("[N9E] Deleted business group ID: %d", id)
	return nil
}

// ============================================================================
// Anchor Manager - VictoriaMetrics anchor metric management
// ============================================================================

// AnchorManager handles vm_rule_anchor metric management via VictoriaMetrics API
type AnchorManager struct {
	vmQueryURL  string // http://<host>/select/0/prometheus
	vmImportURL string // http://<host>/insert/0/prometheus
	vmDeleteURL string // http://<host>/delete/0/prometheus
	httpClient  *http.Client
}

// VMInstance represents a VM instance for anchor binding
type VMInstance struct {
	Region     string `json:"region"`
	Domain     string `json:"domain"`
	InstanceID string `json:"instance_id"`
	Owner      string `json:"owner"` // Rule owner (UUID)
}

// NewAnchorManager creates a new anchor manager
func NewAnchorManager(vmQueryURL, vmImportURL, vmDeleteURL string, timeout time.Duration) *AnchorManager {
	return &AnchorManager{
		vmQueryURL:  vmQueryURL,
		vmImportURL: vmImportURL,
		vmDeleteURL: vmDeleteURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// BindVMsToRule writes vm_rule_anchor metrics with value=1 to bind VMs to a rule
func (am *AnchorManager) BindVMsToRule(ctx context.Context, ruleUUID string, vms []VMInstance) error {
	if len(vms) == 0 {
		return fmt.Errorf("no VMs provided for binding")
	}

	metrics := am.buildAnchorMetrics(ruleUUID, vms, 1)
	return am.importToVM(ctx, metrics)
}

// UnbindVMsFromRule writes vm_rule_anchor metrics with value=0 to unbind VMs from a rule
func (am *AnchorManager) UnbindVMsFromRule(ctx context.Context, ruleUUID string, vms []VMInstance) error {
	if len(vms) == 0 {
		return fmt.Errorf("no VMs provided for unbinding")
	}

	metrics := am.buildAnchorMetrics(ruleUUID, vms, 0)
	return am.importToVM(ctx, metrics)
}

// QueryAnchorLinks queries VictoriaMetrics for all VMs with anchor value=1 for a rule
func (am *AnchorManager) QueryAnchorLinks(ctx context.Context, ruleUUID string) ([]VMInstance, error) {
	// Query: last_over_time(vm_rule_anchor{rule_uuid="<uuid>"}[30d]) == 1
	// Use last_over_time to query historical data within retention period
	query := fmt.Sprintf(`last_over_time(vm_rule_anchor{rule_uuid="%s"}[30d]) == 1`, ruleUUID)

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse VictoriaMetrics JSON response
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	vms := make([]VMInstance, 0, len(result.Data.Result))
	for _, r := range result.Data.Result {
		vms = append(vms, VMInstance{
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Owner:      r.Metric["owner"],
		})
	}

	log.Printf("Queried %d VMs bound to rule %s", len(vms), ruleUUID)
	return vms, nil
}

// QueryAnchorLinksByOwner queries VictoriaMetrics for all VMs with anchor value=1 for an owner
func (am *AnchorManager) QueryAnchorLinksByOwner(ctx context.Context, owner string) (map[string][]VMInstance, error) {
	// Query: last_over_time(vm_rule_anchor{owner="<owner>"}[30d]) == 1
	// Use last_over_time to query historical data within retention period
	query := fmt.Sprintf(`last_over_time(vm_rule_anchor{owner="%s"}[30d]) == 1`, owner)

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	// Group VMs by rule_uuid
	ruleMap := make(map[string][]VMInstance)
	for _, r := range result.Data.Result {
		ruleUUID := r.Metric["rule_uuid"]
		vm := VMInstance{
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Owner:      r.Metric["owner"],
		}
		ruleMap[ruleUUID] = append(ruleMap[ruleUUID], vm)
	}

	log.Printf("Queried %d rules with VMs for owner %s", len(ruleMap), owner)
	return ruleMap, nil
}

// QueryAllAnchorLinks queries VictoriaMetrics for all VMs with anchor value=1
func (am *AnchorManager) QueryAllAnchorLinks(ctx context.Context) (map[string][]VMInstance, error) {
	// Query all active anchors within retention period
	// Use last_over_time to query historical data within 30 days
	query := `last_over_time(vm_rule_anchor[30d]) == 1`

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	// Group VMs by rule_uuid
	ruleMap := make(map[string][]VMInstance)
	for _, r := range result.Data.Result {
		ruleUUID := r.Metric["rule_uuid"]
		vm := VMInstance{
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Owner:      r.Metric["owner"],
		}
		ruleMap[ruleUUID] = append(ruleMap[ruleUUID], vm)
	}

	log.Printf("Queried all anchor links: %d rules with VMs", len(ruleMap))
	return ruleMap, nil
}

// buildAnchorMetrics constructs Prometheus exposition format metrics
func (am *AnchorManager) buildAnchorMetrics(ruleUUID string, vms []VMInstance, value int) string {
	var buf bytes.Buffer

	timestamp := time.Now().UnixMilli()

	for _, vm := range vms {
		// Metric format: vm_rule_anchor{rule_uuid="...",owner="...",region="...",domain="...",instance_id="..."} <value> <timestamp>
		buf.WriteString(fmt.Sprintf(
			`vm_rule_anchor{rule_uuid="%s",owner="%s",region="%s",domain="%s",instance_id="%s"} %d %d`,
			ruleUUID, vm.Owner, vm.Region, vm.Domain, vm.InstanceID, value, timestamp,
		))
		buf.WriteString("\n")
	}

	return buf.String()
}

// importToVM sends metrics to VictoriaMetrics Import API
func (am *AnchorManager) importToVM(ctx context.Context, metrics string) error {
	importURL := fmt.Sprintf("%s/api/v1/import/prometheus", am.vmImportURL)

	req, err := http.NewRequestWithContext(ctx, "POST", importURL, strings.NewReader(metrics))
	if err != nil {
		return fmt.Errorf("failed to create import request: %v", err)
	}

	req.Header.Set("Content-Type", "text/plain")

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("import request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("import failed with status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Successfully imported anchor metrics to VictoriaMetrics")
	return nil
}

// ============================================================================
// Typed Anchor Methods - Per-Rule Threshold Anchors
// Metric names: vm_cpu_anchor / vm_mem_anchor / vm_bw_in_anchor / vm_bw_out_anchor
// Value encodes the threshold (> 0 means active, 0 means unbound)
// Labels: rule_uuid, region, domain, instance_id, owner
// ============================================================================

// AnchorInstance represents a VM instance with its threshold for typed anchor metrics.
type AnchorInstance struct {
	RuleUUID   string
	Region     string
	Domain     string
	InstanceID string
	Interface  string // 网卡接口名，带宽 anchor 使用
	Owner      string
	TenantID   string // 租户UUID，写入 tenant_id label
	Threshold  float64
}

// AnchorResult represents a queried typed anchor entry returned by QueryAnchorsByType.
type AnchorResult struct {
	RuleUUID   string
	Region     string
	Domain     string
	InstanceID string
	Interface  string // 网卡接口名，从 interface label 读取
	Owner      string
	TenantID   string // 租户UUID，从 tenant_id label 读取
	Threshold  float64
}

// WriteAnchorThresholdBatch writes typed anchor metrics with per-instance threshold values.
// anchorType: "cpu", "mem", "bw_in", or "bw_out"
// Metric format: vm_cpu_anchor{rule_uuid="...",region="...",domain="...",instance_id="...",owner="..."} <threshold> <timestamp>
func (am *AnchorManager) WriteAnchorThresholdBatch(ctx context.Context, anchorType string, instances []AnchorInstance) error {
	if len(instances) == 0 {
		return nil
	}
	metrics := am.buildThresholdAnchorMetrics(anchorType, instances)
	log.Printf("[AnchorManager] Writing %d %s anchor thresholds", len(instances), anchorType)
	return am.importToVM(ctx, metrics)
}

// ClearAnchorThresholds writes value=0 to typed anchor metrics to effectively unbind the VMs.
// anchorType: "cpu", "mem", "bw_in", or "bw_out"
func (am *AnchorManager) ClearAnchorThresholds(ctx context.Context, anchorType string, instances []AnchorInstance) error {
	if len(instances) == 0 {
		return nil
	}
	metrics := am.buildClearAnchorMetrics(anchorType, instances)
	log.Printf("[AnchorManager] Clearing %d %s anchor thresholds", len(instances), anchorType)
	return am.importToVM(ctx, metrics)
}

// DeleteAnchorSeries physically deletes anchor series from VictoriaMetrics using the delete_series API.
// This is used by UnlinkVMsFromRule to ensure clean removal without leaving stale value=0 data points.
//
// anchorType: "cpu", "mem", "bw_in", or "bw_out"
// ruleUUID:   the rule whose anchor series to delete
// instanceID: the VM instance UUID
// interfaceName: non-empty for bandwidth rules to target a specific NIC;
//
//	empty string deletes all NIC series for the given instance+rule (used when caller omits interface).
func (am *AnchorManager) DeleteAnchorSeries(ctx context.Context, anchorType, ruleUUID, instanceID, interfaceName string) error {
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	var matcher string
	if interfaceName != "" {
		// Bandwidth: target a specific NIC
		matcher = fmt.Sprintf(`%s{rule_uuid="%s",instance_id="%s",target_device="%s"}`,
			metricName, ruleUUID, instanceID, interfaceName)
	} else {
		// cpu/memory: one series per instance; or bandwidth bulk-delete all NICs
		matcher = fmt.Sprintf(`%s{rule_uuid="%s",instance_id="%s"}`,
			metricName, ruleUUID, instanceID)
	}

	deleteURL := fmt.Sprintf("%s/api/v1/admin/tsdb/delete_series", am.vmDeleteURL)
	body := url.Values{}
	body.Set("match[]", matcher)

	req, err := http.NewRequestWithContext(ctx, "POST", deleteURL, strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create delete request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete request failed: %v", err)
	}
	defer resp.Body.Close()

	// VictoriaMetrics returns 204 No Content on success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete_series failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[AnchorManager] DeleteAnchorSeries: deleted %s matcher=%s", anchorType, matcher)
	return nil
}

// QueryAnchorsByType queries VictoriaMetrics for all active (value > 0) anchors of a given type.
// anchorType: "cpu", "mem", "bw_in", or "bw_out"
// owner: filter by owner label; pass "" to query all owners.
func (am *AnchorManager) QueryAnchorsByType(ctx context.Context, anchorType, owner string) ([]AnchorResult, error) {
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	var query string
	if owner != "" {
		query = fmt.Sprintf(`last_over_time(%s{owner="%s"}[7d]) > 0`, metricName, owner)
	} else {
		query = fmt.Sprintf(`last_over_time(%s[7d]) > 0`, metricName)
	}

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	anchors := make([]AnchorResult, 0, len(result.Data.Result))
	for _, r := range result.Data.Result {
		threshold := 0.0
		if len(r.Value) >= 2 {
			if valStr, ok := r.Value[1].(string); ok {
				threshold, _ = strconv.ParseFloat(valStr, 64)
			}
		}
		anchors = append(anchors, AnchorResult{
			RuleUUID:   r.Metric["rule_uuid"],
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Interface:  r.Metric["target_device"], // 带宽 anchor 有此 label（target_device），其他类型为空字符串
			Owner:      r.Metric["owner"],
			TenantID:   r.Metric["tenant_id"],
			Threshold:  threshold,
		})
	}

	log.Printf("[AnchorManager] Queried %d %s anchors (owner=%q)", len(anchors), anchorType, owner)
	return anchors, nil
}

// QueryTypedAnchorLinksByRule 查询特定类型 anchor 中绑定到指定 rule_uuid 的所有活跃记录（value > 0）。
// anchorType: "cpu", "mem", "bw_in", or "bw_out"
func (am *AnchorManager) QueryTypedAnchorLinksByRule(ctx context.Context, anchorType, ruleUUID string) ([]AnchorResult, error) {
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	query := fmt.Sprintf(`last_over_time(%s{rule_uuid="%s"}[7d]) > 0`, metricName, ruleUUID)

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	anchors := make([]AnchorResult, 0, len(result.Data.Result))
	for _, r := range result.Data.Result {
		threshold := 0.0
		if len(r.Value) >= 2 {
			if valStr, ok := r.Value[1].(string); ok {
				threshold, _ = strconv.ParseFloat(valStr, 64)
			}
		}
		anchors = append(anchors, AnchorResult{
			RuleUUID:   r.Metric["rule_uuid"],
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Interface:  r.Metric["target_device"],
			Owner:      r.Metric["owner"],
			TenantID:   r.Metric["tenant_id"],
			Threshold:  threshold,
		})
	}

	log.Printf("[AnchorManager] QueryTypedAnchorLinksByRule: anchorType=%s ruleUUID=%s found=%d", anchorType, ruleUUID, len(anchors))
	return anchors, nil
}

// QueryAnchorByInstance 查询特定 rule_uuid + instance_id 的 anchor 完整 label 集合，返回所有匹配 series。
// interfaceName：非空时精确匹配 target_device（带宽单 NIC unlink）；空字符串匹配该 instance 的所有 NIC。
// 使用 last_over_time[14d] 不加 > 0，以便也能查到 value=0 的已清除记录（保证幂等性）。
func (am *AnchorManager) QueryAnchorByInstance(ctx context.Context, anchorType, ruleUUID, instanceID, interfaceName string) ([]AnchorResult, error) {
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	var query string
	if interfaceName != "" {
		// 带宽规则：精确匹配指定网卡
		query = fmt.Sprintf(`last_over_time(%s{rule_uuid="%s",instance_id="%s",target_device="%s"}[14d])`,
			metricName, ruleUUID, instanceID, interfaceName)
	} else {
		// 非带宽规则，或带宽规则批量清除所有 NIC
		query = fmt.Sprintf(`last_over_time(%s{rule_uuid="%s",instance_id="%s"}[14d])`,
			metricName, ruleUUID, instanceID)
	}

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	if len(result.Data.Result) == 0 {
		return nil, fmt.Errorf("no anchor found for rule_uuid=%s instance_id=%s interface=%q", ruleUUID, instanceID, interfaceName)
	}

	anchors := make([]AnchorResult, 0, len(result.Data.Result))
	for _, r := range result.Data.Result {
		threshold := 0.0
		if len(r.Value) >= 2 {
			if valStr, ok := r.Value[1].(string); ok {
				threshold, _ = strconv.ParseFloat(valStr, 64)
			}
		}
		anchors = append(anchors, AnchorResult{
			RuleUUID:   r.Metric["rule_uuid"],
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Interface:  r.Metric["target_device"],
			Owner:      r.Metric["owner"],
			TenantID:   r.Metric["tenant_id"],
			Threshold:  threshold,
		})
	}

	log.Printf("[AnchorManager] QueryAnchorByInstance: anchorType=%s ruleUUID=%s instanceID=%s interface=%q found=%d",
		anchorType, ruleUUID, instanceID, interfaceName, len(anchors))
	return anchors, nil
}

// QueryTypedAnchorLinksByOwner 查询特定类型 anchor 中属于指定 owner 的所有活跃记录，按 rule_uuid 分组。
func (am *AnchorManager) QueryTypedAnchorLinksByOwner(ctx context.Context, anchorType, owner string) (map[string][]AnchorResult, error) {
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	query := fmt.Sprintf(`last_over_time(%s{owner="%s"}[7d]) > 0`, metricName, owner)

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %v", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query response: %v", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	ruleMap := make(map[string][]AnchorResult)
	for _, r := range result.Data.Result {
		threshold := 0.0
		if len(r.Value) >= 2 {
			if valStr, ok := r.Value[1].(string); ok {
				threshold, _ = strconv.ParseFloat(valStr, 64)
			}
		}
		ruleUUID := r.Metric["rule_uuid"]
		ruleMap[ruleUUID] = append(ruleMap[ruleUUID], AnchorResult{
			RuleUUID:   ruleUUID,
			Region:     r.Metric["region"],
			Domain:     r.Metric["domain"],
			InstanceID: r.Metric["instance_id"],
			Interface:  r.Metric["target_device"],
			Owner:      r.Metric["owner"],
			TenantID:   r.Metric["tenant_id"],
			Threshold:  threshold,
		})
	}

	log.Printf("[AnchorManager] QueryTypedAnchorLinksByOwner: anchorType=%s owner=%s rules=%d", anchorType, owner, len(ruleMap))
	return ruleMap, nil
}

// QueryAllTypedAnchorLinks 查询所有类型所有活跃 anchor，按 rule_uuid 分组。
func (am *AnchorManager) QueryAllTypedAnchorLinks(ctx context.Context) (map[string][]AnchorResult, error) {
	allAnchorTypes := []string{"cpu", "mem", "bw_in", "bw_out"}
	ruleMap := make(map[string][]AnchorResult)

	for _, anchorType := range allAnchorTypes {
		metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
		query := fmt.Sprintf(`last_over_time(%s[7d]) > 0`, metricName)
		queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
		if err != nil {
			log.Printf("[AnchorManager] QueryAllTypedAnchorLinks: failed to create request for %s: %v", anchorType, err)
			continue
		}

		resp, err := am.httpClient.Do(req)
		if err != nil {
			log.Printf("[AnchorManager] QueryAllTypedAnchorLinks: request failed for %s: %v", anchorType, err)
			continue
		}

		var result struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []struct {
					Metric map[string]string `json:"metric"`
					Value  []interface{}     `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			log.Printf("[AnchorManager] QueryAllTypedAnchorLinks: decode failed for %s: %v", anchorType, err)
			continue
		}
		resp.Body.Close()

		if result.Status != "success" {
			continue
		}

		for _, r := range result.Data.Result {
			threshold := 0.0
			if len(r.Value) >= 2 {
				if valStr, ok := r.Value[1].(string); ok {
					threshold, _ = strconv.ParseFloat(valStr, 64)
				}
			}
			ruleUUID := r.Metric["rule_uuid"]
			ruleMap[ruleUUID] = append(ruleMap[ruleUUID], AnchorResult{
				RuleUUID:   ruleUUID,
				Region:     r.Metric["region"],
				Domain:     r.Metric["domain"],
				InstanceID: r.Metric["instance_id"],
				Interface:  r.Metric["target_device"],
				Owner:      r.Metric["owner"],
				TenantID:   r.Metric["tenant_id"],
				Threshold:  threshold,
			})
		}
	}

	log.Printf("[AnchorManager] QueryAllTypedAnchorLinks: total rules=%d", len(ruleMap))
	return ruleMap, nil
}

// QueryStaleAnchors queries anchor series that are active within [14d] but have NOT been
// refreshed within [7d]. These anchors risk expiring from the last_over_time window used
// in alert PromQL expressions and need to be re-imported with a fresh timestamp.
// Returns map[anchorType][]AnchorResult; partial results + first error on failure.
func (am *AnchorManager) QueryStaleAnchors(ctx context.Context) (map[string][]AnchorResult, error) {
	allAnchorTypes := []string{"cpu", "mem", "bw_in", "bw_out"}
	result := make(map[string][]AnchorResult)
	var firstErr error

	for _, anchorType := range allAnchorTypes {
		metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
		// Anchors present in [14d] but absent in [7d] are stale and need refresh
		query := fmt.Sprintf(
			`last_over_time(%s[14d]) > 0 unless last_over_time(%s[7d]) > 0`,
			metricName, metricName,
		)
		queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
		if err != nil {
			log.Printf("[AnchorManager] QueryStaleAnchors: failed to create request for %s: %v", anchorType, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		resp, err := am.httpClient.Do(req)
		if err != nil {
			log.Printf("[AnchorManager] QueryStaleAnchors: request failed for %s: %v", anchorType, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		var vmResult struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []struct {
					Metric map[string]string `json:"metric"`
					Value  []interface{}     `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&vmResult); err != nil {
			resp.Body.Close()
			log.Printf("[AnchorManager] QueryStaleAnchors: decode failed for %s: %v", anchorType, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resp.Body.Close()

		if vmResult.Status != "success" {
			log.Printf("[AnchorManager] QueryStaleAnchors: non-success for %s: %s", anchorType, vmResult.Status)
			continue
		}

		anchors := make([]AnchorResult, 0, len(vmResult.Data.Result))
		for _, r := range vmResult.Data.Result {
			threshold := 0.0
			if len(r.Value) >= 2 {
				if valStr, ok := r.Value[1].(string); ok {
					threshold, _ = strconv.ParseFloat(valStr, 64)
				}
			}
			anchors = append(anchors, AnchorResult{
				RuleUUID:   r.Metric["rule_uuid"],
				Region:     r.Metric["region"],
				Domain:     r.Metric["domain"],
				InstanceID: r.Metric["instance_id"],
				Interface:  r.Metric["target_device"],
				Owner:      r.Metric["owner"],
				TenantID:   r.Metric["tenant_id"],
				Threshold:  threshold,
			})
		}

		result[anchorType] = anchors
		log.Printf("[AnchorManager] QueryStaleAnchors: anchorType=%s stale=%d", anchorType, len(anchors))
	}

	return result, firstErr
}

// SyncStaleAnchorsBatch re-imports stale anchor series with a fresh timestamp to keep them
// within the last_over_time window used in alert PromQL expressions.
// batchSize: number of series per import request; sleepMs: pause between batches.
// Returns map[anchorType]refreshed_count and first error encountered (non-fatal).
func (am *AnchorManager) SyncStaleAnchorsBatch(ctx context.Context, staleAnchors map[string][]AnchorResult, batchSize int, sleepMs int) (map[string]int, error) {
	refreshed := make(map[string]int)
	var firstErr error

	for anchorType, results := range staleAnchors {
		if len(results) == 0 {
			continue
		}

		// Convert AnchorResult → AnchorInstance, preserving all labels and threshold
		instances := make([]AnchorInstance, len(results))
		for i, r := range results {
			instances[i] = AnchorInstance{
				RuleUUID:   r.RuleUUID,
				Region:     r.Region,
				Domain:     r.Domain,
				InstanceID: r.InstanceID,
				Interface:  r.Interface,
				Owner:      r.Owner,
				TenantID:   r.TenantID,
				Threshold:  r.Threshold,
			}
		}

		// Process in batches to avoid write spikes on VictoriaMetrics
		count := 0
		for start := 0; start < len(instances); start += batchSize {
			end := start + batchSize
			if end > len(instances) {
				end = len(instances)
			}
			batch := instances[start:end]

			if err := am.WriteAnchorThresholdBatch(ctx, anchorType, batch); err != nil {
				log.Printf("[AnchorManager] SyncStaleAnchorsBatch: batch write failed for %s (offset=%d): %v", anchorType, start, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			count += len(batch)

			// Sleep between batches (skip after last batch)
			if end < len(instances) && sleepMs > 0 {
				time.Sleep(time.Duration(sleepMs) * time.Millisecond)
			}
		}

		refreshed[anchorType] = count
		log.Printf("[AnchorManager] SyncStaleAnchorsBatch: anchorType=%s refreshed=%d/%d", anchorType, count, len(instances))
	}

	return refreshed, firstErr
}

// buildThresholdAnchorMetrics builds Prometheus exposition format for typed anchor metrics.
// Each AnchorInstance carries its own threshold value.
// Bandwidth anchors (bw_in / bw_out) include an extra `target_device` label for per-NIC alerting.
// alert_type is NOT stored in anchor labels; it is injected via label_replace() in each PromQL template.
func (am *AnchorManager) buildThresholdAnchorMetrics(anchorType string, instances []AnchorInstance) string {
	var buf bytes.Buffer
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	timestamp := time.Now().UnixMilli()
	isBandwidth := anchorType == "bw_in" || anchorType == "bw_out"

	for _, inst := range instances {
		var line string
		if isBandwidth {
			line = fmt.Sprintf(
				`%s{rule_uuid="%s",region="%s",domain="%s",instance_id="%s",target_device="%s",owner="%s",tenant_id="%s"} %g %d`,
				metricName, inst.RuleUUID, inst.Region, inst.Domain, inst.InstanceID, inst.Interface, inst.Owner, inst.TenantID, inst.Threshold, timestamp,
			)
		} else {
			line = fmt.Sprintf(
				`%s{rule_uuid="%s",region="%s",domain="%s",instance_id="%s",owner="%s",tenant_id="%s"} %g %d`,
				metricName, inst.RuleUUID, inst.Region, inst.Domain, inst.InstanceID, inst.Owner, inst.TenantID, inst.Threshold, timestamp,
			)
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// buildClearAnchorMetrics builds Prometheus exposition format with value=0 to clear anchors.
// Labels must exactly mirror buildThresholdAnchorMetrics so VictoriaMetrics overwrites the same series.
func (am *AnchorManager) buildClearAnchorMetrics(anchorType string, instances []AnchorInstance) string {
	var buf bytes.Buffer
	metricName := fmt.Sprintf("vm_%s_anchor", anchorType)
	timestamp := time.Now().UnixMilli()
	isBandwidth := anchorType == "bw_in" || anchorType == "bw_out"

	for _, inst := range instances {
		var line string
		if isBandwidth {
			line = fmt.Sprintf(
				`%s{rule_uuid="%s",region="%s",domain="%s",instance_id="%s",target_device="%s",owner="%s",tenant_id="%s"} 0 %d`,
				metricName, inst.RuleUUID, inst.Region, inst.Domain, inst.InstanceID, inst.Interface, inst.Owner, inst.TenantID, timestamp,
			)
		} else {
			line = fmt.Sprintf(
				`%s{rule_uuid="%s",region="%s",domain="%s",instance_id="%s",owner="%s",tenant_id="%s"} 0 %d`,
				metricName, inst.RuleUUID, inst.Region, inst.Domain, inst.InstanceID, inst.Owner, inst.TenantID, timestamp,
			)
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// ============================================================================
// Template Renderer - N9E PromQL template rendering
// ============================================================================

// N9ETemplateRenderer handles rendering N9E PromQL templates
type N9ETemplateRenderer struct {
	templatePath string
}

// NewN9ETemplateRenderer creates a new template renderer
func NewN9ETemplateRenderer(templatePath string) *N9ETemplateRenderer {
	return &N9ETemplateRenderer{
		templatePath: templatePath,
	}
}

// RenderN9EPromQL renders a N9E PromQL template with given parameters
// ruleType: "cpu", "memory", "bandwidth", "bandwidth-in", or "bandwidth-out"
// ruleUUID: unique identifier for the rule
// params: map of template variables (duration, operator, threshold)
func (r *N9ETemplateRenderer) RenderN9EPromQL(ruleType, ruleUUID string, params map[string]interface{}) (string, error) {
	// Determine template file based on rule type
	var templateFile string
	switch ruleType {
	case "cpu":
		templateFile = "N9E-cpu-promql.j2"
	case "memory":
		templateFile = "N9E-memory-promql.j2"
	case "bandwidth-in":
		templateFile = "N9E-bandwidth-in-promql.j2"
	case "bandwidth-out":
		templateFile = "N9E-bandwidth-out-promql.j2"
	default:
		return "", fmt.Errorf("unsupported rule type: %s", ruleType)
	}

	templateFilePath := filepath.Join(r.templatePath, templateFile)

	// Check if template file exists
	if _, err := os.Stat(templateFilePath); os.IsNotExist(err) {
		return "", fmt.Errorf("template file not found: %s", templateFilePath)
	}

	// Read template content
	tmplContent, err := os.ReadFile(templateFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read template file: %v", err)
	}

	// Add rule_uuid to params
	if params == nil {
		params = make(map[string]interface{})
	}
	params["rule_uuid"] = ruleUUID

	// Parse and execute template (using Jinja2 syntax, but Go's template engine with {{ }})
	// Since the templates use Jinja2 syntax, we need to convert or use ProcessTemplate if available
	tmpl, err := template.New(templateFile).Parse(string(tmplContent))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute template: %v", err)
	}

	// Clean and normalize the PromQL string
	promql := cleanPromQL(buf.String())

	// Validate the rendered PromQL
	if err := validatePromQL(promql); err != nil {
		return "", fmt.Errorf("invalid rendered PromQL: %v", err)
	}

	log.Printf("Rendered N9E PromQL for rule type: %s, UUID: %s, length: %d bytes", ruleType, ruleUUID, len(promql))

	return promql, nil
}

// cleanPromQL removes excessive whitespace and normalizes the PromQL string
// This ensures the PromQL is compact and compatible with N9E API
func cleanPromQL(promql string) string {
	// Remove leading/trailing whitespace
	promql = strings.TrimSpace(promql)

	// Replace all sequences of whitespace (including newlines) with a single space
	re := regexp.MustCompile(`\s+`)
	promql = re.ReplaceAllString(promql, " ")

	// Remove spaces around operators and parentheses for compactness
	promql = strings.ReplaceAll(promql, " (", "(")
	promql = strings.ReplaceAll(promql, "( ", "(")
	promql = strings.ReplaceAll(promql, " )", ")")
	promql = strings.ReplaceAll(promql, ") ", ")")
	promql = strings.ReplaceAll(promql, " ,", ",")
	promql = strings.ReplaceAll(promql, ", ", ",")

	// Preserve necessary spaces around keywords and operators
	// Add space after commas in function calls
	promql = strings.ReplaceAll(promql, ",", ", ")

	// Preserve spaces around comparison operators
	for _, op := range []string{"==", "!=", "<=", ">=", ">", "<"} {
		promql = strings.ReplaceAll(promql, op, " "+op+" ")
	}

	// Preserve spaces around logical operators
	for _, op := range []string{" AND ", " OR ", " UNLESS "} {
		promql = strings.ReplaceAll(promql, strings.TrimSpace(op), op)
	}

	// Clean up any double spaces created by the replacements
	re = regexp.MustCompile(`\s{2,}`)
	promql = re.ReplaceAllString(promql, " ")

	return strings.TrimSpace(promql)
}

// validatePromQL performs basic validation on the rendered PromQL
func validatePromQL(promql string) error {
	if len(promql) == 0 {
		return fmt.Errorf("empty PromQL string")
	}

	// Check for balanced parentheses
	openCount := strings.Count(promql, "(")
	closeCount := strings.Count(promql, ")")
	if openCount != closeCount {
		return fmt.Errorf("unbalanced parentheses: %d open, %d close", openCount, closeCount)
	}

	// Check for balanced curly braces (label matchers)
	openBraces := strings.Count(promql, "{")
	closeBraces := strings.Count(promql, "}")
	if openBraces != closeBraces {
		return fmt.Errorf("unbalanced curly braces: %d open, %d close", openBraces, closeBraces)
	}

	// Check for balanced square brackets (time ranges)
	openBrackets := strings.Count(promql, "[")
	closeBrackets := strings.Count(promql, "]")
	if openBrackets != closeBrackets {
		return fmt.Errorf("unbalanced square brackets: %d open, %d close", openBrackets, closeBrackets)
	}

	// Check for required anchor metric reference (vm_cpu_anchor, vm_mem_anchor, etc.)
	if !strings.Contains(promql, "_anchor") {
		return fmt.Errorf("missing required anchor metric reference (vm_TYPE_anchor)")
	}

	// Check for rule_uuid label
	if !strings.Contains(promql, "rule_uuid=") {
		return fmt.Errorf("missing required rule_uuid label in anchor metric")
	}

	return nil
}

// ValidateTemplateParams validates common template parameters
func ValidateTemplateParams(ruleType string, params map[string]interface{}) error {
	// Check required common parameters
	if _, ok := params["duration"]; !ok {
		return fmt.Errorf("missing required parameter: duration")
	}
	if _, ok := params["operator"]; !ok {
		return fmt.Errorf("missing required parameter: operator")
	}
	// Note: threshold is no longer a template parameter.
	// The threshold value is encoded in the anchor metric (vm_TYPE_anchor) written by LinkVMsToRule.

	// Validate operator
	operator, ok := params["operator"].(string)
	if !ok {
		return fmt.Errorf("operator must be a string")
	}
	validOperators := map[string]bool{">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true}
	if !validOperators[operator] {
		return fmt.Errorf("invalid operator: %s (must be one of: >, >=, <, <=, ==, !=)", operator)
	}

	// Validate bandwidth-specific parameters
	if ruleType == "bandwidth" {
		if _, ok := params["direction"]; !ok {
			return fmt.Errorf("missing required parameter for bandwidth: direction")
		}
		direction, ok := params["direction"].(string)
		if !ok {
			return fmt.Errorf("direction must be a string")
		}
		if direction != "receive" && direction != "transmit" {
			return fmt.Errorf("invalid direction: %s (must be 'receive' or 'transmit')", direction)
		}
	}

	return nil
}

// ============================================================================
// Database Operations - Rule Group N9ERuleID updates
// ============================================================================

// UpdateRuleGroupN9ERuleID updates the N9ERuleID field of a RuleGroupV2
// This is used to store N9E rule IDs after rule creation
func (a *AlarmOperator) UpdateRuleGroupN9ERuleID(ctx context.Context, groupUUID, n9eRuleID string) error {
	ctx, db := common.GetContextDB(ctx)

	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.RuleGroupV2{}).
			Where("uuid = ?", groupUUID).
			Update("n9e_rule_id", n9eRuleID)

		if result.Error != nil {
			log.Printf("Failed to update N9ERuleID for group %s: %v", groupUUID, result.Error)
			return fmt.Errorf("failed to update N9ERuleID: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf("rule group not found: %s", groupUUID)
		}

		log.Printf("Updated N9ERuleID=%s for group UUID=%s", n9eRuleID, groupUUID)
		return nil
	})
}

// ============================================================================
// N9EOperator - Database operations for N9E-specific tables
// ============================================================================

// N9EOperator handles database operations for N9E-specific tables
type N9EOperator struct {
	DB *gorm.DB
}

// ============================================================================
// Business Group Operations
// ============================================================================

// CreateBusinessGroup creates a new N9E business group record
func (o *N9EOperator) CreateBusinessGroup(ctx context.Context, group *model.N9EBusinessGroup) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Create(group).Error
}

// GetBusinessGroupByUUID retrieves a business group by UUID
func (o *N9EOperator) GetBusinessGroupByUUID(ctx context.Context, uuid string) (*model.N9EBusinessGroup, error) {
	ctx, db := common.GetContextDB(ctx)
	var group model.N9EBusinessGroup
	err := db.Where("uuid = ?", uuid).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// GetBusinessGroupByName retrieves a business group by name
func (o *N9EOperator) GetBusinessGroupByName(ctx context.Context, name string) (*model.N9EBusinessGroup, error) {
	ctx, db := common.GetContextDB(ctx)
	var group model.N9EBusinessGroup
	err := db.Where("name = ? AND deleted_at IS NULL", name).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// UpdateBusinessGroupN9EID updates the N9E business group ID
func (o *N9EOperator) UpdateBusinessGroupN9EID(ctx context.Context, uuid string, n9eBusinessGroupID int64) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.N9EBusinessGroup{}).
		Where("uuid = ?", uuid).
		Update("n9e_business_group_id", n9eBusinessGroupID).Error
}

// DeleteBusinessGroup soft deletes a business group
func (o *N9EOperator) DeleteBusinessGroup(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("uuid = ?", uuid).Delete(&model.N9EBusinessGroup{}).Error
}

// CountRulesByBusinessGroupUUID counts all rules (CPU/Memory/BW) for a business group
func (o *N9EOperator) CountRulesByBusinessGroupUUID(ctx context.Context, businessGroupUUID string) (int, error) {
	ctx, db := common.GetContextDB(ctx)

	var cpuCount, memoryCount, bwCount int64

	if err := db.Model(&model.N9ECPURule{}).Where("business_group_uuid = ?", businessGroupUUID).Count(&cpuCount).Error; err != nil {
		return 0, err
	}

	if err := db.Model(&model.N9EMemoryRule{}).Where("business_group_uuid = ?", businessGroupUUID).Count(&memoryCount).Error; err != nil {
		return 0, err
	}

	if err := db.Model(&model.N9EBandwidthRule{}).Where("business_group_uuid = ?", businessGroupUUID).Count(&bwCount).Error; err != nil {
		return 0, err
	}

	return int(cpuCount + memoryCount + bwCount), nil
}

// ============================================================================
// CPU Rule Operations
// ============================================================================

// CreateCPURule creates a new N9E CPU rule record
func (o *N9EOperator) CreateCPURule(ctx context.Context, rule *model.N9ECPURule) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Create(rule).Error
}

// GetCPURuleByUUID retrieves a CPU rule by UUID
func (o *N9EOperator) GetCPURuleByUUID(ctx context.Context, uuid string) (*model.N9ECPURule, error) {
	ctx, db := common.GetContextDB(ctx)
	var rule model.N9ECPURule
	err := db.Where("rule_id = ?", uuid).First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// ListCPURules lists CPU rules with pagination and filtering
func (o *N9EOperator) ListCPURules(ctx context.Context, params ListN9ERulesParams) ([]model.N9ECPURule, int, error) {
	ctx, db := common.GetContextDB(ctx)

	query := db.Model(&model.N9ECPURule{})

	if params.BusinessGroupUUID != "" {
		query = query.Where("business_group_uuid = ?", params.BusinessGroupUUID)
	}
	if params.Owner != "" {
		query = query.Where("owner = ?", params.Owner)
	}
	if params.UUID != "" {
		query = query.Where("uuid = ?", params.UUID)
	}

	var total int
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rules []model.N9ECPURule
	offset := (params.Page - 1) * params.PageSize
	err := query.Offset(offset).Limit(params.PageSize).
		Order("created_at DESC").
		Find(&rules).Error

	return rules, total, err
}

// UpdateCPURuleN9EID updates the N9E alert rule ID
func (o *N9EOperator) UpdateCPURuleN9EID(ctx context.Context, ruleID string, n9eAlertRuleID int64) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.N9ECPURule{}).
		Where("rule_id = ?", ruleID).
		Update("n9e_alert_rule_id", n9eAlertRuleID).Error
}

// DeleteCPURule soft deletes a CPU rule
func (o *N9EOperator) DeleteCPURule(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("rule_id = ?", uuid).Delete(&model.N9ECPURule{}).Error
}

// ============================================================================
// Memory Rule Operations
// ============================================================================

// CreateMemoryRule creates a new N9E memory rule record
func (o *N9EOperator) CreateMemoryRule(ctx context.Context, rule *model.N9EMemoryRule) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Create(rule).Error
}

// GetMemoryRuleByUUID retrieves a memory rule by UUID
func (o *N9EOperator) GetMemoryRuleByUUID(ctx context.Context, uuid string) (*model.N9EMemoryRule, error) {
	ctx, db := common.GetContextDB(ctx)
	var rule model.N9EMemoryRule
	err := db.Where("rule_id = ?", uuid).First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// ListMemoryRules lists memory rules with pagination and filtering
func (o *N9EOperator) ListMemoryRules(ctx context.Context, params ListN9ERulesParams) ([]model.N9EMemoryRule, int, error) {
	ctx, db := common.GetContextDB(ctx)

	query := db.Model(&model.N9EMemoryRule{})

	if params.BusinessGroupUUID != "" {
		query = query.Where("business_group_uuid = ?", params.BusinessGroupUUID)
	}
	if params.Owner != "" {
		query = query.Where("owner = ?", params.Owner)
	}
	if params.UUID != "" {
		query = query.Where("uuid = ?", params.UUID)
	}

	var total int
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rules []model.N9EMemoryRule
	offset := (params.Page - 1) * params.PageSize
	err := query.Offset(offset).Limit(params.PageSize).
		Order("created_at DESC").
		Find(&rules).Error

	return rules, total, err
}

// UpdateMemoryRuleN9EID updates the N9E alert rule ID
func (o *N9EOperator) UpdateMemoryRuleN9EID(ctx context.Context, ruleID string, n9eAlertRuleID int64) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.N9EMemoryRule{}).
		Where("rule_id = ?", ruleID).
		Update("n9e_alert_rule_id", n9eAlertRuleID).Error
}

// DeleteMemoryRule soft deletes a memory rule
func (o *N9EOperator) DeleteMemoryRule(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("rule_id = ?", uuid).Delete(&model.N9EMemoryRule{}).Error
}

// ============================================================================
// Bandwidth Rule Operations
// ============================================================================

// CreateBandwidthRule creates a new N9E bandwidth rule record
func (o *N9EOperator) CreateBandwidthRule(ctx context.Context, rule *model.N9EBandwidthRule) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Create(rule).Error
}

// GetBandwidthRuleByUUID retrieves a bandwidth rule by UUID
func (o *N9EOperator) GetBandwidthRuleByUUID(ctx context.Context, uuid string) (*model.N9EBandwidthRule, error) {
	ctx, db := common.GetContextDB(ctx)
	var rule model.N9EBandwidthRule
	err := db.Where("rule_id = ?", uuid).First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// ListBandwidthRules lists bandwidth rules with pagination and filtering
func (o *N9EOperator) ListBandwidthRules(ctx context.Context, params ListN9ERulesParams) ([]model.N9EBandwidthRule, int, error) {
	ctx, db := common.GetContextDB(ctx)

	query := db.Model(&model.N9EBandwidthRule{})

	if params.BusinessGroupUUID != "" {
		query = query.Where("business_group_uuid = ?", params.BusinessGroupUUID)
	}
	if params.Owner != "" {
		query = query.Where("owner = ?", params.Owner)
	}
	if params.UUID != "" {
		query = query.Where("uuid = ?", params.UUID)
	}

	var total int
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rules []model.N9EBandwidthRule
	offset := (params.Page - 1) * params.PageSize
	err := query.Offset(offset).Limit(params.PageSize).
		Order("created_at DESC").
		Find(&rules).Error

	return rules, total, err
}

// UpdateBandwidthRuleN9EID updates the N9E alert rule ID
func (o *N9EOperator) UpdateBandwidthRuleN9EID(ctx context.Context, ruleID string, n9eAlertRuleID int64) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.N9EBandwidthRule{}).
		Where("rule_id = ?", ruleID).
		Update("n9e_alert_rule_id", n9eAlertRuleID).Error
}

// DeleteBandwidthRule soft deletes a bandwidth rule
func (o *N9EOperator) DeleteBandwidthRule(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("rule_id = ?", uuid).Delete(&model.N9EBandwidthRule{}).Error
}

// ============================================================================
// VM Rule Link Operations
// ============================================================================

// CreateVMRuleLink creates a new VM-rule link record
func (o *N9EOperator) CreateVMRuleLink(ctx context.Context, link *model.N9EVMRuleLink) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Create(link).Error
}

// GetVMRuleLinks retrieves all VM links for a rule
func (o *N9EOperator) GetVMRuleLinks(ctx context.Context, ruleUUID string) ([]model.N9EVMRuleLink, error) {
	ctx, db := common.GetContextDB(ctx)
	var links []model.N9EVMRuleLink
	err := db.Where("rule_uuid = ? AND deleted_at IS NULL", ruleUUID).Find(&links).Error
	return links, err
}

// GetVMRuleLinksByOwner retrieves all VM links for an owner
func (o *N9EOperator) GetVMRuleLinksByOwner(ctx context.Context, owner string) ([]model.N9EVMRuleLink, error) {
	ctx, db := common.GetContextDB(ctx)
	var links []model.N9EVMRuleLink
	err := db.Where("owner = ? AND deleted_at IS NULL", owner).Find(&links).Error
	return links, err
}

// GetAllVMRuleLinks retrieves all VM rule links from the database (used for disaster recovery)
func (o *N9EOperator) GetAllVMRuleLinks(ctx context.Context) ([]model.N9EVMRuleLink, error) {
	ctx, db := common.GetContextDB(ctx)
	var links []model.N9EVMRuleLink
	err := db.Find(&links).Error
	return links, err
}

// DeleteVMRuleLink deletes VM-rule links
func (o *N9EOperator) DeleteVMRuleLink(ctx context.Context, ruleUUID, vmUUID, iface string) (int64, error) {
	ctx, db := common.GetContextDB(ctx)

	query := db.Where("rule_uuid = ?", ruleUUID)

	if vmUUID != "" {
		query = query.Where("vm_uuid = ?", vmUUID)
	}
	if iface != "" {
		query = query.Where("interface = ?", iface)
	}

	result := query.Delete(&model.N9EVMRuleLink{})
	return result.RowsAffected, result.Error
}

// CheckVMLinkExists checks if a VM link already exists
func (o *N9EOperator) CheckVMLinkExists(ctx context.Context, ruleUUID, vmUUID, iface string) bool {
	ctx, db := common.GetContextDB(ctx)
	var count int64
	db.Model(&model.N9EVMRuleLink{}).
		Where("rule_uuid = ? AND vm_uuid = ? AND interface = ?", ruleUUID, vmUUID, iface).
		Count(&count)
	return count > 0
}

// ============================================================================
// Complex Queries
// ============================================================================

// GetLinkedVMsByRuleUUID retrieves all linked VMs for a specific rule with JOIN queries
func (o *N9EOperator) GetLinkedVMsByRuleUUID(ctx context.Context, ruleUUID string) ([]map[string]interface{}, error) {
	ctx, db := common.GetContextDB(ctx)

	var results []map[string]interface{}

	// First determine rule type
	var ruleType string
	var cpuRule model.N9ECPURule
	if err := db.Where("uuid = ?", ruleUUID).First(&cpuRule).Error; err == nil {
		ruleType = "cpu"
	} else {
		var memRule model.N9EMemoryRule
		if err := db.Where("uuid = ?", ruleUUID).First(&memRule).Error; err == nil {
			ruleType = "memory"
		} else {
			var bwRule model.N9EBandwidthRule
			if err := db.Where("uuid = ?", ruleUUID).First(&bwRule).Error; err == nil {
				ruleType = "bandwidth"
			} else {
				return nil, fmt.Errorf("rule not found: %s", ruleUUID)
			}
		}
	}

	// Query VM links
	var links []model.N9EVMRuleLink
	if err := db.Where("rule_uuid = ?", ruleUUID).Find(&links).Error; err != nil {
		return nil, err
	}

	// Build result
	for _, link := range links {
		results = append(results, map[string]interface{}{
			"rule_uuid":           link.RuleUUID,
			"vm_uuid":             link.VMUUID,
			"interface":           link.Interface,
			"owner":               link.Owner,
			"rule_type":           ruleType,
			"business_group_uuid": link.BusinessGroupUUID,
		})
	}

	return results, nil
}

// GetLinkedVMsByOwner retrieves all linked VMs for an owner
func (o *N9EOperator) GetLinkedVMsByOwner(ctx context.Context, owner string) ([]map[string]interface{}, error) {
	ctx, db := common.GetContextDB(ctx)

	var results []map[string]interface{}
	var links []model.N9EVMRuleLink

	if err := db.Where("owner = ?", owner).Find(&links).Error; err != nil {
		return nil, err
	}

	for _, link := range links {
		results = append(results, map[string]interface{}{
			"rule_uuid":           link.RuleUUID,
			"rule_type":           link.RuleType,
			"vm_uuid":             link.VMUUID,
			"interface":           link.Interface,
			"owner":               link.Owner,
			"business_group_uuid": link.BusinessGroupUUID,
		})
	}

	return results, nil
}

// ============================================================================
// Parameters
// ============================================================================

// ListN9ERulesParams represents filtering parameters for listing N9E rules
type ListN9ERulesParams struct {
	BusinessGroupUUID string
	Owner             string
	UUID              string
	Page              int
	PageSize          int
}

// SetDefaults sets default values for pagination
func (p *ListN9ERulesParams) SetDefaults() {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
}

// GetInstanceMetadata queries vm_instance_map from VictoriaMetrics to get instance metadata
// Returns: domain, hypervisor, region, error
func (am *AnchorManager) GetInstanceMetadata(ctx context.Context, instanceID string) (domain, hypervisor, region string, err error) {
	query := fmt.Sprintf(`vm_instance_map{instance_id="%s"}`, instanceID)
	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", am.vmQueryURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create query request: %w", err)
	}

	resp, err := am.httpClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to query vm_instance_map: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse VictoriaMetrics JSON response
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", "", fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Status != "success" {
		return "", "", "", fmt.Errorf("query returned non-success status: %s", result.Status)
	}

	if len(result.Data.Result) == 0 {
		return "", "", "", fmt.Errorf("instance %s not found in vm_instance_map", instanceID)
	}

	metric := result.Data.Result[0].Metric
	domain = metric["domain"]
	hypervisor = metric["hypervisor"]

	if domain == "" || hypervisor == "" {
		return "", "", "", fmt.Errorf("incomplete metadata for instance %s", instanceID)
	}

	region = extractRegionFromHypervisor(hypervisor)
	return domain, hypervisor, region, nil
}

// extractRegionFromHypervisor extracts region prefix from hypervisor name
// Example: "sv6-cland-compute-3" -> "sv6"
func extractRegionFromHypervisor(hypervisor string) string {
	parts := strings.Split(hypervisor, "-")
	if len(parts) > 0 {
		return parts[0]
	}
	return hypervisor
}
