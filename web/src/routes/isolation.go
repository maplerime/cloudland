package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"

	"web/src/common"
	"web/src/model"
)

type vmIsolationOptions struct {
	RecordUUID string `json:"record_uuid"`
	OperatorID int64  `json:"operator_id"`
	Reason     string `json:"reason"`
}

func QuarantineVM(ctx context.Context, vm, reason string) error {
	return dispatchVMIsolation(ctx, vm, reason, "", "quarantine")
}
func ReleaseVMQuarantine(ctx context.Context, vm, reason, record string) error {
	return dispatchVMIsolation(ctx, vm, reason, record, "release")
}
func dispatchVMIsolation(ctx context.Context, vm, reason, record, action string) error {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 1 || len([]rune(reason)) > 255 {
		return common.NewCLError(common.ErrInvalidParameter, "reason must contain 1 to 255 characters", nil)
	}
	if _, err := uuid.Parse(vm); err != nil {
		return common.NewCLError(common.ErrInvalidParameter, "invalid instance UUID", err)
	}
	inst, err := resolveInspectionInstance(ctx, vm, "")
	if err != nil {
		return err
	}
	_, db := common.GetContextDB(ctx)
	prior := &model.VMInfectionRecord{}
	if record != "" {
		prior, err = GetVMInfection(ctx, record)
		if err != nil {
			return err
		}
		if prior.InstanceUUID != inst.UUID {
			return common.NewCLError(common.ErrInvalidParameter, "record belongs to another VM", nil)
		}
	} else {
		err = db.Where("instance_uuid = ? AND hyper_id = ? AND isolation_status NOT IN (?)", inst.UUID, inst.Hyper, []string{"", "released", "disabled", "not_requested"}).Order("operation_at DESC").Take(prior).Error
		if err != nil && !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	if prior.ID != 0 {
		if prior.HyperID != inst.Hyper {
			return fmt.Errorf("conflict: operation record does not belong to the current compute node")
		}
		record = prior.UUID
	}
	options := vmIsolationOptions{RecordUUID: record, OperatorID: common.GetMemberShip(ctx).UserID, Reason: reason}
	encoded, err := json.Marshal(options)
	if err != nil {
		return err
	}
	script := "quarantine_vm.sh"
	if action == "release" {
		script = "release_vm.sh"
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/%s '%s' '%s' '%s'", script, inst.UUID, inst.Domain, base64.StdEncoding.EncodeToString(encoded))
	if err = common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), command); err != nil {
		return err
	}
	logger.Infof("[VM-INFECTION] VM=%s node=%d action=%s command sent operator=%d", inst.UUID, inst.Hyper, action, options.OperatorID)
	return nil
}

func infectionNeedsRestore(status string) bool {
	switch status {
	case "", "disabled", "not_requested", "released":
		return false
	default:
		return true
	}
}

func (*VMInspectionView) Action(c *macaron.Context) {
	ctx := c.Req.Context()
	vm := c.QueryTrim("instance_uuid")
	reason := c.QueryTrim("reason")
	var err error
	switch c.QueryTrim("action") {
	case "quarantine":
		err = QuarantineVM(ctx, vm, reason)
	case "release":
		err = ReleaseVMQuarantine(ctx, vm, reason, c.QueryTrim("record_uuid"))
	default:
		err = common.NewCLError(common.ErrInvalidParameter, "invalid VM isolation action", nil)
	}
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-infections?" + url.Values{"instance_uuid": {vm}, "accepted": {"1"}}.Encode())
}
