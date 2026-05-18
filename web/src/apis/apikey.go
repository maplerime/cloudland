/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"context"
	"net/http"
	"strconv"
	"time"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var apiKeyAPI = &APIKeyAPI{}
var apiKeyAdmin = &routes.APIKeyAdmin{}

type APIKeyAPI struct{}

type APIKeyPayload struct {
	Name        string `json:"name" binding:"required,min=2,max=64"`
	Description string `json:"description" binding:"omitempty,max=255"`
	ExpiresAt   string `json:"expires_at" binding:"omitempty"`
}

type APIKeyPatchPayload struct {
	Disabled bool `json:"disabled"`
}

type APIKeyResponse struct {
	*ResourceReference
	Description string     `json:"description,omitempty"`
	Disabled    bool       `json:"disabled"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	PlainKey    string     `json:"api_key,omitempty"`
}

type APIKeyListResponse struct {
	Offset  int               `json:"offset"`
	Total   int               `json:"total"`
	Limit   int               `json:"limit"`
	APIKeys []*APIKeyResponse `json:"api_keys"`
}

// @Summary list API keys
// @Description list API keys for the current user
// @tags APIKey
// @Accept  json
// @Produce json
// @Success 200 {object} APIKeyListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /api_keys [get]
func (v *APIKeyAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset", err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid limit", err)
		return
	}
	total, keys, err := apiKeyAdmin.List(ctx, int64(offset), int64(limit), "-created_at", "")
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list API keys", err)
		return
	}
	resp := &APIKeyListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(keys),
	}
	resp.APIKeys = make([]*APIKeyResponse, len(keys))
	for i, k := range keys {
		resp.APIKeys[i] = v.toResponse(ctx, k, "")
	}
	c.JSON(http.StatusOK, resp)
}

// @Summary create an API key
// @Description create a new API key; the plain-text key is returned once only
// @tags APIKey
// @Accept  json
// @Produce json
// @Param   payload body APIKeyPayload true "API key payload"
// @Success 200 {object} APIKeyResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /api_keys [post]
func (v *APIKeyAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &APIKeyPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	var expiresAt *time.Time
	if payload.ExpiresAt != "" {
		t, err := time.Parse(TimeStringForMat, payload.ExpiresAt)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, "Invalid expires_at format, use: "+TimeStringForMat, err)
			return
		}
		expiresAt = &t
	}
	apiKey, plainKey, err := apiKeyAdmin.Create(ctx, payload.Name, payload.Description, expiresAt)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to create API key", err)
		return
	}
	c.JSON(http.StatusOK, v.toResponse(ctx, apiKey, plainKey))
}

// @Summary get an API key
// @Description get an API key by UUID
// @tags APIKey
// @Accept  json
// @Produce json
// @Param   id path string true "API Key UUID"
// @Success 200 {object} APIKeyResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /api_keys/{id} [get]
func (v *APIKeyAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	apiKey, err := apiKeyAdmin.GetByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "API key not found", err)
		return
	}
	c.JSON(http.StatusOK, v.toResponse(ctx, apiKey, ""))
}

// @Summary update an API key
// @Description enable or disable an API key
// @tags APIKey
// @Accept  json
// @Produce json
// @Param   id path string true "API Key UUID"
// @Param   payload body APIKeyPatchPayload true "Patch payload"
// @Success 200 {object} APIKeyResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /api_keys/{id} [patch]
func (v *APIKeyAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	payload := &APIKeyPatchPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	apiKey, err := apiKeyAdmin.Update(ctx, uuID, payload.Disabled)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to update API key", err)
		return
	}
	c.JSON(http.StatusOK, v.toResponse(ctx, apiKey, ""))
}

// @Summary delete an API key
// @Description delete an API key
// @tags APIKey
// @Accept  json
// @Produce json
// @Param   id path string true "API Key UUID"
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /api_keys/{id} [delete]
func (v *APIKeyAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	if err := apiKeyAdmin.Delete(ctx, uuID); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to delete API key", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (v *APIKeyAPI) toResponse(_ context.Context, k *model.APIKey, plainKey string) *APIKeyResponse {
	return &APIKeyResponse{
		ResourceReference: &ResourceReference{
			ID:        k.UUID,
			Name:      k.Name,
			CreatedAt: k.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: k.UpdatedAt.Format(TimeStringForMat),
		},
		Description: k.Description,
		Disabled:    k.Disabled,
		ExpiresAt:   k.ExpiresAt,
		PlainKey:    plainKey,
	}
}
