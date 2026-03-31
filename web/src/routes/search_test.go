package routes

import (
	"testing"
)

func TestParseInt64Slice(t *testing.T) {
	tests := []struct {
		input    string
		expected []int64
	}{
		{"", nil},
		{"1,2,3", []int64{1, 2, 3}},
		{"42", []int64{42}},
		{"1, 2, abc, 3", []int64{1, 2, 3}},
		{"  ", nil},
	}
	for _, tt := range tests {
		result := ParseInt64Slice(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("ParseInt64Slice(%q) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("ParseInt64Slice(%q)[%d] = %d, want %d", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestParseInt32Slice(t *testing.T) {
	tests := []struct {
		input    string
		expected []int32
	}{
		{"", nil},
		{"1,2,3", []int32{1, 2, 3}},
		{"abc", nil},
	}
	for _, tt := range tests {
		result := ParseInt32Slice(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("ParseInt32Slice(%q) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("ParseInt32Slice(%q)[%d] = %d, want %d", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestParseStringSlice(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"running,pending", []string{"running", "pending"}},
		{" running , pending ", []string{"running", "pending"}},
		{"single", []string{"single"}},
	}
	for _, tt := range tests {
		result := ParseStringSlice(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("ParseStringSlice(%q) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("ParseStringSlice(%q)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}
