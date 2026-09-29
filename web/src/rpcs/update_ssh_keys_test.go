/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateSSHKeys_TooFewArgs(t *testing.T) {
	status, err := UpdateSSHKeys(context.Background(), []string{"update_ssh_keys.sh", "1", "success"})
	if err == nil || !strings.Contains(err.Error(), "wrong params") {
		t.Errorf("expected 'wrong params' error, got %v", err)
	}
	if status != "" {
		t.Errorf("expected empty status, got %q", status)
	}
}

func TestUpdateSSHKeys_InvalidTaskID(t *testing.T) {
	status, err := UpdateSSHKeys(context.Background(), []string{"update_ssh_keys.sh", "abc", "success", ""})
	if err == nil {
		t.Error("expected error for non-numeric task ID")
	}
	if status != "" {
		t.Errorf("expected empty status, got %q", status)
	}
}
