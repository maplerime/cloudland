package rpcs

import (
	"context"
	"fmt"
	"strconv"
	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("create_cg_wds", CreateCGWDS)
	Add("delete_cg_wds", DeleteCGWDS)
}

// CreateCGWDS handles the callback from create_cg_wds.sh script
// 处理创建一致性组脚本的回调
// |:-COMMAND-:| create_cg_wds.sh '<cg_id>' '<status>' '<wds_cg_id>' 'message'
func CreateCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("CreateCGWDS", args)
	if len(args) < 5 {
		logger.Errorf("Invalid args for create_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	wdsCgID := args[3]
	message := args[4]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Update consistency group
	// 更新一致性组
	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}
	err = db.Where(cg).Take(cg).Error
	if err != nil {
		logger.Error("Invalid CG ID", err)
		return
	}

	if status == "available" {
		// Update to available status with WDS CG ID
		// 更新为可用状态并设置 WDS CG ID
		err = db.Model(cg).Updates(map[string]interface{}{
			"status":    model.CGStatusAvailable,
			"wds_cg_id": wdsCgID,
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		logger.Errorf("CG creation failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
		return
	}

	logger.Debugf("Successfully updated consistency group %d to status %s", cgID, status)
	return
}

// DeleteCGWDS handles the callback from delete_cg_wds.sh script
// 处理删除一致性组脚本的回调
// |:-COMMAND-:| delete_cg_wds.sh '<cg_id>' '<status>' 'message'
func DeleteCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("DeleteCGWDS", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for delete_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	message := args[3]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}

	if status == "deleted" {
		// Delete volume associations
		// 删除卷关联
		err = db.Where("cg_id = ?", cgID).Delete(&model.ConsistencyGroupVolume{}).Error
		if err != nil {
			logger.Errorf("Failed to delete CG volume associations: %v", err)
			return
		}

		// Delete consistency group
		// 删除一致性组
		err = db.Delete(cg).Error
		if err != nil {
			logger.Errorf("Failed to delete consistency group %d: %v", cgID, err)
			return
		}
		logger.Debugf("Successfully deleted consistency group %d", cgID)
	} else {
		// Update status to error
		// 更新状态为错误
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		if err != nil {
			logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
			return
		}
		logger.Errorf("CG deletion failed: %s", message)
	}

	return
}
