/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var backendAPI = &BackendAPI{}
var backendAdmin = &routes.BackendAdmin{}

type BackendAPI struct{}

type BackendResponse struct {
	*ResourceReference
	Endpoint string `json:"endpoint,omitempty"`
	Status   string `json:"status"`
}

type BackendListResponse struct {
	Offset   int                `json:"offset"`
	Total    int                `json:"total"`
	Limit    int                `json:"limit"`
	Backends []*BackendResponse `json:"backends"`
}

type BackendPayload struct {
	Name     string `json:"name" binding:"required,min=2,max=32"`
	Endpoint string `json:"endpoint" binding:"required,min=8,max=128"`
}

type BackendPatchPayload struct {
	Name     string `json:"name" binding:"required,min=2,max=32"`
	Endpoint string `json:"endpoint" binding:"omitempty,min=8,max=128"`
	Action   string `json:"action" binding:"omitempty,oneof=enable disable"`
}

// @Summary get a backend
// @Description get a backend
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} BackendResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id}/backends/{backend_id} [get]
func (v *BackendAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	listenerID := c.Param("listener_id")
	logger.Debugf("Get listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listsner query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	backendID := c.Param("backend_id")
	logger.Debugf("Get backend %s", backendID)
	backend, err := backendAdmin.GetBackendByUUID(ctx, backendID)
	if err != nil {
		logger.Errorf("Failed to get backend %s, %+v", backendID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid security group query", err)
		return
	}
	if backend.ListenerID != listener.ID {
		logger.Error("Invalid query for listener backend")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for listener backend", nil))
		return
	}
	backendResp, err := v.getBackendResponse(ctx, backend)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get backend successfully, %s, %+v", backendID, backendResp)
	c.JSON(http.StatusOK, backendResp)
}

// @Summary patch a backend
// @Description patch a backend
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   BackendPatchPayload  true   "Backend patch payload"
// @Success 200 {object} BackendResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id}/backends/{backend_id} [patch]
func (v *BackendAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	listenerID := c.Param("listener_id")
	logger.Debugf("Get listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listsner query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	backendID := c.Param("backend_id")
	logger.Debugf("Patch backend %s", backendID)
	backend, err := backendAdmin.GetBackendByUUID(ctx, backendID)
	if err != nil {
		logger.Errorf("Failed to get backend %s, %+v", backendID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid security group query", err)
		return
	}
	if backend.ListenerID != listener.ID {
		logger.Error("Invalid query for listener backend")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for listener backend", nil))
		return
	}
	payload := &BackendPatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Patching backend %s with %+v", backendID, payload)
	/*
		err = backendAdmin.Update(ctx, backend, payload.Name, payload.IsDefault)
		if err != nil {
			logger.Errorf("Failed to patch backend %s, %+v", backendID, err)
			ErrorResponse(c, http.StatusBadRequest, "Patch security group failed", err)
			return
		}
	*/
	backendResp, err := v.getBackendResponse(ctx, backend)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch backend successfully, %s, %+v", backendID, backendResp)
	c.JSON(http.StatusOK, backendResp)
}

// @Summary delete a backend
// @Description delete a backend
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/:listener_id/backends/{backend_id} [delete]
func (v *BackendAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	listenerID := c.Param("listener_id")
	logger.Debugf("Get listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listsner query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	backendID := c.Param("backend_id")
	logger.Debugf("Delete backend %s", backendID)
	backend, err := backendAdmin.GetBackendByUUID(ctx, backendID)
	if err != nil {
		logger.Errorf("Failed to get backend %s, %+v", backendID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	if backend.ListenerID != listener.ID {
		logger.Error("Invalid query for listener backend")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for listener backend", nil))
		return
	}
	err = backendAdmin.Delete(ctx, backend, listener, loadBalancer)
	if err != nil {
		logger.Errorf("Failed to delete backend %s, %+v", backendID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a backend
// @Description create a backend
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   BackendPayload  true   "Backend create payload"
// @Success 200 {object} BackendResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id}/backends [post]
func (v *BackendAPI) Create(c *gin.Context) {
	logger.Debugf("Create backend")
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	listenerID := c.Param("listener_id")
	logger.Debugf("Get listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listsner query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	payload := &BackendPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating backend with %+v", payload)
	backend, err := backendAdmin.Create(ctx, payload.Name, payload.Endpoint, listener, loadBalancer)
	if err != nil {
		logger.Errorf("Failed to create backend %+v, %+v", payload, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	backendResp, err := v.getBackendResponse(ctx, backend)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create backend successfully, %+v", backendResp)
	c.JSON(http.StatusOK, backendResp)
}

func (v *BackendAPI) getBackendResponse(ctx context.Context, backend *model.Backend) (backendResp *BackendResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, backend.Owner)
	backendResp = &BackendResponse{
		ResourceReference: &ResourceReference{
			ID:        backend.UUID,
			Name:      backend.Name,
			Owner:     owner,
			CreatedAt: backend.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: backend.UpdatedAt.Format(TimeStringForMat),
		},
		Endpoint: backend.BackendAddr,
		Status:   backend.Status,
	}
	return
}

// @Summary list backends
// @Description list backends
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} BackendListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/:listener_id/backends [get]
func (v *BackendAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	listenerID := c.Param("listener_id")
	logger.Debugf("Get listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listsner query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}

	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset: %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit: %s, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		errStr := "Invalid query offset or limit, cannot be negative"
		logger.Errorf(errStr)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", errors.New(errStr))
		return
	}
	total, backends, err := backendAdmin.List(ctx, int64(offset), int64(limit), "-created_at", listener)
	if err != nil {
		logger.Errorf("Failed to list backends, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list backends", err)
		return
	}
	backendListResp := &BackendListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(backends),
	}
	backendListResp.Backends = make([]*BackendResponse, backendListResp.Limit)
	for i, backend := range backends {
		backendListResp.Backends[i], err = v.getBackendResponse(ctx, backend)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List backends successfully, %+v", backendListResp)
	c.JSON(http.StatusOK, backendListResp)
}
