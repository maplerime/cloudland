package apis

import (
	"context"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var volBackupAPI = &VolBackupAPI{}
var volBackupAdmin = &routes.BackupAdmin{}

type VolBackupAPI struct{}

type VolBackupPayload struct {
	Name     string `json:"name" binding:"required"`
	VolumeID string `json:"volume_id" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=snapshot backup"`
}

type VolBackupResponse struct {
	*ResourceReference
	Volume   *BaseReference `json:"volume"`
	Status   string         `json:"status"`
	BackupID string         `json:"backup_id"`
}

type VolBackupListResponse struct {
	Offset  int                  `json:"offset"`
	Total   int                  `json:"total"`
	Limit   int                  `json:"limit"`
	Backups []*VolBackupResponse `json:"backups"`
}

func (v *VolBackupAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &VolBackupPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	volume, err := volumeAdmin.GetVolumeByUUID(ctx, payload.VolumeID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid volume id", err)
		return
	}
	var backup *model.VolumeBackup
	if payload.Type == "snapshot" {
		backup, err = volBackupAdmin.CreateSnapshotByUUID(ctx, volume.UUID, payload.Name)
	} else {
		backup, err = volBackupAdmin.CreateBackupByUUID(ctx, volume.UUID, "", payload.Name)
	}
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to create backup", err)
		return
	}
	backupResp, err := v.getVolBackupResponse(ctx, backup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, backupResp)
}

func (v *VolBackupAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	volumeUUID := c.Param("id")
	backupType := c.Param("backup_type")
	if backupType != "snapshot" && backupType != "backup" {
		ErrorResponse(c, http.StatusBadRequest, "Invalid backup type", nil)
		return
	}

	volume, err := volumeAdmin.GetVolumeByUUID(ctx, volumeUUID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid volume id", err)
		return
	}
	if volume == nil {
		ErrorResponse(c, http.StatusNotFound, "Volume not found", nil)
		return
	}
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	total, backups, err := volBackupAdmin.List(ctx, int64(offset), int64(limit), "-created_at", "", volume.ID, backupType)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list backups", err)
		return
	}
	backupListResp := &VolBackupListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(backups),
	}
	backupListResp.Backups = make([]*VolBackupResponse, backupListResp.Limit)
	for i, backup := range backups {
		backupListResp.Backups[i], err = v.getVolBackupResponse(ctx, backup)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	c.JSON(http.StatusOK, backupListResp)
}

func (v *VolBackupAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	backup, err := volBackupAdmin.GetBackupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid backup query", err)
		return
	}
	backupResp, err := v.getVolBackupResponse(ctx, backup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, backupResp)
}

func (v *VolBackupAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	backup, err := volBackupAdmin.GetBackupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = volBackupAdmin.Delete(ctx, backup)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (v *VolBackupAPI) Restore(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	backup, err := volBackupAdmin.GetBackupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid backup query", err)
		return
	}
	err = volBackupAdmin.Restore(ctx, backup.ID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to restore backup", err)
		return
	}
	c.JSON(http.StatusOK, nil)
}

func (v *VolBackupAPI) getVolBackupResponse(ctx context.Context, backup *model.VolumeBackup) (backupResp *VolBackupResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, backup.Owner)
	backupResp = &VolBackupResponse{
		ResourceReference: &ResourceReference{
			ID:        backup.UUID,
			Name:      backup.Name,
			Owner:     owner,
			CreatedAt: backup.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: backup.UpdatedAt.Format(TimeStringForMat),
		},
		Status:   backup.Status,
		BackupID: backup.UUID,
	}
	if backup.Volume != nil {
		backupResp.Volume = &BaseReference{
			ID:   backup.Volume.UUID,
			Name: backup.Volume.Name,
		}
	}
	return
}
