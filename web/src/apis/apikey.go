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

// APIKeyAPI groups the REST handlers for the /api/v1/api_keys endpoints.
type APIKeyAPI struct{}

// APIKeyPayload is the request body for creating an API key.
type APIKeyPayload struct {
	Name        string `json:"name" binding:"required,min=2,max=64"`
	Description string `json:"description" binding:"omitempty,max=255"`
	ExpiresAt   string `json:"expires_at" binding:"omitempty"` // RFC-like "2006-01-02 15:04:05.000000" or empty for no expiry
}

// APIKeyPatchPayload is the request body for updating an API key.
// Disabled is a pointer so that omitting it leaves the current state unchanged
// (rather than defaulting to false and unintentionally enabling the key).
type APIKeyPatchPayload struct {
	Disabled *bool `json:"disabled"`
}

// APIKeyResponse is the JSON representation of an API key. PlainKey is populated
// only in the Create response (the one-time plain-text key) and omitted elsewhere.
type APIKeyResponse struct {
	*ResourceReference
	Description string     `json:"description,omitempty"`
	Disabled    bool       `json:"disabled"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	PlainKey    string     `json:"api_key,omitempty"`
}

// APIKeyListResponse is the paginated list payload for API keys.
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
	logger.Debugf("List API keys, offset: %s, limit: %s", offsetStr, limitStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		// Error handling: non-numeric offset.
		logger.Errorf("Invalid offset %q: %v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset", err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		// Error handling: non-numeric limit.
		logger.Errorf("Invalid limit %q: %v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid limit", err)
		return
	}
	total, keys, err := apiKeyAdmin.List(ctx, int64(offset), int64(limit), "-created_at", "")
	if err != nil {
		// Error handling: service-layer list failed.
		logger.Errorf("Failed to list API keys: %v", err)
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
		// Error handling: invalid request body.
		logger.Errorf("Failed to bind create payload: %v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Create API key, name: %s", payload.Name)
	var expiresAt *time.Time
	// Branch: parse the optional expiry only when provided.
	if payload.ExpiresAt != "" {
		t, err := time.Parse(TimeStringForMat, payload.ExpiresAt)
		if err != nil {
			// Error handling: invalid expiry format.
			logger.Errorf("Invalid expires_at %q: %v", payload.ExpiresAt, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid expires_at format, use: "+TimeStringForMat, err)
			return
		}
		expiresAt = &t
	}
	apiKey, plainKey, err := apiKeyAdmin.Create(ctx, payload.Name, payload.Description, expiresAt)
	if err != nil {
		// Error handling: service-layer create failed.
		logger.Errorf("Failed to create API key: %v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create API key", err)
		return
	}
	// The plain key is returned to the client here exactly once.
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
	logger.Debugf("Get API key by uuid: %s", uuID)
	apiKey, err := apiKeyAdmin.GetByUUID(ctx, uuID)
	if err != nil {
		// Error handling: key not found or not visible to the caller.
		logger.Errorf("Failed to get API key %s: %v", uuID, err)
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
	logger.Debugf("Patch API key %s", uuID)
	payload := &APIKeyPatchPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		// Error handling: invalid request body.
		logger.Errorf("Failed to bind patch payload: %v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	var apiKey *model.APIKey
	var err error
	// Branch: only update when "disabled" was explicitly provided; otherwise
	// return the current key unchanged (avoids accidentally disabling it).
	if payload.Disabled != nil {
		apiKey, err = apiKeyAdmin.Update(ctx, uuID, *payload.Disabled)
	} else {
		apiKey, err = apiKeyAdmin.GetByUUID(ctx, uuID)
	}
	if err != nil {
		// Error handling: update or lookup failed.
		logger.Errorf("Failed to patch API key %s: %v", uuID, err)
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
	logger.Debugf("Delete API key %s", uuID)
	if err := apiKeyAdmin.Delete(ctx, uuID); err != nil {
		// Error handling: service-layer delete failed.
		logger.Errorf("Failed to delete API key %s: %v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to delete API key", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// toResponse maps an APIKey model to its JSON response, resolving the owner org
// name. plainKey is included only for the Create response and should be "" otherwise.
func (v *APIKeyAPI) toResponse(ctx context.Context, k *model.APIKey, plainKey string) *APIKeyResponse {
	return &APIKeyResponse{
		ResourceReference: &ResourceReference{
			ID:        k.UUID,
			Name:      k.Name,
			Owner:     orgAdmin.GetOrgName(ctx, k.Owner),
			CreatedAt: k.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: k.UpdatedAt.Format(TimeStringForMat),
		},
		Description: k.Description,
		Disabled:    k.Disabled,
		ExpiresAt:   k.ExpiresAt,
		PlainKey:    plainKey,
	}
}
