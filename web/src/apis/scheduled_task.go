/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
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
	Name           *string    `json:"name"`            // Updated task name
	Status         *string    `json:"status"`          // Updated status (enabled/disabled)
	ScheduleType   *string    `json:"schedule_type"`   // Updated schedule type
	ExecutionTime  *time.Time `json:"execution_time"`  // Updated execution time
	CronExpression *string    `json:"cron_expression"` // Updated cron expression
	RetentionCount *int       `json:"retention_count"` // Updated retention count
}

// ScheduledTaskResponse is a flat, swagger-documentation-only mirror of model.ScheduledTask.
// It exists because model.Model embeds an *Organization which itself embeds model.Model,
// a cycle swag cannot statically resolve; the actual endpoints still serialize the real
// model.ScheduledTask value as-is, this type only describes its shape for the docs.
type ScheduledTaskResponse struct {
	ID             int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	UUID           string
	Owner          int64
	Name           string
	TaskType       string
	ResourceType   string
	ResourceID     int64
	Operation      model.STaskAction
	ScheduleType   string
	ExecutionTime  time.Time
	CronExpression string
	RetentionCount int
	Status         string
}

// ScheduledTaskListResponse represents the paginated response for listing scheduled tasks.
type ScheduledTaskListResponse struct {
	Total int64                    `json:"total"`
	Tasks []*ScheduledTaskResponse `json:"tasks"`
}

// ScheduledTaskHistoryResponse is a flat, swagger-documentation-only mirror of
// model.ScheduledTaskHistory (see ScheduledTaskResponse for why this is needed).
type ScheduledTaskHistoryResponse struct {
	ID              int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ScheduledTaskID int64
	Status          string
	Message         string
	ExecutionTime   time.Time
	Duration        int64
}

// ScheduledTaskHistoryListResponse represents the paginated response for listing scheduled task execution history.
type ScheduledTaskHistoryListResponse struct {
	Total   int64                           `json:"total"`
	History []*ScheduledTaskHistoryResponse `json:"history"`
}

// @Summary create a scheduled task
// @Description create a scheduled task for instance operations or volume backup/snapshot
// @tags Administration
// @Accept  json
// @Produce json
// @Param   message body   ScheduledTaskPayload  true   "Scheduled task create payload"
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /scheduled_tasks [post]
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

// @Summary list scheduled tasks
// @Description list scheduled tasks with optional search filtering
// @Param offset query int    false "Offset"
// @Param limit  query int    false "Limit"
// @Param order  query string false "Order by field"
// @Param q      query string false "Search query on task name"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /scheduled_tasks [get]
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

// @Summary get a scheduled task
// @Description get a scheduled task by its ID
// @Param   id     path    int     true  "Scheduled task ID"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} common.APIError "Not found"
// @Router /scheduled_tasks/{id} [get]
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

// @Summary update a scheduled task
// @Description update an existing scheduled task; only non-empty fields in the payload are updated
// @Param   id      path    int                         true  "Scheduled task ID"
// @Param   message body    ScheduledTaskPatchPayload   true  "Scheduled task update payload"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /scheduled_tasks/{id} [patch]
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
	_, err = scheduledTaskAdmin.Update(c.Request.Context(), id, &routes.ScheduledTaskUpdateOptions{
		Name:           payload.Name,
		Status:         payload.Status,
		ScheduleType:   payload.ScheduleType,
		CronExpression: payload.CronExpression,
		RetentionCount: payload.RetentionCount,
		ExecutionTime:  payload.ExecutionTime,
	})
	if err != nil {
		logger.Errorf("[API] Failed to update scheduled task: %v", err)
		common.ErrorResponse(c, http.StatusInternalServerError, "Failed to update scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task updated successfully - function exit")
	c.JSON(http.StatusOK, nil)
}

// @Summary delete a scheduled task
// @Description delete a scheduled task by its ID
// @Param   id     path    int     true  "Scheduled task ID"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /scheduled_tasks/{id} [delete]
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

// @Summary list a scheduled task's execution history
// @Description list execution history records for a specific scheduled task
// @Param   id     path  int    true  "Scheduled task ID"
// @Param offset query int    false "Offset"
// @Param limit  query int    false "Limit"
// @Param order  query string false "Order by field"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskHistoryListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /scheduled_tasks/{id}/history [get]
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
