package rpcs

import (
	"context"
	"fmt"
	"strconv"
	"web/src/routes"
)

func init() {
	Add("vol_snapshot_wds_vhost", BackupVolumeWDSVhost)
}

func BackupVolumeWDSVhost(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| backup_volume_wds_vhost.sh 5 /volume-12.disk available reason
	logger.Debug("BackupVolumeWDSVhost", args)
	if len(args) < 5 {
		logger.Errorf("Invalid args for vol_snapshot_wds_vhost: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	backupID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup ID: %v", args[1])
		return
	}
	state := args[2]
	path := args[3]
	// message := args[4]
	backupAdmin := &routes.BackupAdmin{}
	_, err = backupAdmin.Update(ctx, backupID, "", path, state)
	if err != nil {
		logger.Errorf("Failed to update backup %d: %v", backupID, err)
		return "", err
	}
	return
}
