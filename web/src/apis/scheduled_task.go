package apis

import (
	"net/http"
	"strconv"
	"time"

	"web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var scheduledTaskAPI = &ScheduledTaskAPI{}
var scheduledTaskAdmin = &routes.ScheduledTaskAdmin{}
var scheduledTaskHistoryAdmin = &routes.ScheduledTaskHistoryAdmin{}

// ScheduledTaskAPI provides RESTful API endpoints for scheduled task management.
// It handles CRUD operations and execution history for automated tasks.
type ScheduledTaskAPI struct{}

// ScheduledTaskPayload represents the JSON payload for creating a new scheduled task.
// All fields are validated according to the binding constraints.
type ScheduledTaskPayload struct {
	Name           string            `json:"name" binding:"required"`                                                                      // Human-readable task name
	TaskType       string            `json:"task_type" binding:"required"`                                                                 // Type of task (instance_op, volume_backup)
	ResourceType   string            `json:"resource_type" binding:"required"`                                                             // Type of resource (instance, volume)
	ResourceID     int64             `json:"resource_id" binding:"required"`                                                               // Target resource ID
	Operation      model.STaskAction `json:"operation" binding:"required,oneof=stop hard_stop start restart hard_restart snapshot backup"` // Operation to perform
	ScheduleType   string            `json:"schedule_type" binding:"required"`                                                             // Schedule type (one-time, daily, weekly, monthly)
	ExecutionTime  time.Time         `json:"execution_time"`                                                                               // Execution time for one-time tasks
	CronExpression string            `json:"cron_expression"`                                                                              // Cron expression for recurring tasks
	RetentionCount int               `json:"retention_count"`                                                                              // Number of backups/snapshots to retain
}

// ScheduledTaskPatchPayload represents the JSON payload for updating an existing scheduled task.
// Only non-empty fields will be updated in the database.
type ScheduledTaskPatchPayload struct {
	Name           string    `json:"name"`                             // Updated task name
	Status         string    `json:"status"`                           // Updated status (enabled/disabled)
	ScheduleType   string    `json:"schedule_type" binding:"required"` // Updated schedule type
	ExecutionTime  time.Time `json:"execution_time"`                   // Updated execution time
	CronExpression string    `json:"cron_expression"`                  // Updated cron expression
	RetentionCount int       `json:"retention_count"`                  // Updated retention count
}

// Create creates a new scheduled task with the provided parameters.
// Returns HTTP 201 on success or appropriate error status on failure.
func (a *ScheduledTaskAPI) Create(c *gin.Context) {
	logger.Info("[API] Creating new scheduled task - function entry")
	payload := &ScheduledTaskPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("[API] Invalid input JSON during task creation: %v", err)
		common.ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	logger.Debugf("[API] Scheduled task payload received: %+v", payload)
	_, err = scheduledTaskAdmin.Create(c.Request.Context(), payload.Name, payload.TaskType, payload.ResourceType, payload.Operation, payload.ScheduleType, payload.CronExpression, payload.ResourceID, payload.RetentionCount, payload.ExecutionTime)
	if err != nil {
		logger.Errorf("[API] Failed to create scheduled task: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to create scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task created successfully - function exit")
	c.JSON(http.StatusOK, nil)
}

// List retrieves a paginated list of scheduled tasks with optional search filtering.
// Supports offset, limit, ordering, and text search parameters.
func (a *ScheduledTaskAPI) List(c *gin.Context) {
	logger.Info("[API] Listing scheduled tasks - function entry")
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	order := c.Query("order")
	query := c.Query("q")

	logger.Debugf("[API] List parameters: offset=%d, limit=%d, order=%s, query=%s", offset, limit, order, query)
	total, tasks, err := scheduledTaskAdmin.List(c.Request.Context(), offset, limit, order, query)
	if err != nil {
		logger.Errorf("[API] Failed to list scheduled tasks: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to list scheduled tasks", err)
		return
	}

	logger.Infof("[API] Successfully found %d scheduled tasks - function exit", total)
	c.JSON(http.StatusOK, gin.H{
		"total": total,
		"tasks": tasks,
	})
}

// Get retrieves a single scheduled task by its ID.
// Returns HTTP 404 if the task doesn't exist or user doesn't have access.
func (a *ScheduledTaskAPI) Get(c *gin.Context) {
	logger.Info("[API] Getting scheduled task - function entry")
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	logger.Debugf("[API] Retrieving task ID: %d", id)

	task, err := scheduledTaskAdmin.Get(c.Request.Context(), id)
	if err != nil {
		logger.Errorf("[API] Scheduled task not found: %v", err)
		common.ErrorResponse(c, http.StatusNotFound, "Scheduled task not found", err)
		return
	}

	logger.Info("[API] Successfully retrieved scheduled task - function exit")
	c.JSON(http.StatusOK, task)
}

// Patch updates an existing scheduled task with the provided parameters.
// Only non-empty fields in the payload will be updated.
func (a *ScheduledTaskAPI) Patch(c *gin.Context) {
	logger.Info("[API] Updating scheduled task - function entry")
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	logger.Debugf("[API] Updating task ID: %d", id)

	payload := &ScheduledTaskPatchPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("[API] Invalid input JSON during task update: %v", err)
		common.ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	logger.Debugf("[API] Patch payload: %+v", payload)
	_, err = scheduledTaskAdmin.Update(c.Request.Context(), id, payload.Name, payload.Status,
		payload.ScheduleType, payload.CronExpression, payload.RetentionCount, payload.ExecutionTime)
	if err != nil {
		logger.Errorf("[API] Failed to update scheduled task: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to update scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task updated successfully - function exit")
	c.JSON(http.StatusOK, nil)
}

// Delete removes a scheduled task by its ID.
// Returns HTTP 204 on successful deletion.
func (a *ScheduledTaskAPI) Delete(c *gin.Context) {
	logger.Info("[API] Deleting scheduled task - function entry")
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	logger.Debugf("[API] Deleting task ID: %d", id)

	err := scheduledTaskAdmin.Delete(c.Request.Context(), id)
	if err != nil {
		logger.Errorf("[API] Failed to delete scheduled task: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to delete scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task deleted successfully - function exit")
	c.JSON(http.StatusNoContent, nil)
}

// ListHistory retrieves the execution history for a specific scheduled task.
// Supports pagination and ordering of history records.
func (a *ScheduledTaskAPI) ListHistory(c *gin.Context) {
	logger.Info("[API] Listing scheduled task history - function entry")
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	order := c.Query("order")
	query := c.Query("q")
	scheduledTaskID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	logger.Debugf("[API] ListHistory parameters: offset=%d, limit=%d, order=%s, query=%s, scheduledTaskID=%d", offset, limit, order, query, scheduledTaskID)

	// Input validation
	if scheduledTaskID <= 0 {
		logger.Error("[API] Invalid scheduled task ID provided")
		common.ErrorResponse(c, http.StatusBadRequest, "Invalid scheduled task ID", nil)
		return
	}

	// Set default values for pagination
	if limit <= 0 {
		limit = 20 // Default limit
		logger.Debug("[API] Using default limit of 20")
	}
	if offset < 0 {
		offset = 0 // Default offset
		logger.Debug("[API] Reset negative offset to 0")
	}
	if order == "" {
		order = "-created_at" // Default order by created_at descending
		logger.Debug("[API] Using default order: -created_at")
	}

	total, history, err := scheduledTaskHistoryAdmin.List(c.Request.Context(), offset, limit, order, query, scheduledTaskID)
	if err != nil {
		logger.Errorf("[API] Failed to list scheduled task history: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to list scheduled task history", err)
		return
	}

	logger.Infof("[API] Successfully found %d history records - function exit", total)
	c.JSON(http.StatusOK, gin.H{
		"total":   total,
		"history": history,
	})
}
