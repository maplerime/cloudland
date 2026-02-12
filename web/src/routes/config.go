/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

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
		DefaultLimit: 16,
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
		DefaultLimit: 16,
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
		DefaultLimit: 16,
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
		DefaultLimit: 16,
		PageSizes:    []int64{10, 20, 50, 100},
	}
}
