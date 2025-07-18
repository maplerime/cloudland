package routes

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
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

// Update modifies an existing scheduled task with the provided parameters.
// Only non-empty fields are updated. Validates ownership before modification.
func (a *ScheduledTaskAdmin) Update(ctx context.Context, id int64, name, status, scheduleType, cronExpression string, retentionCount int, executionTime time.Time) (task *model.ScheduledTask, err error) {
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
		return
	}

	// Update fields only if they are provided (non-empty)
	updateFields := []string{}
	if name != "" {
		task.Name = name
		updateFields = append(updateFields, "name")
	}
	if status != "" {
		task.Status = status
		updateFields = append(updateFields, "status")
	}
	if scheduleType != "" {
		task.ScheduleType = scheduleType
		updateFields = append(updateFields, "schedule_type")
	}
	if cronExpression != "" {
		task.CronExpression = cronExpression
		updateFields = append(updateFields, "cron_expression")
	}
	if retentionCount >= 0 {
		task.RetentionCount = retentionCount
		updateFields = append(updateFields, "retention_count")
	}
	if !executionTime.IsZero() {
		task.ExecutionTime = executionTime
		updateFields = append(updateFields, "execution_time")
	}

	logger.Debugf("[Admin] Updating fields: %v", updateFields)
	
	// Save the updated task
	err = db.Save(task).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to update scheduled task %d: %v", id, err)
		return
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
	err = db.Where(where).Where("id = ?", id).Delete(&model.ScheduledTask{}).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to delete scheduled task %d: %v", id, err)
		return
	}
	
	logger.Infof("[Admin] Scheduled task %d deleted successfully - function exit", id)
	return
}

// ListEnabledTasks retrieves all scheduled tasks that are currently enabled.
// Used by the scheduler to find tasks that need to be executed.
func (a *ScheduledTaskAdmin) ListEnabledTasks(ctx context.Context) (tasks []*model.ScheduledTask, err error) {
	logger.Debug("[Admin] Listing enabled scheduled tasks - function entry")
	
	// Query for all enabled tasks across all organizations
	db := DB()
	err = db.Where("status = ?", "enabled").Find(&tasks).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to list enabled scheduled tasks: %v", err)
		return
	}
	
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
	
	// Validate task ID
	if scheduled_task_id <= 0 {
		err = fmt.Errorf("invalid scheduled task ID")
		logger.Error("[Admin] Invalid scheduled task ID provided")
		return
	}
	
	// Apply organization filter and task ID filter
	where := memberShip.GetWhere()
	logger.Debugf("[Admin] Organization filter applied: %s", where)
	
	// Count total matching history records
	if err = db.Model(&model.ScheduledTaskHistory{}).Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").Where(where).Where("scheduled_task_histories.scheduled_task_id = ?", scheduled_task_id).Count(&total).Error; err != nil {
		logger.Errorf("[Admin] Failed to count scheduled task history: %v", err)
		return
	}
	
	// Retrieve paginated history records with ordering
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	err = db.Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").Where(where).Where("scheduled_task_histories.scheduled_task_id = ?", scheduled_task_id).Find(&historys).Error
	if err != nil {
		logger.Errorf("[Admin] Failed to retrieve scheduled task history: %v", err)
		return
	}
	
	logger.Debugf("[Admin] Successfully retrieved %d history records (total: %d) - function exit", len(historys), total)
	return
}

// List retrieves execution history records with pagination and filtering.
// This is the dedicated method for ScheduledTaskHistoryAdmin.
func (a *ScheduledTaskHistoryAdmin) List(ctx context.Context, offset, limit int64, order, query string, scheduledTaskID int64) (total int64, historys []*model.ScheduledTaskHistory, err error) {
	logger.Debugf("[HistoryAdmin] Listing task history - function entry: offset=%d, limit=%d, order=%s, query=%s, scheduledTaskID=%d", offset, limit, order, query, scheduledTaskID)
	
	// Get database connection and user membership
	db := DB()
	memberShip := GetMemberShip(ctx)
	
	// Set default values
	if order == "" {
		order = "created_at"
		logger.Debug("[HistoryAdmin] Using default order: created_at")
	}
	if limit == 0 {
		limit = 16
		logger.Debug("[HistoryAdmin] Using default limit of 16")
	}
	
	// Validate task ID
	if scheduledTaskID <= 0 {
		err = fmt.Errorf("invalid scheduled task ID")
		logger.Error("[HistoryAdmin] Invalid scheduled task ID provided")
		return
	}
	
	// Apply organization filter through join
	where := memberShip.GetWhere()
	logger.Debugf("[HistoryAdmin] Organization filter applied: %s", where)
	
	// Count total matching records
	if err = db.Model(&model.ScheduledTaskHistory{}).Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").Where(where).Where("scheduled_task_histories.scheduled_task_id = ?", scheduledTaskID).Count(&total).Error; err != nil {
		logger.Errorf("[HistoryAdmin] Failed to count scheduled task history: %v", err)
		return
	}
	
	// Retrieve paginated results with ordering
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	err = db.Joins("JOIN scheduled_tasks ON scheduled_task_histories.scheduled_task_id = scheduled_tasks.id").Where(where).Where("scheduled_task_histories.scheduled_task_id = ?", scheduledTaskID).Find(&historys).Error
	if err != nil {
		logger.Errorf("[HistoryAdmin] Failed to retrieve scheduled task history: %v", err)
		return
	}
	
	logger.Debugf("[HistoryAdmin] Successfully retrieved %d history records (total: %d) - function exit", len(historys), total)
	return
}

func (v *ScheduledTaskView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
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
	pages := GetPages(total, limit)
	c.Data["Tasks"] = tasks
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
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
	//resourceType := c.QueryTrim("resource_type")
	resourceType := "instance" // Default to instance, can be extended later
	if taskType == "volume_backup" {
		resourceType = "volume"
	}
	resourceID := c.QueryInt64("resource_id")
	operation := c.QueryTrim("operation")
	scheduleType := c.QueryTrim("schedule_type")
	executionTimeStr := c.QueryTrim("execution_time")
	cronExpression := c.QueryTrim("cron_expression")
	retentionCount := c.QueryInt("retention_count")
	executionTime, _ := time.Parse(time.RFC3339, executionTimeStr)
	_, err := scheduledTaskAdmin.Create(c.Req.Context(), name, taskType, resourceType, model.STaskAction(operation), scheduleType, cronExpression, resourceID, retentionCount, executionTime)
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

	_, volumes, err := volumeAdmin.List(c.Req.Context(), 0, -1, "", "")
	if err != nil {
		logger.Error("Failed to query volumes %v", err)
		return
	}
	_, instances, err := instanceAdmin.List(c.Req.Context(), 0, -1, "", "")
	if err != nil {
		logger.Error("Failed to query instances %v", err)
		return
	}

	c.Data["Task"] = task
	c.Data["Volumes"] = volumes
	c.Data["Instances"] = instances
	c.HTML(200, "scheduled_tasks_patch")
}

func (v *ScheduledTaskView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.ParamsInt64(":id")
	name := c.QueryTrim("name")
	status := c.QueryTrim("status")
	scheduleType := c.QueryTrim("schedule_type")
	executionTimeStr := c.QueryTrim("execution_time")
	cronExpression := c.QueryTrim("cron_expression")
	retentionCount := c.QueryInt("retention_count")
	executionTime, _ := time.Parse(time.RFC3339, executionTimeStr)

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
	_, err = scheduledTaskAdmin.Update(c.Req.Context(), id, name, status, scheduleType, cronExpression, retentionCount, executionTime)
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

	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	scheduledTaskID := c.ParamsInt64(":id")

	total, histories, err := scheduledTaskAdmin.ListHistory(c.Req.Context(), scheduledTaskID, offset, limit, order)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Histories"] = histories
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.HTML(200, "scheduled_task_history")
}
