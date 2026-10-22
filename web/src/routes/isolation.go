package routes

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"

	"web/src/common"
	"web/src/model"
)

func QuarantineVM(ctx context.Context, vm string) error {
	return dispatchVMIsolation(ctx, vm, "quarantine")
}
func ReleaseVMQuarantine(ctx context.Context, vm string) error {
	return dispatchVMIsolation(ctx, vm, "release")
}
func dispatchVMIsolation(ctx context.Context, vm, action string) error {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return err
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
	err = db.Where("instance_uuid = ? AND hyper_id = ?", inst.UUID, inst.Hyper).Order("id DESC").Take(prior).Error
	if err != nil && !gorm.IsRecordNotFoundError(err) {
		return err
	}
	if action == "release" && (prior.ID == 0 || !infectionNeedsRestore(prior.IsolationStatus)) {
		return fmt.Errorf("conflict: VM has no isolation to release")
	}
	if action == "quarantine" && prior.IsolationStatus == "isolated" {
		return fmt.Errorf("conflict: VM is already isolated")
	}
	script := "quarantine_vm.sh"
	if action == "release" {
		script = "release_vm.sh"
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/%s '%s'", script, inst.Domain)
	if err = common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), command); err != nil {
		return err
	}
	logger.Infof("[VM-INFECTION] VM=%s node=%d action=%s command sent operator=%d", inst.UUID, inst.Hyper, action, common.GetMemberShip(ctx).UserID)
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
	var err error
	switch c.QueryTrim("action") {
	case "quarantine":
		err = QuarantineVM(ctx, vm)
	case "release":
		err = ReleaseVMQuarantine(ctx, vm)
	default:
		err = common.NewCLError(common.ErrInvalidParameter, "invalid VM isolation action", nil)
	}
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-infections?" + url.Values{"instance_uuid": {vm}, "accepted": {"1"}}.Encode())
}
