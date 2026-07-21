/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package apis

import (
	"context"
	"errors"
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
	ResourceID     string            `json:"resource_id" binding:"required"`                                                               // UUID of the target resource: instance UUID for instance_op tasks, volume UUID for volume_backup tasks
	Operation      model.STaskAction `json:"operation" binding:"required,oneof=stop hard_stop start restart hard_restart snapshot backup"` // Operation to perform
	ScheduleType   string            `json:"schedule_type" binding:"required"`                                                             // Schedule type (one-time, daily, weekly, monthly)
	ExecutionTime  time.Time         `json:"execution_time"`                                                                               // Execution time for one-time tasks
	CronExpression string            `json:"cron_expression"`                                                                              // Cron expression for recurring tasks
	Timezone       string            `json:"timezone"`                                                                                     // IANA timezone name (e.g. Asia/Shanghai) used to evaluate cron_expression for recurring tasks
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
	Timezone       *string    `json:"timezone"`        // IANA timezone name (e.g. Asia/Shanghai) used to evaluate cron_expression for recurring tasks
	RetentionCount *int       `json:"retention_count"` // Updated retention count
}

// ScheduledTaskResponse is the JSON representation of a scheduled task returned by the API.
// The uuid field is the task's globally unique identifier and is the path
// parameter of the get/patch/delete/history endpoints (unique across regions);
// the numeric id is the region-local database identifier, exposed for
// reference only. resource_id is the UUID of the target instance/volume
// (empty if the resource has been deleted).
type ScheduledTaskResponse struct {
	ID             int64             `json:"id"`
	UUID           string            `json:"uuid"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Owner          int64             `json:"owner"`
	Name           string            `json:"name"`
	TaskType       string            `json:"task_type"`
	ResourceType   string            `json:"resource_type"`
	ResourceID     string            `json:"resource_id"`
	Operation      model.STaskAction `json:"operation"`
	ScheduleType   string            `json:"schedule_type"`
	ExecutionTime  time.Time         `json:"execution_time"`
	CronExpression string            `json:"cron_expression"`
	RetentionCount int               `json:"retention_count"`
	Status         string            `json:"status"`
}

// ScheduledTaskAPIError is the unified failure envelope for scheduled task APIs:
// status is always "failed", error_code carries the HTTP status code,
// error_code_str the business error code name (when available) and
// error_message the failure detail.
type ScheduledTaskAPIError struct {
	Status       string `json:"status"`
	ErrorCode    int    `json:"error_code"`
	ErrorCodeStr string `json:"error_code_str,omitempty"`
	ErrorMessage string `json:"error_message"`
}

// scheduledTaskErrorResponse writes the unified failure envelope.
func scheduledTaskErrorResponse(c *gin.Context, code int, errorMsg string, err error) {
	apiErr := &ScheduledTaskAPIError{
		Status:    "failed",
		ErrorCode: code,
	}
	if err != nil {
		var clErr *common.CLError
		if errors.As(err, &clErr) {
			apiErr.ErrorCodeStr = clErr.Code.String()
		}
		errorMsg = errorMsg + ": " + err.Error()
	}
	apiErr.ErrorMessage = errorMsg
	c.JSON(code, apiErr)
}

// scheduledTaskFailureCode maps validation errors (ErrInvalidParameter) to
// HTTP 400, missing-resource errors (ErrResourceNotFound) to HTTP 404, and
// keeps the given fallback code for everything else.
func scheduledTaskFailureCode(err error, fallback int) int {
	var clErr *common.CLError
	if errors.As(err, &clErr) {
		switch clErr.Code {
		case common.ErrInvalidParameter:
			return http.StatusBadRequest
		case common.ErrResourceNotFound:
			return http.StatusNotFound
		}
	}
	return fallback
}

// ScheduledTaskOperationResponse is the success envelope for Create/Patch,
// wrapping the resulting task; the task is nested to avoid clashing with the
// envelope's status field (the task has its own enabled/disabled status).
type ScheduledTaskOperationResponse struct {
	Status       string                 `json:"status"`
	ErrorMessage string                 `json:"error_message"`
	Task         *ScheduledTaskResponse `json:"task"`
}

// ScheduledTaskStatusResponse is the success envelope for operations without
// a result payload (Delete).
type ScheduledTaskStatusResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

// ScheduledTaskListResponse represents the paginated response for listing scheduled tasks.
type ScheduledTaskListResponse struct {
	Status       string                   `json:"status"`
	ErrorMessage string                   `json:"error_message"`
	Total        int64                    `json:"total"`
	Tasks        []*ScheduledTaskResponse `json:"tasks"`
}

// ScheduledTaskHistoryResponse is the JSON representation of a scheduled task execution history record.
// scheduled_task_uuid is the globally unique identifier of the owning task
// (unique across regions); scheduled_task_id is the region-local numeric ID.
type ScheduledTaskHistoryResponse struct {
	ID                int64                  `json:"id"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	ScheduledTaskID   int64                  `json:"scheduled_task_id"`
	ScheduledTaskUUID string                 `json:"scheduled_task_uuid"`
	ScheduledTask     *ScheduledTaskResponse `json:"scheduled_task,omitempty"`
	Status            string                 `json:"status"`
	Message           string                 `json:"message"`
	ExecutionTime     time.Time              `json:"execution_time"`
	Duration          int64                  `json:"duration"`
}

// ScheduledTaskHistoryListResponse represents the paginated response for listing scheduled task execution history.
type ScheduledTaskHistoryListResponse struct {
	Status       string                          `json:"status"`
	ErrorMessage string                          `json:"error_message"`
	Total        int64                           `json:"total"`
	History      []*ScheduledTaskHistoryResponse `json:"history"`
}

// getScheduledTaskResponseWithUUID converts a model.ScheduledTask into its
// JSON response representation using an already-resolved resource UUID
// (no database access).
func getScheduledTaskResponseWithUUID(task *model.ScheduledTask, resourceUUID string) *ScheduledTaskResponse {
	return &ScheduledTaskResponse{
		ID:             task.ID,
		UUID:           task.UUID,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
		Owner:          task.Owner,
		Name:           task.Name,
		TaskType:       task.TaskType,
		ResourceType:   task.ResourceType,
		ResourceID:     resourceUUID,
		Operation:      task.Operation,
		ScheduleType:   task.ScheduleType,
		ExecutionTime:  task.ExecutionTime,
		CronExpression: task.CronExpression,
		RetentionCount: task.RetentionCount,
		Status:         task.Status,
	}
}

// getScheduledTaskResponse converts a single model.ScheduledTask into its JSON
// response representation, resolving the resource UUID with one point query.
// For task lists use routes.GetScheduledTaskResourceUUIDs together with
// getScheduledTaskResponseWithUUID to avoid N+1 queries.
func getScheduledTaskResponse(ctx context.Context, task *model.ScheduledTask) *ScheduledTaskResponse {
	return getScheduledTaskResponseWithUUID(task, routes.GetScheduledTaskResourceUUID(ctx, task))
}

// getScheduledTaskHistoryResponse converts a model.ScheduledTaskHistory into its JSON response representation.
// taskUUID and resourceUUID belong to the owning task the history was queried
// for; they are resolved once by the caller and reused for every record.
func getScheduledTaskHistoryResponse(h *model.ScheduledTaskHistory, taskUUID, resourceUUID string) *ScheduledTaskHistoryResponse {
	resp := &ScheduledTaskHistoryResponse{
		ID:                h.ID,
		CreatedAt:         h.CreatedAt,
		UpdatedAt:         h.UpdatedAt,
		ScheduledTaskID:   h.ScheduledTaskID,
		ScheduledTaskUUID: taskUUID,
		Status:            h.Status,
		Message:           h.Message,
		ExecutionTime:     h.ExecutionTime,
		Duration:          h.Duration,
	}
	if h.ScheduledTask != nil {
		resp.ScheduledTask = getScheduledTaskResponseWithUUID(h.ScheduledTask, resourceUUID)
	}
	return resp
}

// @Summary create a scheduled task
// @Description create a scheduled task for instance operations or volume backup/snapshot.
// @Description The resource_id in the payload is the UUID of the target instance/volume
// @Description (as returned by the instances/volumes APIs). The id field in the response
// @Description is the task's numeric ID, which is the path parameter accepted by the
// @Description get/patch/delete/history endpoints.
// @tags Administration
// @Accept  json
// @Produce json
// @Param   message body   ScheduledTaskPayload  true   "Scheduled task create payload"
// @Success 200 {object} ScheduledTaskOperationResponse
// @Failure 400 {object} ScheduledTaskAPIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 500 {object} ScheduledTaskAPIError "Internal error"
// @Router /scheduled_tasks [post]
func (a *ScheduledTaskAPI) Create(c *gin.Context) {
	logger.Info("[API] Creating new scheduled task - function entry")
	payload := &ScheduledTaskPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("[API] Invalid input JSON during task creation: %v", err)
		scheduledTaskErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	logger.Debugf("[API] Scheduled task payload received: %+v", payload)
	// Resolve the resource UUID from the payload to its internal ID; the
	// database and the scheduler keep working with internal IDs.
	var resourceID int64
	switch payload.ResourceType {
	case "instance":
		instance, err := instanceAdmin.GetInstanceByUUID(c.Request.Context(), payload.ResourceID)
		if err != nil {
			logger.Errorf("[API] Instance %s not found during task creation: %v", payload.ResourceID, err)
			scheduledTaskErrorResponse(c, http.StatusBadRequest, "Invalid resource_id: instance with uuid "+payload.ResourceID+" not found", err)
			return
		}
		resourceID = instance.ID
	case "volume":
		volume, err := volumeAdmin.GetVolumeByUUID(c.Request.Context(), payload.ResourceID)
		if err != nil {
			logger.Errorf("[API] Volume %s not found during task creation: %v", payload.ResourceID, err)
			scheduledTaskErrorResponse(c, http.StatusBadRequest, "Invalid resource_id: volume with uuid "+payload.ResourceID+" not found", err)
			return
		}
		resourceID = volume.ID
	default:
		logger.Errorf("[API] Invalid resource type during task creation: %s", payload.ResourceType)
		scheduledTaskErrorResponse(c, http.StatusBadRequest, "Invalid resource_type: \""+payload.ResourceType+"\" (must be one of: instance, volume)", nil)
		return
	}

	cronExpression := payload.CronExpression
	if payload.ScheduleType != "one-time" {
		cronExpression = routes.EnsureCronTimezone(cronExpression, payload.Timezone)
	}
	task, err := scheduledTaskAdmin.Create(c.Request.Context(), payload.Name, payload.TaskType, payload.ResourceType, payload.Operation, payload.ScheduleType, cronExpression, resourceID, payload.RetentionCount, payload.ExecutionTime)
	if err != nil {
		logger.Errorf("[API] Failed to create scheduled task: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to create scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task created successfully - function exit")
	c.JSON(http.StatusOK, &ScheduledTaskOperationResponse{
		Status:       "success",
		ErrorMessage: "",
		Task:         getScheduledTaskResponse(c.Request.Context(), task),
	})
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
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 500 {object} ScheduledTaskAPIError "Internal error"
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
		scheduledTaskErrorResponse(c, http.StatusInternalServerError, "Failed to list scheduled tasks", err)
		return
	}

	// Resolve all resource UUIDs in batch to avoid one point query per task
	resourceUUIDs := routes.GetScheduledTaskResourceUUIDs(c.Request.Context(), tasks)
	taskResps := make([]*ScheduledTaskResponse, len(tasks))
	for i, t := range tasks {
		taskResps[i] = getScheduledTaskResponseWithUUID(t, resourceUUIDs[t.ID])
	}

	logger.Infof("[API] Successfully found %d scheduled tasks - function exit", total)
	c.JSON(http.StatusOK, ScheduledTaskListResponse{
		Status:       "success",
		ErrorMessage: "",
		Total:        total,
		Tasks:        taskResps,
	})
}

// @Summary get a scheduled task
// @Description get a scheduled task by its UUID. The UUID is the uuid field
// @Description returned by the create/list endpoints and is unique across regions
// @Description (the numeric id is region-local and NOT accepted here).
// @Param   id     path    string     true  "Task UUID (the uuid field returned by create/list)"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} ScheduledTaskAPIError "Not found"
// @Router /scheduled_tasks/{id} [get]
func (a *ScheduledTaskAPI) Get(c *gin.Context) {
	logger.Info("[API] Getting scheduled task - function entry")
	uuID := c.Param("id")
	logger.Debugf("[API] Retrieving task UUID: %s", uuID)

	task, err := scheduledTaskAdmin.GetTaskByUUID(c.Request.Context(), uuID)
	if err != nil {
		logger.Errorf("[API] Scheduled task not found: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to get scheduled task", err)
		return
	}

	logger.Info("[API] Successfully retrieved scheduled task - function exit")
	c.JSON(http.StatusOK, getScheduledTaskResponse(c.Request.Context(), task))
}

// @Summary update a scheduled task
// @Description update an existing scheduled task; only non-empty fields in the payload are updated.
// @Description The path parameter is the task UUID returned by the create/list endpoints,
// @Description unique across regions (the region-local numeric id is NOT accepted here).
// @Param   id      path    string                      true  "Task UUID (the uuid field returned by create/list)"
// @Param   message body    ScheduledTaskPatchPayload   true  "Scheduled task update payload"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskOperationResponse
// @Failure 400 {object} ScheduledTaskAPIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} ScheduledTaskAPIError "Not found"
// @Failure 500 {object} ScheduledTaskAPIError "Internal error"
// @Router /scheduled_tasks/{id} [patch]
func (a *ScheduledTaskAPI) Patch(c *gin.Context) {
	logger.Info("[API] Updating scheduled task - function entry")
	uuID := c.Param("id")
	logger.Debugf("[API] Updating task UUID: %s", uuID)

	payload := &ScheduledTaskPatchPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("[API] Invalid input JSON during task update: %v", err)
		scheduledTaskErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	existing, err := scheduledTaskAdmin.GetTaskByUUID(c.Request.Context(), uuID)
	if err != nil {
		logger.Errorf("[API] Scheduled task not found for update: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to get scheduled task", err)
		return
	}

	logger.Debugf("[API] Patch payload: %+v", payload)
	if payload.CronExpression != nil {
		timezone := ""
		if payload.Timezone != nil {
			timezone = *payload.Timezone
		}
		cronExpression := routes.EnsureCronTimezone(*payload.CronExpression, timezone)
		payload.CronExpression = &cronExpression
	}

	task, err := scheduledTaskAdmin.Update(c.Request.Context(), existing.ID, &routes.ScheduledTaskUpdateOptions{
		Name:           payload.Name,
		Status:         payload.Status,
		ScheduleType:   payload.ScheduleType,
		CronExpression: payload.CronExpression,
		RetentionCount: payload.RetentionCount,
		ExecutionTime:  payload.ExecutionTime,
	})
	if err != nil {
		logger.Errorf("[API] Failed to update scheduled task: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to update scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task updated successfully - function exit")
	c.JSON(http.StatusOK, &ScheduledTaskOperationResponse{
		Status:       "success",
		ErrorMessage: "",
		Task:         getScheduledTaskResponse(c.Request.Context(), task),
	})
}

// @Summary delete a scheduled task
// @Description delete a scheduled task by its UUID. The UUID is the uuid field
// @Description returned by the create/list endpoints and is unique across regions
// @Description (the region-local numeric id is NOT accepted here).
// @Param   id     path    string     true  "Task UUID (the uuid field returned by create/list)"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskStatusResponse
// @Failure 400 {object} ScheduledTaskAPIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} ScheduledTaskAPIError "Not found"
// @Failure 500 {object} ScheduledTaskAPIError "Internal error"
// @Router /scheduled_tasks/{id} [delete]
func (a *ScheduledTaskAPI) Delete(c *gin.Context) {
	logger.Info("[API] Deleting scheduled task - function entry")
	uuID := c.Param("id")
	logger.Debugf("[API] Deleting task UUID: %s", uuID)

	existing, err := scheduledTaskAdmin.GetTaskByUUID(c.Request.Context(), uuID)
	if err != nil {
		logger.Errorf("[API] Scheduled task not found for deletion: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to get scheduled task", err)
		return
	}

	err = scheduledTaskAdmin.Delete(c.Request.Context(), existing.ID)
	if err != nil {
		logger.Errorf("[API] Failed to delete scheduled task: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to delete scheduled task", err)
		return
	}

	logger.Info("[API] Scheduled task deleted successfully - function exit")
	c.JSON(http.StatusOK, &ScheduledTaskStatusResponse{
		Status:       "success",
		ErrorMessage: "",
	})
}

// @Summary list a scheduled task's execution history
// @Description list execution history records for a specific scheduled task.
// @Description The path parameter is the task UUID returned by the create/list endpoints,
// @Description unique across regions (the region-local numeric id is NOT accepted here).
// @Param   id     path  string true  "Task UUID (the uuid field returned by create/list)"
// @Param offset query int    false "Offset"
// @Param limit  query int    false "Limit"
// @Param order  query string false "Order by field"
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} ScheduledTaskHistoryListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} ScheduledTaskAPIError "Not found"
// @Failure 500 {object} ScheduledTaskAPIError "Internal error"
// @Router /scheduled_tasks/{id}/history [get]
func (a *ScheduledTaskAPI) ListHistory(c *gin.Context) {
	logger.Info("[API] Listing scheduled task history - function entry")
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	order := c.Query("order")
	query := c.Query("q")
	uuID := c.Param("id")

	logger.Debugf("[API] ListHistory parameters: offset=%d, limit=%d, order=%s, query=%s, taskUUID=%s", offset, limit, order, query, uuID)

	task, err := scheduledTaskAdmin.GetTaskByUUID(c.Request.Context(), uuID)
	if err != nil {
		logger.Errorf("[API] Scheduled task not found for history listing: %v", err)
		scheduledTaskErrorResponse(c, scheduledTaskFailureCode(err, http.StatusInternalServerError), "Failed to get scheduled task", err)
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

	total, history, err := scheduledTaskHistoryAdmin.List(c.Request.Context(), offset, limit, order, query, task.ID)
	if err != nil {
		logger.Errorf("[API] Failed to list scheduled task history: %v", err)
		scheduledTaskErrorResponse(c, http.StatusInternalServerError, "Failed to list scheduled task history", err)
		return
	}

	// All records belong to the same task: resolve its resource UUID once
	resourceUUID := routes.GetScheduledTaskResourceUUID(c.Request.Context(), task)
	historyResps := make([]*ScheduledTaskHistoryResponse, len(history))
	for i, h := range history {
		historyResps[i] = getScheduledTaskHistoryResponse(h, task.UUID, resourceUUID)
	}

	logger.Infof("[API] Successfully found %d history records - function exit", total)
	c.JSON(http.StatusOK, ScheduledTaskHistoryListResponse{
		Status:       "success",
		ErrorMessage: "",
		Total:        total,
		History:      historyResps,
	})
}
