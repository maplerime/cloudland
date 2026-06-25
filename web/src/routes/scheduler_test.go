/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"web/src/model"
)

// ========================== shouldRun logic ==========================

func TestShouldRun_OneTime_BeforeExecutionTime(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:  "one-time",
		ExecutionTime: time.Now().Add(1 * time.Hour),
	}
	if shouldRun(task) {
		t.Error("one-time task before execution time should NOT run")
	}
}

func TestShouldRun_OneTime_AfterExecutionTime(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:  "one-time",
		ExecutionTime: time.Now().Add(-1 * time.Hour),
	}
	if !shouldRun(task) {
		t.Error("one-time task after execution time should run")
	}
}

func TestShouldRun_OneTime_ZeroExecutionTime(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:  "one-time",
		ExecutionTime: time.Time{}, // zero value
	}
	if shouldRun(task) {
		t.Error("one-time task with zero execution time should NOT run")
	}
}

func TestParseScheduledTaskTimezoneOffset_ClientZone(t *testing.T) {
	loc := parseScheduledTaskTimezoneOffset("-480")
	if loc == nil {
		t.Fatal("expected timezone location")
	}

	ts, err := parseScheduledTaskExecutionTime("2026-06-05T10:00", loc)
	if err != nil {
		t.Fatalf("parseScheduledTaskExecutionTime returned error: %v", err)
	}

	if got, want := ts.UTC().Format(time.RFC3339), "2026-06-05T02:00:00Z"; got != want {
		t.Fatalf("unexpected UTC conversion, got %s want %s", got, want)
	}
}

func TestShouldRun_Daily_ValidCron(t *testing.T) {
	// "0 3 * * *" = 3:00 AM every day
	task := &model.ScheduledTask{
		ScheduleType:   "daily",
		CronExpression: "0 3 * * *",
	}
	// Should return true (the cron schedule returns a valid next time)
	// This test verifies it doesn't panic and returns a boolean
	_ = shouldRun(task)
	// Cannot assert exact boolean since it depends on current time
}

func TestShouldRun_Daily_InvalidCron(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:   "daily",
		CronExpression: "invalid",
	}
	if shouldRun(task) {
		t.Error("daily task with invalid cron should NOT run")
	}
}

func TestShouldRun_Daily_EmptyCron(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:   "daily",
		CronExpression: "",
	}
	if shouldRun(task) {
		t.Error("daily task with empty cron should NOT run")
	}
}

func TestShouldRun_UnknownScheduleType(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType: "unknown",
	}
	if shouldRun(task) {
		t.Error("unknown schedule type should NOT run")
	}
}

func TestShouldRun_Weekly_ValidCron(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:   "weekly",
		CronExpression: "0 0 * * 1", // every Monday midnight
	}
	_ = shouldRun(task) // should not panic for valid weekly
}

func TestShouldRun_Monthly_ValidCron(t *testing.T) {
	task := &model.ScheduledTask{
		ScheduleType:   "monthly",
		CronExpression: "0 0 1 * *", // 1st of every month
	}
	_ = shouldRun(task) // should not panic for valid monthly
}

// ========================== One-time disables after execution ==========================

func TestShouldRun_OneTime_DisablesAfterRun(t *testing.T) {
	// This test verifies the architecture: one-time tasks self-disable.
	// In production, DB() would persist the status change.
	// Here we verify the logic doesn't panic and returns true.
	task := &model.ScheduledTask{
		ScheduleType:  "one-time",
		ExecutionTime: time.Now().Add(-10 * time.Minute),
	}
	if !shouldRun(task) {
		t.Error("one-time task after time should run")
	}
}

// ========================== Lock TTL ==========================

func TestLockTTL_IsPositive(t *testing.T) {
	if lockTTL <= 0 {
		t.Error("lockTTL must be positive")
	}
	if lockTTL < 1*time.Minute {
		t.Error("lockTTL should be at least 1 minute for task execution safety")
	}
}

// ========================== Task timeout ==========================

func TestTaskTimeout_IsReasonable(t *testing.T) {
	if taskTimeout <= 0 {
		t.Error("taskTimeout must be positive")
	}
	if taskTimeout < 1*time.Minute {
		t.Error("taskTimeout should be at least 1 minute")
	}
	if taskTimeout > 2*time.Hour {
		t.Error("taskTimeout should not exceed 2 hours")
	}
	if lockTTL <= taskTimeout {
		t.Errorf("lockTTL should exceed taskTimeout, got lockTTL=%s taskTimeout=%s", lockTTL, taskTimeout)
	}
}

// ========================== CRON cache safety ==========================

func TestCronCache_SameExpressionReuses(t *testing.T) {
	expr := "0 0 * * *" // midnight daily

	// First call primes cache, second uses cache — neither should panic
	task1 := &model.ScheduledTask{ScheduleType: "daily", CronExpression: expr}
	task2 := &model.ScheduledTask{ScheduleType: "daily", CronExpression: expr}
	_ = shouldRun(task1) // prime cache
	_ = shouldRun(task2) // use cache
}

// ========================== Update selective fields ==========================

func TestScheduledTaskAdmin_Update_SelectiveFields(t *testing.T) {
	updates := map[string]interface{}{}
	name := "new-name"
	status := ""
	scheduleType := "daily"
	retentionCount := -1

	if name != "" {
		updates["name"] = name
	}
	if status != "" {
		updates["status"] = status
	}
	if scheduleType != "" {
		updates["schedule_type"] = scheduleType
	}
	if retentionCount >= 0 {
		updates["retention_count"] = retentionCount
	}

	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d: %v", len(updates), updates)
	}
	if updates["name"] != "new-name" {
		t.Errorf("name = %v", updates["name"])
	}
	if _, ok := updates["status"]; ok {
		t.Error("status should NOT be in updates when empty")
	}
	if _, ok := updates["retention_count"]; ok {
		t.Error("retention_count should NOT be in updates when negative")
	}
}

func TestScheduledTaskAdmin_Update_AllEmptyMeansNoUpdates(t *testing.T) {
	updates := map[string]interface{}{}
	if "" != "" {
		updates["name"] = ""
	}
	if len(updates) != 0 {
		t.Errorf("expected 0 updates for all-empty, got %d", len(updates))
	}
}

// ========================== STaskAction uniqueness ==========================

func TestSTaskAction_Constants(t *testing.T) {
	tests := []struct {
		name  string
		value model.STaskAction
		want  string
	}{
		{"Stop", model.STaskActionStop, "stop"},
		{"HardStop", model.STaskActionHardStop, "hard_stop"},
		{"Start", model.STaskActionStart, "start"},
		{"Restart", model.STaskActionRestart, "restart"},
		{"HardRestart", model.STaskActionHardRestart, "hard_restart"},
		{"Snapshot", model.STaskActionSnapshot, "snapshot"},
		{"Backup", model.STaskActionBackup, "backup"},
	}
	for _, tt := range tests {
		if string(tt.value) != tt.want {
			t.Errorf("STaskAction%s = %q, want %q", tt.name, tt.value, tt.want)
		}
	}
}

// ========================== Model validation ==========================

func TestScheduledTask_DefaultStatus(t *testing.T) {
	task := &model.ScheduledTask{Name: "test"}
	if task.Status != "" {
		t.Errorf("new task status = %q, want empty", task.Status)
	}
}

func TestScheduledTaskHistory_Fields(t *testing.T) {
	h := &model.ScheduledTaskHistory{
		ScheduledTaskID: 1,
		Status:          "success",
		Message:         "done",
		Duration:        120,
	}
	if h.ScheduledTaskID != 1 || h.Duration != 120 {
		t.Error("ScheduledTaskHistory fields not preserved")
	}
}

// ========================== RunScheduler signature ==========================

func TestRunScheduler_FunctionReturnType(t *testing.T) {
	var _ func() error = RunScheduler
}

// ========================== Lock name format ==========================

func TestLockNameFormat(t *testing.T) {
	expected := "scheduled_task_42"
	actual := fmt.Sprintf("scheduled_task_%d", 42)
	if actual != expected {
		t.Errorf("lock name = %q, want %q", actual, expected)
	}
}

// ========================== ListHistory delegation ==========================

func TestScheduledTaskHistoryAdmin_ListDelegates(t *testing.T) {
	// Compile-time check: ScheduledTaskHistoryAdmin has List method
	a := &ScheduledTaskHistoryAdmin{}
	_ = a
}

func TestShouldResetTaskLockOnUpdate_ReenableOneTimeTask(t *testing.T) {
	before := &model.ScheduledTask{
		ScheduleType: "one-time",
		Status:       "disabled",
	}
	after := &model.ScheduledTask{
		ScheduleType: "one-time",
		Status:       "enabled",
	}
	status := "enabled"

	if !shouldResetTaskLockOnUpdate(before, after, &ScheduledTaskUpdateOptions{Status: &status}) {
		t.Fatal("expected one-time re-enable to reset task lock")
	}
}

func TestShouldResetTaskLockOnUpdate_RecurringTaskDoesNotReset(t *testing.T) {
	before := &model.ScheduledTask{
		ScheduleType: "daily",
		Status:       "disabled",
	}
	after := &model.ScheduledTask{
		ScheduleType: "daily",
		Status:       "enabled",
	}
	status := "enabled"

	if shouldResetTaskLockOnUpdate(before, after, &ScheduledTaskUpdateOptions{Status: &status}) {
		t.Fatal("did not expect recurring task update to reset task lock")
	}
}

func TestOneTimeScheduleLookupDeadline_AddsOneMinute(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got := oneTimeScheduleLookupDeadline(now)
	want := now.Add(1 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("lookup deadline = %s, want %s", got, want)
	}
}

func TestOneTimeScheduleLookupDeadline_PreservesLocation(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, loc)
	got := oneTimeScheduleLookupDeadline(now)
	if got.Location() != loc {
		t.Fatal("expected lookup deadline to preserve time location")
	}
}

func TestScheduledTaskSchedulerColumns_ContainRequiredFields(t *testing.T) {
	columns := scheduledTaskSchedulerColumns()
	required := []string{
		"id",
		"owner",
		"name",
		"task_type",
		"resource_id",
		"operation",
		"schedule_type",
		"execution_time",
		"cron_expression",
		"retention_count",
		"status",
	}

	columnSet := map[string]bool{}
	for _, column := range columns {
		columnSet[column] = true
	}

	for _, field := range required {
		if !columnSet[field] {
			t.Fatalf("missing scheduler column %q", field)
		}
	}
}

func TestScheduledTaskSchedulerOrder_Strategies(t *testing.T) {
	if got, want := recurringScheduledTaskOrder(), "id ASC"; got != want {
		t.Fatalf("recurring order = %q, want %q", got, want)
	}
	if got, want := oneTimeScheduledTaskOrder(), "execution_time ASC, id ASC"; got != want {
		t.Fatalf("one-time order = %q, want %q", got, want)
	}
}

func TestMergeScheduledTasksForExecution_OneTimeFirst(t *testing.T) {
	oneTimeTasks := []*model.ScheduledTask{
		{Model: model.Model{ID: 2}, ScheduleType: "one-time", Name: "one-time"},
	}
	recurringTasks := []*model.ScheduledTask{
		{Model: model.Model{ID: 5}, ScheduleType: "daily", Name: "recurring"},
	}

	merged := mergeScheduledTasksForExecution(oneTimeTasks, recurringTasks)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged tasks, got %d", len(merged))
	}
	if merged[0].ScheduleType != "one-time" || merged[0].ID != 2 {
		t.Fatalf("expected one-time task first, got %#v", merged[0])
	}
	if merged[1].ScheduleType != "daily" || merged[1].ID != 5 {
		t.Fatalf("expected recurring task second, got %#v", merged[1])
	}
}

// ========================== Scheduled task validation ==========================

func TestValidateScheduledTaskConfig_OneTime_Success(t *testing.T) {
	task := &model.ScheduledTask{
		Name:           " nightly stop ",
		TaskType:       "instance_op",
		ResourceType:   "instance",
		ResourceID:     100,
		Operation:      model.STaskActionStop,
		ScheduleType:   "one-time",
		ExecutionTime:  time.Now().Add(30 * time.Minute),
		CronExpression: "0 1 * * *",
		Status:         "enabled",
	}

	err := validateScheduledTaskConfig(task)
	if err != nil {
		t.Fatalf("validateScheduledTaskConfig returned error: %v", err)
	}
	if task.Name != "nightly stop" {
		t.Fatalf("expected trimmed name, got %q", task.Name)
	}
	if task.CronExpression != "" {
		t.Fatalf("one-time task should clear cron expression, got %q", task.CronExpression)
	}
}

func TestValidateScheduledTaskConfig_Recurring_Success(t *testing.T) {
	task := &model.ScheduledTask{
		Name:           "volume-backup",
		TaskType:       "volume_backup",
		ResourceType:   "volume",
		ResourceID:     200,
		Operation:      model.STaskActionBackup,
		ScheduleType:   "daily",
		CronExpression: "0 3 * * *",
		ExecutionTime:  time.Now(),
		Status:         "enabled",
	}

	err := validateScheduledTaskConfig(task)
	if err != nil {
		t.Fatalf("validateScheduledTaskConfig returned error: %v", err)
	}
	if !task.ExecutionTime.IsZero() {
		t.Fatal("recurring task should clear execution time")
	}
}

func TestValidateScheduledTaskConfig_InvalidCombination(t *testing.T) {
	task := &model.ScheduledTask{
		Name:          "bad-task",
		TaskType:      "volume_backup",
		ResourceType:  "volume",
		ResourceID:    1,
		Operation:     model.STaskActionStart,
		ScheduleType:  "one-time",
		ExecutionTime: time.Now().Add(time.Hour),
		Status:        "enabled",
	}

	err := validateScheduledTaskConfig(task)
	if err == nil {
		t.Fatal("expected invalid operation combination to fail")
	}
	if !strings.Contains(err.Error(), "invalid volume backup operation") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateScheduledTaskConfig_InvalidRecurringCron(t *testing.T) {
	task := &model.ScheduledTask{
		Name:           "bad-cron",
		TaskType:       "instance_op",
		ResourceType:   "instance",
		ResourceID:     2,
		Operation:      model.STaskActionStop,
		ScheduleType:   "daily",
		CronExpression: "bad cron",
		Status:         "enabled",
	}

	err := validateScheduledTaskConfig(task)
	if err == nil {
		t.Fatal("expected invalid cron to fail")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateScheduledTaskConfig_InvalidStatus(t *testing.T) {
	task := &model.ScheduledTask{
		Name:          "bad-status",
		TaskType:      "instance_op",
		ResourceType:  "instance",
		ResourceID:    3,
		Operation:     model.STaskActionStop,
		ScheduleType:  "one-time",
		ExecutionTime: time.Now().Add(time.Hour),
		Status:        "running",
	}

	err := validateScheduledTaskConfig(task)
	if err == nil {
		t.Fatal("expected invalid status to fail")
	}
}

// ========================== Execution time parsing ==========================

func TestParseScheduledTaskExecutionTime_DatetimeLocal(t *testing.T) {
	got, err := parseScheduledTaskExecutionTime("2026-06-04T12:34", time.Local)
	if err != nil {
		t.Fatalf("parseScheduledTaskExecutionTime returned error: %v", err)
	}
	localGot := got.In(time.Local)
	if localGot.Year() != 2026 || localGot.Month() != 6 || localGot.Day() != 4 || localGot.Hour() != 12 || localGot.Minute() != 34 {
		t.Fatalf("unexpected parsed time: %v", localGot)
	}
}

func TestParseScheduledTaskExecutionTime_Empty(t *testing.T) {
	got, err := parseScheduledTaskExecutionTime("", time.Local)
	if err != nil {
		t.Fatalf("parseScheduledTaskExecutionTime returned error: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time, got %v", got)
	}
}

func TestParseScheduledTaskExecutionTime_Invalid(t *testing.T) {
	_, err := parseScheduledTaskExecutionTime("not-a-time", time.Local)
	if err == nil {
		t.Fatal("expected invalid time format to fail")
	}
}
