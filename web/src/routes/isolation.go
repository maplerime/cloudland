package routes

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	macaron "gopkg.in/macaron.v1"

	"web/src/common"
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
	// Manual actions use the current assignment, independent of historical scan status.
	inst, err := resolveInspectionInstance(ctx, vm, "")
	if err != nil {
		return err
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
