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

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var taskAPI = &TaskAPI{}
var taskAdmin = &routes.TaskAdmin{}

type TaskAPI struct{}

type CLTaskResponse struct {
	*ResourceReference
	Source    string `json:"source"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Action    string `json:"action"`
	Resources string `json:"resources"`
}

type CLTaskListResponse struct {
	Offset int               `json:"offset"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Tasks  []*CLTaskResponse `json:"tasks"`
}

// @Summary get a task
// @Description get a task
// @tags Compute
// @Accept  json
// @Produce json
// @Param   id     path    string     true  "Task UUID"
// @Success 200 {object} CLTaskResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /tasks/{id} [get]
func (a *TaskAPI) Get(c *gin.Context) {
	taskUUID := c.Param("id")
	task, err := taskAdmin.GetTaskByUUID(context.Background(), taskUUID)
	if err != nil {
		logger.Errorf("Failed to get task by uuid: %s, %+v", taskUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid task query", err)
		return
	}
	taskResp, err := a.getTaskResponse(context.Background(), task)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, taskResp)
}

// @Summary list tasks
// @Description list tasks
// @Param source query    string     true  "Source: empty or manual or scheduler or migration or not_migration or all"
// @Param offset query    int        true  "Offset"
// @Param limit query    int        true  "Limit"
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} CLTaskListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /tasks [get]
func (a *TaskAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	nameStr := c.DefaultQuery("name", "")
	sourceStr := c.DefaultQuery("source", "")
	logger.Debugf("List volumes, offset:%s, limit:%s, name:%s, source:%s", offsetStr, limitStr, nameStr, sourceStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset: %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit: %s, %+v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		errStr := "Invalid query offset or limit, cannot be negative"
		logger.Errorf(errStr)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", errors.New(errStr))
		return
	}
	total, tasks, err := taskAdmin.List(ctx, int64(offset), int64(limit), "-created_at", c.DefaultQuery("query", ""), c.DefaultQuery("source", ""))
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list tasks", err)
		return
	}
	taskListResp := &CLTaskListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(tasks),
	}
	taskListResp.Tasks = make([]*CLTaskResponse, taskListResp.Limit)
	for i, task := range tasks {
		taskListResp.Tasks[i], err = a.getTaskResponse(ctx, task)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	c.JSON(http.StatusOK, taskListResp)
}

func (a *TaskAPI) getTaskResponse(ctx context.Context, task *model.Task) (*CLTaskResponse, error) {
	owner := orgAdmin.GetOrgName(ctx, task.Owner)
	resp := &CLTaskResponse{
		ResourceReference: &ResourceReference{
			ID:        task.UUID,
			Name:      task.Name,
			Owner:     owner,
			CreatedAt: task.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: task.UpdatedAt.Format(TimeStringForMat),
		},
		Source:    string(task.Source),
		Summary:   task.Summary,
		Status:    string(task.Status),
		Message:   task.Message,
		Action:    string(task.Action),
		Resources: task.Resources,
	}
	return resp, nil
}
