/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	macaron "gopkg.in/macaron.v1"
)

// FilterField defines a filter field configuration for list page searches
type FilterField struct {
	// Name is the database column name for filtering
	Name string
	// Label is the UI label displayed for this field
	Label string
	// Type is the field type: 'text', 'select', 'date', 'number'
	Type string
	// Options is the list of available options for 'select' type, empty for other types
	Options []string
	// Placeholder is the placeholder text displayed in the UI input field
	Placeholder string
}

// ListConfig defines the configuration for a list page including available filters and display options
type ListConfig struct {
	// Name is the list name (e.g., 'zones', 'instances', 'hypers')
	Name string
	// FilterFields are the available filter fields for this list
	FilterFields []FilterField
	// DefaultLimit is the default number of items per page
	DefaultLimit int64
	// PageSizes are the available page size options for pagination
	PageSizes []int64
}

// GlobalListConfigs maps list names to their configurations
var GlobalListConfigs = map[string]ListConfig{
	"zones": {
		Name: "zones",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Zone ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Zone ID",
			},
			{
				Name:        "name",
				Label:       "Zone Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Zone Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"hypers": {
		Name: "hypers",
		FilterFields: []FilterField{
			{
				Name:        "hostid",
				Label:       "Host ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Host ID",
			},
			{
				Name:        "hostname",
				Label:       "Hostname",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Hostname",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"instances": {
		Name: "instances",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Instance ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Instance ID",
			},
			{
				Name:        "hostname",
				Label:       "Hostname",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Hostname",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"users": {
		Name: "users",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "User ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter User ID",
			},
			{
				Name:        "username",
				Label:       "Username",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Username",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"orgs": {
		Name: "orgs",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Organization ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Organization ID",
			},
			{
				Name:        "name",
				Label:       "Organization Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Organization Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"flavors": {
		Name: "flavors",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Flavor ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Flavor ID",
			},
			{
				Name:        "name",
				Label:       "Flavor Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Flavor Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"images": {
		Name: "images",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Image ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Image ID",
			},
			{
				Name:        "name",
				Label:       "Image Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Image Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"volumes": {
		Name: "volumes",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Volume ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Volume ID",
			},
			{
				Name:        "name",
				Label:       "Volume Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Volume Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"subnets": {
		Name: "subnets",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Subnet ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Subnet ID",
			},
			{
				Name:        "name",
				Label:       "Subnet Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Subnet Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"ipgroups": {
		Name: "ipgroups",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "IP Group ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter IP Group ID",
			},
			{
				Name:        "name",
				Label:       "IP Group Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter IP Group Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"keys": {
		Name: "keys",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Key ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Key ID",
			},
			{
				Name:        "name",
				Label:       "Key Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Key Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"floatingips": {
		Name: "floatingips",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Floating IP ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Floating IP ID",
			},
			{
				Name:        "address",
				Label:       "IP Address",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter IP Address",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"routers": {
		Name: "routers",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Router ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Router ID",
			},
			{
				Name:        "name",
				Label:       "Router Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Router Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"secgroups": {
		Name: "secgroups",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Security Group ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Security Group ID",
			},
			{
				Name:        "name",
				Label:       "Security Group Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Security Group Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"backups": {
		Name: "backups",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Backup ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Backup ID",
			},
			{
				Name:        "name",
				Label:       "Backup Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Backup Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"tasks": {
		Name: "tasks",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Task ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Task ID",
			},
			{
				Name:        "name",
				Label:       "Task Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Task Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"loadbalancers": {
		Name: "loadbalancers",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Load Balancer ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Load Balancer ID",
			},
			{
				Name:        "name",
				Label:       "Load Balancer Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Load Balancer Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"cgroups": {
		Name: "cgroups",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Consistency Group ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Consistency Group ID",
			},
			{
				Name:        "name",
				Label:       "Consistency Group Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Consistency Group Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"dictionaries": {
		Name: "dictionaries",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Dictionary ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Dictionary ID",
			},
			{
				Name:        "name",
				Label:       "Dictionary Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Dictionary Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"migrations": {
		Name: "migrations",
		FilterFields: []FilterField{
			{
				Name:        "id",
				Label:       "Migration ID",
				Type:        "number",
				Options:     []string{},
				Placeholder: "Enter Migration ID",
			},
			{
				Name:        "name",
				Label:       "Migration Name",
				Type:        "text",
				Options:     []string{},
				Placeholder: "Enter Migration Name",
			},
		},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	},
}

// GetListConfig retrieves the configuration for a given list name
// Returns the config if found, or a default config if not found
func GetListConfig(listName string) ListConfig {
	if config, exists := GlobalListConfigs[listName]; exists {
		return config
	}
	// Return default config if list not found
	return ListConfig{
		Name:         listName,
		FilterFields: []FilterField{},
		DefaultLimit: 20,
		PageSizes:    []int64{10, 20, 50, 100},
	}
}

// GetPaginationParams extracts and validates pagination parameters from the request query string
// It returns the list config, validated offset and limit values
// Parameters: c - macaron context, listName - name of the list configuration
// Returns: listConfig, offset, limit
func GetPaginationParams(c *macaron.Context, listName string) (listConfig ListConfig, offset, limit int64) {
	listConfig = GetListConfig(listName)
	offset = c.QueryInt64("offset")
	limit = c.QueryInt64("limit")

	// Apply default limit if not specified
	if limit == 0 {
		limit = listConfig.DefaultLimit
	}

	// Validate limit against allowed page sizes
	validLimit := false
	for _, size := range listConfig.PageSizes {
		if limit == size {
			validLimit = true
			break
		}
	}
	if !validLimit {
		limit = listConfig.DefaultLimit
	}

	// Handle page jump parameter (page takes precedence over offset)
	if page := c.QueryInt64("page"); page > 0 {
		offset = (page - 1) * limit
	}
	return
}

// SetPaginationData sets common pagination template data on the macaron context
// Parameters: c - macaron context, listName - list name, total/limit/offset - pagination values,
// listConfig - list configuration, defaultColumns - default visible columns, availableColumns - all available columns
func SetPaginationData(c *macaron.Context, listName string, total, limit, offset int64, listConfig ListConfig, defaultColumnsJSON string, availableColumns []string) {
	pageInfo := GetSmartPaginationInfo(total, limit, offset)
	pageInfo.PageSizes = listConfig.PageSizes
	c.Data["PageInfo"] = pageInfo
	c.Data["Total"] = total
	c.Data["Limit"] = limit
	c.Data["ListConfig"] = listConfig
	c.Data["ListName"] = listName
	c.Data["DefaultColumnsJSON"] = defaultColumnsJSON
	c.Data["AvailableColumns"] = availableColumns
}
