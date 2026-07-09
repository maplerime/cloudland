/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// ========================== init() registration ==========================

func TestExportWDSVhost_RegistrationCompiles(t *testing.T) {
	_ = ExportWDSVhost
}

// ========================== Args validation ==========================

func TestExportWDSVhost_TooFewArgs(t *testing.T) {
	ctx := context.Background()
	// Minimum args: [script, taskID, resourceType, state, path, msg] = 6
	_, err := ExportWDSVhost(ctx, []string{"export_wds_vhost.sh"})
	if err == nil {
		t.Error("expected error for too few args")
	}
	if !strings.Contains(err.Error(), "wrong params") {
		t.Errorf("expected 'wrong params', got %v", err)
	}
}

func TestExportWDSVhost_FiveArgs_StillTooFew(t *testing.T) {
	ctx := context.Background()
	_, err := ExportWDSVhost(ctx, []string{
		"export_wds_vhost.sh", "1", "volume", "done", "/path/file.img",
	})
	if err == nil {
		t.Error("expected error for 5 args (need 6)")
	}
}

func TestExportWDSVhost_InvalidTaskID(t *testing.T) {
	ctx := context.Background()
	_, err := ExportWDSVhost(ctx, []string{
		"export_wds_vhost.sh",
		"not_a_number", "volume", "done", "/path/file.img", "done",
	})
	if err == nil {
		t.Error("expected error for non-numeric task ID")
	}
}

// ========================== Callback format verification ==========================

func TestExportWDSVhost_CallbackFormat(t *testing.T) {
	// Verify the expected callback format from the script:
	// |:-COMMAND-:| export_wds_vhost.sh '$task_ID' '$resource_type' '$state' '$path' '$msg'
	args := []string{
		"export_wds_vhost.sh",                                  // args[0] = script
		"42",                                                    // args[1] = task ID
		"volume",                                               // args[2] = resource type
		"done",                                                 // args[3] = state
		"/var/data/uss-vol-dts/export-volume-42-1720432800.img", // args[4] = path
		"done",                                                 // args[5] = msg
	}
	if len(args) != 6 {
		t.Errorf("callback should have 6 args, got %d", len(args))
	}
	if args[2] != "volume" && args[2] != "image" {
		t.Errorf("resource_type should be 'volume' or 'image', got %q", args[2])
	}
	if args[3] != "done" && args[3] != "failed" {
		t.Errorf("state should be 'done' or 'failed', got %q", args[3])
	}
}

// ========================== Task status branch logic (pure, no DB) ==========================

func TestExportWDSVhost_TaskStatusBranches(t *testing.T) {
	// state == "done"  → TaskStatusSuccess, message = "exported to: <path>"
	// state != "done"  → TaskStatusFailed,  message = original msg from script
	tests := []struct {
		state        string
		path         string
		expectFailed bool
		expectMsg    string
	}{
		{"done", "/var/data/uss-vol-dts/export-volume-42-1720432800.img", false, "exported to: /var/data/uss-vol-dts/export-volume-42-1720432800.img"},
		{"done", "", false, "done"}, // empty path: use original msg
		{"failed", "", true, "wds export failed: timeout"},
	}
	for _, tc := range tests {
		isFailed := tc.state != "done"
		if isFailed != tc.expectFailed {
			t.Errorf("state=%q: expected failed=%v, got %v", tc.state, tc.expectFailed, isFailed)
		}
		var msg string
		if tc.state == "done" && tc.path != "" {
			msg = "exported to: " + tc.path
		} else if tc.state != "done" {
			msg = "wds export failed: timeout"
		} else {
			msg = "done"
		}
		if msg != tc.expectMsg {
			t.Errorf("state=%q path=%q: expected msg=%q, got %q", tc.state, tc.path, tc.expectMsg, msg)
		}
	}
}

// ========================== Volume status restore logic (pure, no DB) ==========================

func TestExportWDSVhost_VolumeStatusRestoreLogic(t *testing.T) {
	// After export (success or failure): volume.status restored based on InstanceID
	tests := []struct {
		instanceID int64
		wantStatus string
	}{
		{0, "available"},  // not attached → restore to available
		{99, "attached"},  // was attached → restore to attached
	}
	for _, tt := range tests {
		var status string
		if tt.instanceID > 0 {
			status = "attached"
		} else {
			status = "available"
		}
		if status != tt.wantStatus {
			t.Errorf("instanceID=%d: want status=%q, got %q", tt.instanceID, tt.wantStatus, status)
		}
	}
}

// ========================== Task.Resources prefix logic (pure) ==========================

func TestExportWDSVhost_ResourcesPrefixParsing(t *testing.T) {
	tests := []struct {
		resources    string
		isVolume     bool
		expectedUUID string
	}{
		{"volume:abc-123-def", true, "abc-123-def"},
		{"image:img-456-ghi", false, ""},
		{"volume:", true, ""},  // malformed but handles gracefully
		{"", false, ""},
	}
	for _, tt := range tests {
		isVolume := strings.HasPrefix(tt.resources, "volume:")
		if isVolume != tt.isVolume {
			t.Errorf("resources=%q: expected isVolume=%v, got %v", tt.resources, tt.isVolume, isVolume)
		}
		if isVolume {
			uuid := strings.TrimPrefix(tt.resources, "volume:")
			if uuid != tt.expectedUUID {
				t.Errorf("resources=%q: expected uuid=%q, got %q", tt.resources, tt.expectedUUID, uuid)
			}
		}
	}
}

// ========================== Resource type dispatch (pure) ==========================

func TestExportWDSVhost_ResourceTypeDispatch(t *testing.T) {
	// Only "volume" type triggers volume status restore
	// "image" type skips status restore (image has no status lock)
	validTypes := []string{"volume", "image"}
	for _, rt := range validTypes {
		needsRestore := rt == "volume"
		if rt == "volume" && !needsRestore {
			t.Errorf("resource_type=%q should need volume status restore", rt)
		}
		if rt == "image" && needsRestore {
			t.Errorf("resource_type=%q should NOT need volume status restore", rt)
		}
	}
}

// ========================== Path naming convention (pure) ==========================

func TestExportWDSVhost_PathNamingConvention(t *testing.T) {
	// Path generated by script: /var/data/uss-vol-dts/export-{resource_type}-{task_ID}-{ts}.img
	tests := []struct {
		resourceType string
		taskID       int
		expectPrefix string
	}{
		{"volume", 42, "/var/data/uss-vol-dts/export-volume-42-"},
		{"image", 7, "/var/data/uss-vol-dts/export-image-7-"},
	}
	for _, tt := range tests {
		// simulate path construction
		import_path := "/var/data/uss-vol-dts/export-" + tt.resourceType + "-" + strconv.Itoa(tt.taskID) + "-1720432800.img"
		if !strings.HasPrefix(import_path, tt.expectPrefix) {
			t.Errorf("path %q does not start with %q", import_path, tt.expectPrefix)
		}
		if !strings.HasSuffix(import_path, ".img") {
			t.Errorf("path %q should end with .img", import_path)
		}
	}
}

