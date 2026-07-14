/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"github.com/jinzhu/gorm"
	"github.com/robfig/cron/v3"
	"gopkg.in/macaron.v1"
)

// ScheduledTaskAdmin handles the backend logic for scheduled task management.
// It provides CRUD operations and business logic for automated task scheduling.
type ScheduledTaskAdmin struct{}

// ScheduledTaskHistoryAdmin handles the backend logic for task execution history.
// It provides functionality to track and query task execution records.
type ScheduledTaskHistoryAdmin struct{}

// ScheduledTaskView handles the web interface for scheduled task management.
// It provides HTML templates and form processing for the web console.
type ScheduledTaskView struct{}

var scheduledTaskAdmin = &ScheduledTaskAdmin{}
var scheduledTaskView = &ScheduledTaskView{}
var scheduledTaskHistoryAdmin = &ScheduledTaskHistoryAdmin{}
var scheduledTaskHistoryView = &ScheduledTaskView{}

type ScheduledTaskUpdateOptions struct {
	Name           *string
	Status         *string
	ScheduleType   *string
	CronExpression *string
	RetentionCount *int
	ExecutionTime  *time.Time
}

var recurringCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func normalizeScheduledTaskFields(task *model.ScheduledTask) {
	task.Name = strings.TrimSpace(task.Name)
	task.TaskType = strings.TrimSpace(task.TaskType)
	task.ResourceType = strings.TrimSpace(task.ResourceType)
	task.ScheduleType = strings.TrimSpace(task.ScheduleType)
	task.CronExpression = strings.TrimSpace(task.CronExpression)
	task.Status = strings.TrimSpace(task.Status)
}

func validateScheduledTaskConfig(task *model.ScheduledTask) error {
	normalizeScheduledTaskFields(task)

	if task.Name == "" {
		return fmt.Errorf("task name is required")
	}
	if task.ResourceID <= 0 {
		return fmt.Errorf("invalid resource ID: %d (must be greater than 0)", task.ResourceID)
	}
	if task.RetentionCount < 0 {
		return fmt.Errorf("retention count cannot be negative, got %d", task.RetentionCount)
	}

	switch task.Status {
	case "", "enabled", "disabled":
	default:
		return fmt.Errorf("invalid task status: %q (must be one of: enabled, disabled)", task.Status)
	}

	switch task.TaskType {
	case "instance_op":
		if task.ResourceType != "instance" {
			return fmt.Errorf("instance operation tasks must target instance resources, got resource_type=%q", task.ResourceType)
		}
		switch task.Operation {
		case model.STaskActionStart, model.STaskActionStop, model.STaskActionHardStop, model.STaskActionRestart, model.STaskActionHardRestart:
		default:
			return fmt.Errorf("invalid instance operation: %q (must be one of: start, stop, hard_stop, restart, hard_restart)", task.Operation)
		}
	case "volume_backup":
		if task.ResourceType != "volume" {
			return fmt.Errorf("volume operation tasks must target volume resources, got resource_type=%q", task.ResourceType)
		}
		switch task.Operation {
		case model.STaskActionSnapshot, model.STaskActionBackup:
		default:
			return fmt.Errorf("invalid volume operation: %q (must be one of: snapshot, backup)", task.Operation)
		}
	default:
		return fmt.Errorf("invalid task type: %q (must be one of: instance_op, volume_backup)", task.TaskType)
	}

	switch task.ScheduleType {
	case "one-time":
		if task.ExecutionTime.IsZero() {
			return fmt.Errorf("execution time is required for one-time tasks")
		}
		task.CronExpression = ""
	case "daily", "weekly", "monthly":
		if task.CronExpression == "" {
			return fmt.Errorf("cron expression is required for %q tasks", task.ScheduleType)
		}
		if _, err := recurringCronParser.Parse(task.CronExpression); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", task.CronExpression, err)
		}
		if err := validateCronScheduleSemantics(task.ScheduleType, task.CronExpression); err != nil {
			return err
		}
		task.ExecutionTime = time.Time{}
	default:
		return fmt.Errorf("invalid schedule type: %q (must be one of: one-time, daily, weekly, monthly)", task.ScheduleType)
	}

	return nil
}

// stripCronTimezonePrefix splits an optional leading "TZ=<loc>"/"CRON_TZ=<loc>"
// prefix (as understood by robfig/cron) from the 5-field cron spec that follows.
func stripCronTimezonePrefix(expr string) (tz string, rest string) {
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		if idx := strings.Index(expr, " "); idx != -1 {
			eq := strings.Index(expr, "=")
			return expr[eq+1 : idx], strings.TrimSpace(expr[idx:])
		}
	}
	return "", expr
}

// validateCronScheduleSemantics ensures the cron expression's day-of-month and
// day-of-week fields actually match the declared schedule type, so a task
// labeled "daily"/"weekly"/"monthly" behaves the way its label implies.
func validateCronScheduleSemantics(scheduleType, cronExpr string) error {
	_, fieldsPart := stripCronTimezonePrefix(cronExpr)
	fields := strings.Fields(fieldsPart)
	if len(fields) != 5 {
		return fmt.Errorf("cron expression %q must have exactly 5 space-separated fields (minute hour day-of-month month day-of-week), got %d", cronExpr, len(fields))
	}
	dom, dow := fields[2], fields[4]
	switch scheduleType {
	case "daily":
		if dom != "*" || dow != "*" {
			return fmt.Errorf("cron expression %q doesn't match schedule type \"daily\": the 3rd field (day-of-month) and 5th field (day-of-week) must both be '*', got day-of-month=%q day-of-week=%q — this looks like a weekly or monthly schedule, check the schedule type or fix these fields", cronExpr, dom, dow)
		}
	case "weekly":
		if dow == "*" {
			return fmt.Errorf("cron expression %q doesn't match schedule type \"weekly\": the 5th field (day-of-week) must be a specific value (0-6 or MON-SUN), not '*'", cronExpr)
		}
		if dom != "*" {
			return fmt.Errorf("cron expression %q doesn't match schedule type \"weekly\": the 3rd field (day-of-month) must be '*', got %q — this looks like a monthly schedule", cronExpr, dom)
		}
	case "monthly":
		if dom == "*" {
			return fmt.Errorf("cron expression %q doesn't match schedule type \"monthly\": the 3rd field (day-of-month) must be a specific value (1-31), not '*'", cronExpr)
		}
	}
	return nil
}

// EnsureCronTimezone prepends a CRON_TZ=<location> prefix (recognized natively
// by robfig/cron) so the cron schedule is evaluated in the client's timezone
// instead of the scheduler's default UTC. Leaves the expression untouched if
// it already carries a timezone prefix or the client timezone is invalid.
func EnsureCronTimezone(expr, clientTimezone string) string {
	if tz, _ := stripCronTimezonePrefix(expr); tz != "" {
		return expr
	}
	clientTimezone = strings.TrimSpace(clientTimezone)
	if clientTimezone == "" {
		return expr
	}
	if _, err := time.LoadLocation(clientTimezone); err != nil {
		return expr
	}
	return fmt.Sprintf("CRON_TZ=%s %s", clientTimezone, expr)
}

func validateScheduledTaskResource(ctx context.Context, taskType, resourceType string, resourceID int64) error {
	switch taskType {
	case "instance_op":
		if resourceType != "instance" {
			return fmt.Errorf("instance task must target instance resources")
		}
		_, err := instanceAdmin.Get(ctx, resourceID)
		return err
	case "volume_backup":
		if resourceType != "volume" {
			return fmt.Errorf("volume backup task must target volume resources")
		}
		_, err := volumeAdmin.Get(ctx, resourceID)
		return err
	default:
		return fmt.Errorf("invalid task type: %s", taskType)
	}
}

func parseScheduledTaskTimezoneOffset(raw string) *time.Location {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Local
	}

	offsetMinutes, err := strconv.Atoi(raw)
	if err != nil {
		return time.Local
	}

	// JavaScript getTimezoneOffset() returns minutes behind UTC.
	offsetSeconds := -offsetMinutes * 60
	return time.FixedZone("client", offsetSeconds)
}

func parseScheduledTaskExecutionTime(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if loc == nil {
		loc = time.Local
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return ts.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid execution time %q: expected format YYYY-MM-DDTHH:MM (e.g. 2026-01-02T15:04)", raw)
}

func shouldResetTaskLockOnUpdate(before, after *model.ScheduledTask, opts *ScheduledTaskUpdateOptions) bool {
	if before == nil || after == nil || opts == nil {
		return false
	}
	if after.ScheduleType != "one-time" {
		return false
	}
	if opts.ExecutionTime != nil {
		return true
	}
	if opts.ScheduleType != nil && *opts.ScheduleType == "one-time" {
		return true
	}
	if opts.Status != nil && *opts.Status == "enabled" && before.Status != "enabled" {
		return true
	}
	return false
}

func clearScheduledTaskLock(taskID int64) error {
	lockName := fmt.Sprintf("scheduled_task_%d", taskID)
	return DB().Where("name = ?", lockName).Delete(&model.Lock{}).Error
}

func oneTimeScheduleLookupDeadline(now time.Time) time.Time {
	return now.Add(1 * time.Minute)
}

func scheduledTaskSchedulerColumns() []string {
	return []string{
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
}

func recurringScheduledTaskOrder() string {
	return "id ASC"
}

func oneTimeScheduledTaskOrder() string {
	return "execution_time ASC, id ASC"
}

func mergeScheduledTasksForExecution(oneTimeTasks, recurringTasks []*model.ScheduledTask) []*model.ScheduledTask {
	tasks := make([]*model.ScheduledTask, 0, len(oneTimeTasks)+len(recurringTasks))
	tasks = append(tasks, oneTimeTasks...)
	tasks = append(tasks, recurringTasks...)
	return tasks
}

// Create creates a new scheduled task with the specified parameters.
// Returns the created task record or an error if creation fails.
// Validates user permissions and sets default values.
func (a *ScheduledTaskAdmin) Create(ctx context.Context, name, taskType, resourceType string,
	operation model.STaskAction, scheduleType, cronExpression string,
	resourceID int64, retentionCount int, executionTime time.Time) (task *model.ScheduledTask, err error) {
	logger.Infof("[Admin] Creating scheduled task - function entry: name=%s, taskType=%s, resourceType=%s, operation=%s, scheduleType=%s, cronExpression=%s, resourceID=%d, retentionCount=%d, executionTime=%s",
		name, taskType, resourceType, operation, scheduleType, cronExpression, resourceID, retentionCount, executionTime)

	// Get user membership and validate permissions
	memberShip := GetMemberShip(ctx)
	logger.Debugf("[Admin] Creating task for organization: %d", memberShip.OrgID)

	// Create task instance with validated data
	task = &model.ScheduledTask{
		Owner:          memberShip.OrgID,
		Name:           name,
		TaskType:       taskType,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Operation:      operation,
		ScheduleType:   scheduleType,
		ExecutionTime:  executionTime,
		CronExpression: cronExpression,
		RetentionCount: retentionCount,
		Status:         "enabled", // Default status
	}

	if err = validateScheduledTaskConfig(task); err != nil {
		return nil, NewCLError(ErrInvalidParameter, err.Error(), err)
	}
	if task.ScheduleType == "one-time" && !task.ExecutionTime.After(time.Now().UTC()) {
		return nil, NewCLError(ErrInvalidParameter, "execution time must be in the future", nil)
	}
	if err = validateScheduledTaskResource(ctx, task.TaskType, task.ResourceType, task.ResourceID); err != nil {
		return nil, NewCLError(ErrInvalidParameter, err.Error(), err)
	}

	// Save to database
	db := DB()
	err = db.Create(task).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to create scheduled task in database: %v", err)
		return
	}

	logger.Infof("[Admin] Scheduled task created successfully: id=%d - function exit", task.ID)
	return
}

// List retrieves a paginated list of scheduled tasks for the current organization.
// Supports filtering by name and custom ordering. Returns total count and task list.
func (a *ScheduledTaskAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, tasks []*model.ScheduledTask, err error) {
	logger.Debugf("[Admin] Listing scheduled tasks - function entry: offset=%d, limit=%d, order=%s, query=%s", offset, limit, order, query)

	// Get user membership for permission filtering
	memberShip := GetMemberShip(ctx)
	db := DB()

	// Set default pagination values
	if limit == 0 {
		limit = 16
		logger.Debug("[Admin] Using default limit of 16")
	}
	if order == "" {
		order = "created_at"
		logger.Debug("[Admin] Using default order: created_at")
	}

	// Build search query if provided
	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
		logger.Debugf("[Admin] Search query applied: %s", query)
	}

	// Apply organization filter
	where := memberShip.GetWhere()
	logger.Debugf("[Admin] Organization filter applied: %s", where)

	// Count total matching records
	if err = db.Model(&model.ScheduledTask{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Errorf("[Admin] Failed to count scheduled tasks: %v", err)
		return
	}

	// Retrieve paginated results with ordering
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	err = db.Where(where).Where(query).Find(&tasks).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled tasks: %v", err)
		return
	}

	logger.Debugf("[Admin] Successfully retrieved %d scheduled tasks (total: %d) - function exit", len(tasks), total)
	return
}

// Get retrieves a single scheduled task by ID for the current organization.
// Validates ownership permissions before returning the task.
func (a *ScheduledTaskAdmin) Get(ctx context.Context, id int64) (task *model.ScheduledTask, err error) {
	logger.Debugf("[Admin] Getting scheduled task - function entry: id=%d", id)

	// Get database connection and user membership
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()

	// Query for the task with organization filtering
	task = &model.ScheduledTask{}
	err = db.Where(where).Where("id = ?", id).First(task).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled task %d: %v", id, err)
		return
	}

	logger.Debugf("[Admin] Successfully retrieved scheduled task %d - function exit", id)
	return
}

// GetTaskByUUID retrieves a single scheduled task by its UUID with
// organization filtering. Returns ErrResourceNotFound when no task matches,
// so API callers can map it to HTTP 404.
func (a *ScheduledTaskAdmin) GetTaskByUUID(ctx context.Context, uuID string) (task *model.ScheduledTask, err error) {
	logger.Debugf("[Admin] Getting scheduled task - function entry: uuid=%s", uuID)

	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()

	task = &model.ScheduledTask{}
	err = db.Where(where).Where("uuid = ?", uuID).First(task).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled task %s: %v", uuID, err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewCLError(ErrResourceNotFound, fmt.Sprintf("scheduled task %s not found", uuID), err)
		}
		return
	}

	logger.Debugf("[Admin] Successfully retrieved scheduled task %s - function exit", uuID)
	return
}

// GetScheduledTaskResourceUUID converts the task's internal resource ID to the
// resource's UUID for external consumption (API responses and Web Console).
// Returns an empty string when the resource no longer exists.
func GetScheduledTaskResourceUUID(ctx context.Context, task *model.ScheduledTask) string {
	switch task.ResourceType {
	case "instance":
		instance, err := instanceAdmin.Get(ctx, task.ResourceID)
		if err == nil {
			return instance.UUID
		}
		logger.Warningf("Failed to resolve instance %d of scheduled task %d: %v", task.ResourceID, task.ID, err)
	case "volume":
		volume, err := volumeAdmin.Get(ctx, task.ResourceID)
		if err == nil {
			return volume.UUID
		}
		logger.Warningf("Failed to resolve volume %d of scheduled task %d: %v", task.ResourceID, task.ID, err)
	}
	return ""
}

// GetScheduledTaskResourceUUIDs resolves the target resource UUIDs of the
// given tasks in batch — one IN query per resource type instead of one point
// query per task. Returns a map keyed by task ID; tasks whose resource no
// longer exists map to an empty string.
func GetScheduledTaskResourceUUIDs(ctx context.Context, tasks []*model.ScheduledTask) map[int64]string {
	result := make(map[int64]string, len(tasks))
	var instanceIDs, volumeIDs []int64
	for _, task := range tasks {
		result[task.ID] = ""
		switch task.ResourceType {
		case "instance":
			instanceIDs = append(instanceIDs, task.ResourceID)
		case "volume":
			volumeIDs = append(volumeIDs, task.ResourceID)
		}
	}

	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	instanceUUIDs := make(map[int64]string, len(instanceIDs))
	if len(instanceIDs) > 0 {
		instances := []*model.Instance{}
		if err := db.Select("id, uuid").Where(where).Where("id IN (?)", instanceIDs).Find(&instances).Error; err != nil {
			logger.Warningf("Failed to batch resolve instances of scheduled tasks: %v", err)
		}
		for _, inst := range instances {
			instanceUUIDs[inst.ID] = inst.UUID
		}
	}
	volumeUUIDs := make(map[int64]string, len(volumeIDs))
	if len(volumeIDs) > 0 {
		volumes := []*model.Volume{}
		if err := db.Select("id, uuid").Where(where).Where("id IN (?)", volumeIDs).Find(&volumes).Error; err != nil {
			logger.Warningf("Failed to batch resolve volumes of scheduled tasks: %v", err)
		}
		for _, vol := range volumes {
			volumeUUIDs[vol.ID] = vol.UUID
		}
	}

	for _, task := range tasks {
		switch task.ResourceType {
		case "instance":
			result[task.ID] = instanceUUIDs[task.ResourceID]
		case "volume":
			result[task.ID] = volumeUUIDs[task.ResourceID]
		}
	}
	return result
}

// Update modifies an existing scheduled task with the provided parameters.
// Only non-empty fields are updated. Validates ownership before modification.
func (a *ScheduledTaskAdmin) Update(ctx context.Context, id int64, opts *ScheduledTaskUpdateOptions) (task *model.ScheduledTask, err error) {
	logger.Infof("[Admin] Updating scheduled task - function entry: id=%d", id)

	// Get database connection and user membership
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()

	// First, retrieve the existing task to verify ownership
	task = &model.ScheduledTask{}
	err = db.Where(where).Where("id = ?", id).First(task).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled task %d for update: %v", id, err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewCLError(ErrResourceNotFound, fmt.Sprintf("scheduled task %d not found", id), err)
		}
		return
	}

	// Build update map (selective update — only non-empty fields)
	if opts == nil {
		return task, fmt.Errorf("empty update options")
	}

	updated := *task
	if opts.Name != nil {
		updated.Name = *opts.Name
	}
	if opts.Status != nil {
		updated.Status = *opts.Status
	}
	if opts.ScheduleType != nil {
		updated.ScheduleType = *opts.ScheduleType
	}
	if opts.CronExpression != nil {
		updated.CronExpression = *opts.CronExpression
	}
	if opts.RetentionCount != nil {
		updated.RetentionCount = *opts.RetentionCount
	}
	if opts.ExecutionTime != nil {
		updated.ExecutionTime = *opts.ExecutionTime
	}

	if err = validateScheduledTaskConfig(&updated); err != nil {
		return nil, NewCLError(ErrInvalidParameter, err.Error(), err)
	}
	if opts.ExecutionTime != nil && updated.ScheduleType == "one-time" && !updated.ExecutionTime.After(time.Now().UTC()) {
		return nil, NewCLError(ErrInvalidParameter, "execution time must be in the future", nil)
	}

	updates := map[string]interface{}{}
	if opts.Name != nil && updated.Name != task.Name {
		updates["name"] = updated.Name
	}
	if opts.Status != nil && updated.Status != task.Status {
		updates["status"] = updated.Status
	}
	if opts.ScheduleType != nil || opts.CronExpression != nil || opts.ExecutionTime != nil {
		updates["schedule_type"] = updated.ScheduleType
		updates["cron_expression"] = updated.CronExpression
		updates["execution_time"] = updated.ExecutionTime
	}
	if opts.RetentionCount != nil && updated.RetentionCount != task.RetentionCount {
		updates["retention_count"] = updated.RetentionCount
	}

	logger.Debugf("[Admin] Updating fields: %v", updates)
	if len(updates) == 0 {
		return task, nil
	}

	// Save the updated task (selective update)
	err = db.Model(task).Updates(updates).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to update scheduled task %d: %v", id, err)
		return
	}

	if shouldResetTaskLockOnUpdate(task, &updated, opts) {
		if lockErr := clearScheduledTaskLock(task.ID); lockErr != nil {
			logger.Warningf("[Admin] Failed to clear scheduled task lock for task %d: %v", task.ID, lockErr)
		}
	}

	logger.Infof("[Admin] Scheduled task %d updated successfully - function exit", id)
	return
}

// Delete removes a scheduled task by its ID.
// Validates ownership permissions before deletion.
func (a *ScheduledTaskAdmin) Delete(ctx context.Context, id int64) (err error) {
	logger.Infof("[Admin] Deleting scheduled task - function entry: id=%d", id)

	// Get database connection and user membership
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()

	// Delete the task with organization filtering
	result := db.Where(where).Where("id = ?", id).Delete(&model.ScheduledTask{})
	if result.Error != nil {
		logger.Errorf("[Admin] Failed to delete scheduled task %d: %v", id, result.Error)
		err = result.Error
		return
	}
	// gorm's Delete does not error when nothing matches; report it as not
	// found instead of pretending the deletion succeeded.
	if result.RowsAffected == 0 {
		logger.Errorf("[Admin] Scheduled task %d not found for deletion", id)
		err = NewCLError(ErrResourceNotFound, fmt.Sprintf("scheduled task %d not found", id), nil)
		return
	}

	logger.Infof("[Admin] Scheduled task %d deleted successfully - function exit", id)
	return
}

// ListEnabledTasks retrieves all scheduled tasks that are currently enabled.
// Used by the scheduler to find tasks that need to be executed.
func (a *ScheduledTaskAdmin) ListEnabledTasks(ctx context.Context) (tasks []*model.ScheduledTask, err error) {
	logger.Debug("[Admin] Listing enabled scheduled tasks - function entry")

	db := DB()
	columns := scheduledTaskSchedulerColumns()

	var recurringTasks []*model.ScheduledTask
	err = db.Select(columns).
		Where("status = ? AND schedule_type <> ?", "enabled", "one-time").
		Order(recurringScheduledTaskOrder()).
		Find(&recurringTasks).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to list enabled recurring scheduled tasks: %v", err)
		return
	}

	var oneTimeTasks []*model.ScheduledTask
	deadline := oneTimeScheduleLookupDeadline(time.Now().UTC())
	err = db.Select(columns).
		Where(
			"status = ? AND schedule_type = ? AND execution_time <= ?",
			"enabled",
			"one-time",
			deadline,
		).
		Order(oneTimeScheduledTaskOrder()).
		Find(&oneTimeTasks).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to list due one-time scheduled tasks: %v", err)
		return
	}

	tasks = mergeScheduledTasksForExecution(oneTimeTasks, recurringTasks)

	logger.Debugf("[Admin] Found %d enabled scheduled tasks - function exit", len(tasks))
	return
}

// ListHistory retrieves the execution history for a specific scheduled task.
// Supports pagination and filtering by task ID. Used by both API and web interface.
func (a *ScheduledTaskAdmin) ListHistory(ctx context.Context, scheduled_task_id int64, offset, limit int64, order string) (total int64, historys []*model.ScheduledTaskHistory, err error) {
	logger.Debugf("[Admin] Listing scheduled task history - function entry: scheduled_task_id=%d, offset=%d, limit=%d, order=%s", scheduled_task_id, offset, limit, order)

	// Get database connection and validate input
	db := DB()
	memberShip := GetMemberShip(ctx)

	// Set default values
	if order == "" {
		order = "created_at"
		logger.Debug("[Admin] Using default order: created_at")
	}
	if limit == 0 {
		limit = 16
		logger.Debug("[Admin] Using default limit of 16")
	}

	// Apply organization filter and task ID filter
	where := memberShip.GetWhere()
	logger.Debugf("[Admin] Organization filter applied: %s", where)

	queryDB := db.Model(&model.ScheduledTaskHistory{}).
		Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").
		Where(where)
	if scheduled_task_id > 0 {
		queryDB = queryDB.Where("scheduled_task_histories.scheduled_task_id = ?", scheduled_task_id)
	}

	// Count total matching history records
	if err = queryDB.Count(&total).Error; err != nil {
		logger.Errorf("[Admin] Failed to count scheduled task history: %v", err)
		return
	}

	// Retrieve paginated history records with ordering
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	resultDB := db.Preload("ScheduledTask", func(preloadDB *gorm.DB) *gorm.DB {
		return preloadDB.Unscoped()
	}).
		Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").
		Where(where)
	if scheduled_task_id > 0 {
		resultDB = resultDB.Where("scheduled_task_histories.scheduled_task_id = ?", scheduled_task_id)
	}
	err = resultDB.Find(&historys).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled task history: %v", err)
		return
	}

	logger.Debugf("[Admin] Successfully retrieved %d history records (total: %d) - function exit", len(historys), total)
	return
}

// List retrieves execution history records with pagination and filtering.
// Delegates to ScheduledTaskAdmin.ListHistory.
func (a *ScheduledTaskHistoryAdmin) List(ctx context.Context, offset, limit int64, order, query string, scheduledTaskID int64) (total int64, historys []*model.ScheduledTaskHistory, err error) {
	return scheduledTaskAdmin.ListHistory(ctx, scheduledTaskID, offset, limit, order)
}

func (v *ScheduledTaskView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listConfig, offset, limit := GetPaginationParams(c, "scheduled_tasks")
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, tasks, err := scheduledTaskAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	// Resolve the target resource UUID of each task for display (task ID -> resource UUID)
	resourceUUIDs := GetScheduledTaskResourceUUIDs(c.Req.Context(), tasks)
	c.Data["Tasks"] = tasks
	c.Data["ResourceUUIDs"] = resourceUUIDs
	c.Data["Query"] = query
	SetPaginationData(c, "scheduled_tasks", total, limit, offset, listConfig,
		`["ID", "TaskUUID", "Name", "TaskType", "ResourceID", "ResourceUUID", "Operation", "ScheduleType", "Status", "History", "Edit", "Delete"]`,
		[]string{"ID", "TaskUUID", "Name", "TaskType", "ResourceID", "ResourceUUID", "Operation", "ScheduleType", "Status", "History", "Edit", "Delete"})
	c.HTML(200, "scheduled_tasks")
}

func (v *ScheduledTaskView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "scheduled_tasks_new")
}

func (v *ScheduledTaskView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../scheduled_tasks"
	name := c.QueryTrim("name")
	taskType := c.QueryTrim("task_type")
	resourceType := c.QueryTrim("resource_type")
	resourceID := c.QueryInt64("resource_id")
	operation := c.QueryTrim("operation")
	scheduleType := c.QueryTrim("schedule_type")
	executionTimeStr := c.QueryTrim("execution_time")
	clientTZ := parseScheduledTaskTimezoneOffset(c.QueryTrim("timezone_offset_minutes"))
	cronExpression := c.QueryTrim("cron_expression")
	if scheduleType != "one-time" {
		cronExpression = EnsureCronTimezone(cronExpression, c.QueryTrim("client_timezone"))
	}
	retentionCount := c.QueryInt("retention_count")
	executionTime, err := parseScheduledTaskExecutionTime(executionTimeStr, clientTZ)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err = scheduledTaskAdmin.Create(c.Req.Context(), name, taskType, resourceType, model.STaskAction(operation), scheduleType, cronExpression, resourceID, retentionCount, executionTime)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *ScheduledTaskView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.ParamsInt64(":id")
	task, err := scheduledTaskAdmin.Get(c.Req.Context(), id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}
	permit := memberShip.ValidateOwner(model.Writer, task.Owner)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	c.Data["Task"] = task
	c.Data["ResourceUUID"] = GetScheduledTaskResourceUUID(c.Req.Context(), task)
	c.HTML(200, "scheduled_tasks_patch")
}

func (v *ScheduledTaskView) View(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.ParamsInt64(":id")
	task, err := scheduledTaskAdmin.Get(c.Req.Context(), id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}

	permit := memberShip.ValidateOwner(model.Reader, task.Owner)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	c.Data["Task"] = task
	c.Data["ResourceUUID"] = GetScheduledTaskResourceUUID(c.Req.Context(), task)
	c.HTML(200, "scheduled_task_details")
}

func (v *ScheduledTaskView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.ParamsInt64(":id")
	name := c.QueryTrim("name")
	status := c.QueryTrim("status")
	scheduleType := c.QueryTrim("schedule_type")
	executionTimeStr := c.QueryTrim("execution_time")
	clientTZ := parseScheduledTaskTimezoneOffset(c.QueryTrim("timezone_offset_minutes"))
	cronExpression := c.QueryTrim("cron_expression")
	if scheduleType != "one-time" {
		cronExpression = EnsureCronTimezone(cronExpression, c.QueryTrim("client_timezone"))
	}
	retentionCount := c.QueryInt("retention_count")

	task, err := scheduledTaskAdmin.Get(c.Req.Context(), id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}
	permit := memberShip.ValidateOwner(model.Writer, task.Owner)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	var executionTime *time.Time
	if executionTimeStr != "" || scheduleType == "one-time" {
		parsedTime, parseErr := parseScheduledTaskExecutionTime(executionTimeStr, clientTZ)
		if parseErr != nil {
			c.Data["ErrorMsg"] = parseErr.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		executionTime = &parsedTime
	}

	_, err = scheduledTaskAdmin.Update(c.Req.Context(), id, &ScheduledTaskUpdateOptions{
		Name:           &name,
		Status:         &status,
		ScheduleType:   &scheduleType,
		CronExpression: &cronExpression,
		RetentionCount: &retentionCount,
		ExecutionTime:  executionTime,
	})
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}
	c.Redirect("../scheduled_tasks")
}

func (v *ScheduledTaskView) Delete(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.ParamsInt64(":id")
	task, err := scheduledTaskAdmin.Get(c.Req.Context(), id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}
	permit := memberShip.ValidateOwner(model.Writer, task.Owner)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	err = scheduledTaskAdmin.Delete(c.Req.Context(), id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "scheduled_tasks",
	})
}

func (v *ScheduledTaskView) ListHistory(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	listConfig, offset, limit := GetPaginationParams(c, "scheduled_task_history")
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	scheduledTaskID := c.ParamsInt64(":id")
	if scheduledTaskID == 0 {
		scheduledTaskID = c.QueryInt64("task_id")
	}

	total, histories, err := scheduledTaskAdmin.ListHistory(c.Req.Context(), scheduledTaskID, offset, limit, order)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["Histories"] = histories
	SetPaginationData(c, "scheduled_task_history", total, limit, offset, listConfig, "", []string{})
	c.HTML(200, "scheduled_task_history")
}
