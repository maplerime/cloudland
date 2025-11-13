/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"
	"web/src/utils"

	"github.com/gin-gonic/gin"
)

var imageAPI = &ImageAPI{}
var imageAdmin = &routes.ImageAdmin{}
var imageStorageAdmin = &routes.ImageStorageAdmin{}

type ImageAPI struct{}

type ImageResponse struct {
	*ResourceReference
	OSCode       string `json:"os_code"`
	OSVersion    string `json:"os_version"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
	Architecture string `json:"architecture"`
	User         string `json:"user"`
	Status       string `json:"status"`
	BootLoader   string `json:"boot_loader"`
	OsFamily     string `json:"os_family"`
	// QAEnabled    bool   `json:"qa_enabled"`
}

type ImageListResponse struct {
	Offset int              `json:"offset"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Images []*ImageResponse `json:"images"`
}

type ImagePayload struct {
	UUID         string         `json:"uuid,omitempty" binding:"omitempty"`
	Name         string         `json:"name" binding:"required,min=2,max=32"`
	OSCode       string         `json:"os_code" binding:"required,oneof=linux windows other"`
	DownloadURL  string         `json:"download_url" binding:"required,http_url"`
	OSVersion    string         `json:"os_version" binding:"required,min=2,max=32"`
	User         string         `json:"user" binding:"required,min=2,max=32"`
	InstanceUUID string         `json:"instance_uuid"`
	BootLoader   string         `json:"boot_loader" binding:"required,oneof=bios uefi"`
	IsRescue     bool           `json:"is_resque"`
	RescueImage  *BaseReference `json:"rescue_image" binding:"omitempty"`
	OsFamily     string         `json:"os_family" binding:"required"`
}

type ImagePatchPayload struct {
	Name      string   `json:"name" binding:"required,min=2,max=32"`
	OSCode    string   `json:"os_code" binding:"required,oneof=linux windows other"`
	OSVersion string   `json:"os_version" binding:"required,min=2,max=32"`
	User      string   `json:"user" binding:"required,min=2,max=32"`
	Pools     []string `json:"pools" binding:"omitempty"`
	OsFamily  string   `json:"os_family" binding:"required"`
	UUID      string   `json:"uuid,omitempty" binding:"omitempty"`
}

type ImageStorageResponse struct {
	*ResourceReference
	VolumeID string `json:"volume_id"`
	PoolID   string `json:"pool_id"`
	Status   string `json:"status"`
}

type ImageStorageListResponse struct {
	Offset   int                     `json:"offset"`
	Total    int                     `json:"total"`
	Limit    int                     `json:"limit"`
	Storages []*ImageStorageResponse `json:"storages"`
}

// @Summary get a image
// @Description get a image
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} ImageResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images/{id} [get]
func (v *ImageAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Get image %s", uuID)
	image, err := imageAdmin.GetImageByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get image %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid image query", err)
		return
	}
	imageResp, err := v.getImageResponse(ctx, image)
	if err != nil {
		logger.Errorf("Failed to create image response %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get image %s success, response: %+v", uuID, imageResp)
	c.JSON(http.StatusOK, imageResp)
}

// @Summary patch a image
// @Description patch a image
// @tags Compute
// @Accept  json
// @Produce json
// @Param   message	body   ImagePatchPayload  true   "Image patch payload"
// @Success 200 {object} ImageResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images/{id} [patch]
func (v *ImageAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patch image %s", uuID)
	image, err := imageAdmin.GetImageByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get image %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid image query", err)
		return
	}
	payload := &ImagePatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind JSON, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	err = imageAdmin.Update(ctx, image, payload.OSCode, payload.Name, payload.OSVersion, payload.User, payload.Pools, payload.OsFamily, payload.UUID)
	if err != nil {
		logger.Errorf("Patch image failed, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Patch image failed", err)
		return
	}
	imageResp, err := v.getImageResponse(ctx, image)
	if err != nil {
		logger.Errorf("Failed to create image response, %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch image %s success, response: %+v", uuID, imageResp)
	c.JSON(http.StatusOK, imageResp)
}

// @Summary delete a image
// @Description delete a image
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images/{id} [delete]
func (v *ImageAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Delete image %s", uuID)
	image, err := imageAdmin.GetImageByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get image %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = imageAdmin.Delete(ctx, image)
	if err != nil {
		logger.Errorf("Failed to delete image %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a image
// @Description create a image
// @tags Compute
// @Accept  json
// @Produce json
// @Param   message	body   ImagePayload  true   "Image create payload"
// @Success 200 {object} ImageResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images [post]
func (v *ImageAPI) Create(c *gin.Context) {
	logger.Debugf("Create image")
	ctx := c.Request.Context()
	payload := &ImagePayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	instanceID := int64(0)
	if payload.InstanceUUID != "" {
		instance := &model.Instance{}
		instance, err = instanceAdmin.GetInstanceByUUID(ctx, payload.InstanceUUID)
		if err != nil {
			logger.Errorf("Failed to get instance %s, %+v", payload.InstanceUUID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid input, specified instance does not exist", err)
			return
		}
		instanceID = instance.ID
	}
	if payload.UUID != "" && !utils.IsUUID(payload.UUID) {
		logger.Errorf("Invalid input UUID %s", payload.UUID)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input UUID", nil)
		return
	}
	var rescueImage *model.Image
	if payload.RescueImage != nil {
		rescueImage, err = imageAdmin.GetImage(ctx, payload.RescueImage)
		if err != nil {
			logger.Errorf("Failed to get rescue image %+v, %+v", payload.RescueImage, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid rescue image", err)
			return
		}
	}
	logger.Debugf("Creating image with payload %+v", payload)
	image, err := imageAdmin.Create(ctx, payload.OSCode, payload.Name, payload.OSVersion, "kvm-x86_64", payload.User, payload.DownloadURL, "x86_64", payload.BootLoader, true, instanceID, payload.UUID, rescueImage, payload.OsFamily)
	if err != nil {
		logger.Errorf("Not able to create image %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	imageResp, err := v.getImageResponse(ctx, image)
	if err != nil {
		logger.Errorf("Failed to create image response %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create image success, response: %+v", imageResp)
	c.JSON(http.StatusOK, imageResp)
}

func (v *ImageAPI) getImageResponse(ctx context.Context, image *model.Image) (imageResp *ImageResponse, err error) {
	imageResp = &ImageResponse{
		ResourceReference: &ResourceReference{
			ID:        image.UUID,
			Name:      image.Name,
			CreatedAt: image.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: image.UpdatedAt.Format(TimeStringForMat),
		},
		OSCode:       image.OSCode,
		OSVersion:    image.OsVersion,
		Size:         image.Size,
		Format:       image.Format,
		Architecture: image.Architecture,
		User:         image.UserName,
		Status:       image.Status,
		BootLoader:   image.BootLoader,
		OsFamily:     image.OsFamily,
		// QAEnabled:    image.QAEnabled,
	}
	return
}

// @Summary list images
// @Description list images
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} ImageListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images [get]
func (v *ImageAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	logger.Debugf("List images with offset %s, limit %s, query %s", offsetStr, limitStr, queryStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit %s, %+v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("Invalid query offset or limit %d, %d", offset, limit)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	total, images, err := imageAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("Failed to list images %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list images", err)
		return
	}
	imageListResp := &ImageListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(images),
	}
	imageListResp.Images = make([]*ImageResponse, imageListResp.Limit)
	for i, image := range images {
		imageListResp.Images[i], err = v.getImageResponse(ctx, image)
		if err != nil {
			logger.Errorf("Failed to create image response %+v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List images success, response: %+v", imageListResp)
	c.JSON(http.StatusOK, imageListResp)
}

// @Summary list image storages
// @Description list image storages
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} ImageStorageResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /images/{id}/storages [get]
func (v *ImageAPI) ListStorages(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	imageUUID := c.Param("id")
	if imageUUID == "" {
		logger.Error("Missing image ID")
		ErrorResponse(c, http.StatusBadRequest, "Missing image ID", nil)
		return
	}
	logger.Debugf("List images with offset %s, limit %s, query %s", offsetStr, limitStr, queryStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit %s, %+v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("Invalid query offset or limit %d, %d", offset, limit)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	image, err := imageAdmin.GetImageByUUID(ctx, imageUUID)
	if err != nil {
		logger.Errorf("Failed to get image %s, %+v", imageUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid image query", err)
		return
	}
	total, storages, err := imageStorageAdmin.List(int64(offset), int64(limit), "-created_at", image, queryStr)
	if err != nil {
		logger.Errorf("Failed to list storages %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list storages", err)
		return
	}
	storageListResp := &ImageStorageListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(storages),
	}
	storageListResp.Storages = make([]*ImageStorageResponse, storageListResp.Limit)
	for i, storage := range storages {
		storageListResp.Storages[i], err = v.getImageStorageResponse(ctx, storage)
		if err != nil {
			logger.Errorf("Failed to create storage response %+v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List storage success, response: %+v", storageListResp)
	c.JSON(http.StatusOK, storageListResp)
}

func (v *ImageAPI) getImageStorageResponse(ctx context.Context, storage *model.ImageStorage) (storageResp *ImageStorageResponse, err error) {
	storageResp = &ImageStorageResponse{
		ResourceReference: &ResourceReference{
			ID:        storage.UUID,
			CreatedAt: storage.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: storage.UpdatedAt.Format(TimeStringForMat),
		},
		Status:   string(storage.Status),
		PoolID:   storage.PoolID,
		VolumeID: storage.VolumeID,
	}
	return
}
