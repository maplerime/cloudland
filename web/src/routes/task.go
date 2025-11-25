/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
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
		source_where = fmt.Sprintf("source=%s", source)
	} else if source == string(model.TaskSourceScheduler) {
		source_where = fmt.Sprintf("source=%s", source)
	} else if source == string(model.TaskSourceMigration) {
		source_where = fmt.Sprintf("source=%s", source)
	} else if source == "not_migration" || source == "" { // show all tasks except migration tasks
		source_where = fmt.Sprintf("source != %s", string(model.TaskSourceMigration))
	} else if source == "all" {
		source_where = ""
	} else {
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid task source %s", source), nil)
		return
	}

	tasks = []*model.Task{}
	if source_where != "" {
		if err = db.Model(&model.Task{}).Where(where).Where(query).Where(source_where).Count(&total).Error; err != nil {
			err = NewCLError(ErrSQLSyntaxError, "Failed to count tasks", err)
			return
		}
	} else {
		if err = db.Model(&model.Task{}).Where(where).Where(query).Count(&total).Error; err != nil {
			err = NewCLError(ErrSQLSyntaxError, "Failed to count tasks", err)
			return
		}
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Instance").Where(where).Where(query).Find(&tasks).Error; err != nil {
		err = NewCLError(ErrSQLSyntaxError, "Failed to query tasks", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, task := range tasks {
			task.OwnerInfo = &model.Organization{Model: model.Model{ID: task.Owner}}
			if err = db.Take(task.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				err = NewCLError(ErrOwnerNotFound, "Owner organization not found", err)
				return
			}
		}
	}

	return
}
