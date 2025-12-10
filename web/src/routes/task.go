/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"
	"net/http"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"gopkg.in/macaron.v1"
)

var (
	taskAdmin = &TaskAdmin{}
	taskView  = &TaskView{}
)

type TaskAdmin struct{}
type TaskView struct{}

func (a *TaskAdmin) Get(ctx context.Context, taskID int64) (task *model.Task, err error) {
	if taskID <= 0 {
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid task ID: %d", taskID), nil)
		logger.Error(err)
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	task = &model.Task{Model: model.Model{ID: taskID}}
	if err = db.Where(where).Take(task).Error; err != nil {
		logger.Error("DB: query task failed", err)
		err = NewCLError(ErrTaskNotFound, "Task not found", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, task.Owner)
	if !permit {
		logger.Error("Not authorized to read the task")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the task", nil)
		return
	}
	return
}

func (a *TaskAdmin) GetTaskByUUID(ctx context.Context, uuid string) (task *model.Task, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	task = &model.Task{}
	err = db.Where(where).Where("uuid = ?", uuid).Take(task).Error
	if err != nil {
		logger.Error("DB: query task failed", err)
		err = NewCLError(ErrTaskNotFound, "Task not found", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, task.Owner)
	if !permit {
		logger.Error("Not authorized to read the task")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the task", nil)
		return
	}
	return
}

func (a *TaskAdmin) List(ctx context.Context, offset, limit int64, order string, query string, source string) (total int64, tasks []*model.Task, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	where := memberShip.GetWhere()
	source_where := ""
	if source == string(model.TaskSourceManual) {
		source_where = fmt.Sprintf("source='%s'", source)
	} else if source == string(model.TaskSourceScheduler) {
		source_where = fmt.Sprintf("source='%s'", source)
	} else if source == string(model.TaskSourceMigration) {
		source_where = fmt.Sprintf("source='%s'", source)
	} else if source == "not_migration" || source == "" { // show all tasks except migration tasks
		source_where = fmt.Sprintf("source!='%s'", string(model.TaskSourceMigration))
	} else if source == "all" {
		source_where = ""
	} else {
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid task source %s", source), nil)
		return
	}

	tasks = []*model.Task{}
	if err = db.Model(&model.Task{}).Where(where).Where(query).Where(source_where).Count(&total).Error; err != nil {
		logger.Error("DB: count tasks failed", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to count tasks", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(source_where).Where(where).Where(query).Find(&tasks).Error; err != nil {
		logger.Error("DB: query tasks failed", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query tasks", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, task := range tasks {
			task.OwnerInfo = &model.Organization{Model: model.Model{ID: task.Owner}}
			if err = db.Take(task.OwnerInfo).Error; err != nil {
				logger.Error("DB: query owner info failed", err)
				err = NewCLError(ErrOwnerNotFound, "Owner organization not found", err)
				return
			}
		}
	}

	return
}

// TaskView

func (v *TaskView) Get(c *macaron.Context, store session.Store) {
	taskID := c.ParamsInt64("id")
	task, err := taskAdmin.Get(c.Req.Context(), taskID)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Data["Task"] = task
	c.HTML(http.StatusOK, "task_details")
}

func (v *TaskView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
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
	source := c.QueryTrim("source")
	total, tasks, err := taskAdmin.List(c.Req.Context(), offset, limit, order, query, source)
	if err != nil {
		logger.Error("DB: query tasks failed", err)
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	pages := GetPages(total, limit)
	c.Data["Tasks"] = tasks
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.Data["Source"] = source
	c.HTML(http.StatusOK, "tasks")
}
