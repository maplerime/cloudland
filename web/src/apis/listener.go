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
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var listenerAPI = &ListenerAPI{}
var listenerAdmin = &routes.ListenerAdmin{}

type ListenerAPI struct{}

type ListenerResponse struct {
	*ResourceReference
	Mode     string             `json:"mode"`
	Port     int32              `json:"port"`
	Backends []*BackendResponse `json:"backends,omitempty"`
	Status   string             `json:"status"`
}

type ListenerListResponse struct {
	Offset    int                 `json:"offset"`
	Total     int                 `json:"total"`
	Limit     int                 `json:"limit"`
	Listeners []*ListenerResponse `json:"listeners"`
}

type ListenerPayload struct {
	Name string `json:"name" binding:"required,min=2,max=32"`
	Mode string `json:"mode" binding:"required,oneof=http tcp"`
	Port int    `json:"port" binding:"required,min=1,max=65535"`
	Key  string `json:"key" binding:"omitempty"`
	Cert string `json:"cert" binding:"omitempty"`
}

type ListenerPatchPayload struct {
	Name   string `json:"name" binding:"required,min=2,max=32"`
	Action string `json:"action" binding:"omitempty,oneof=enable disable"`
}

// @Summary get a listener
// @Description get a listener
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} ListenerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id} [get]
func (v *ListenerAPI) Get(c *gin.Context) {
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
		ErrorResponse(c, http.StatusBadRequest, "Invalid listener query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	listenerResp, err := v.getListenerResponse(ctx, listener)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get listener successfully, %s, %+v", listenerID, listenerResp)
	c.JSON(http.StatusOK, listenerResp)
}

// @Summary patch a listener
// @Description patch a listener
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   ListenerPatchPayload  true   "Listener patch payload"
// @Success 200 {object} ListenerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id} [patch]
func (v *ListenerAPI) Patch(c *gin.Context) {
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
	logger.Debugf("Patch listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid listener query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	payload := &ListenerPatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Patching listener %s with %+v", listenerID, payload)
	/*
		err = listenerAdmin.Update(ctx, listener, payload.Name, payload.IsDefault)
		if err != nil {
			logger.Errorf("Failed to patch listener %s, %+v", listenerID, err)
			ErrorResponse(c, http.StatusBadRequest, "Patch listener failed", err)
			return
		}
	*/
	listenerResp, err := v.getListenerResponse(ctx, listener)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch listener successfully, %s, %+v", listenerID, listenerResp)
	c.JSON(http.StatusOK, listenerResp)
}

// @Summary delete a listener
// @Description delete a listener
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners/{listener_id} [delete]
func (v *ListenerAPI) Delete(c *gin.Context) {
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
	logger.Debugf("Delete listener %s", listenerID)
	listener, err := listenerAdmin.GetListenerByUUID(ctx, listenerID)
	if err != nil {
		logger.Errorf("Failed to get listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	if listener.LoadBalancerID != loadBalancer.ID {
		logger.Error("Invalid query for load balancer listener")
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", NewCLError(ErrInvalidParameter, "Invalid query for load balancer listener", nil))
		return
	}
	err = listenerAdmin.Delete(ctx, listener, loadBalancer)
	if err != nil {
		logger.Errorf("Failed to delete listener %s, %+v", listenerID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a listener
// @Description create a listener
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   ListenerPayload  true   "Listener create payload"
// @Success 200 {object} ListenerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners [post]
func (v *ListenerAPI) Create(c *gin.Context) {
	logger.Debugf("Create listener")
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	payload := &ListenerPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating listener with %+v", payload)
	listener, err := listenerAdmin.Create(ctx, payload.Name, payload.Mode, payload.Key, payload.Cert, int32(payload.Port), loadBalancer)
	if err != nil {
		logger.Errorf("Failed to create listener %+v, %+v", payload, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	listenerResp, err := v.getListenerResponse(ctx, listener)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create listener successfully, %+v", listenerResp)
	c.JSON(http.StatusOK, listenerResp)
}

func (v *ListenerAPI) getListenerResponse(ctx context.Context, listener *model.Listener) (listenerResp *ListenerResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, listener.Owner)
	listenerResp = &ListenerResponse{
		ResourceReference: &ResourceReference{
			ID:        listener.UUID,
			Name:      listener.Name,
			Owner:     owner,
			CreatedAt: listener.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: listener.UpdatedAt.Format(TimeStringForMat),
		},
		Mode: listener.Mode,
		Port:   listener.Port,
		Status: listener.Status,
	}
	backends := make([]*BackendResponse, len(listener.Backends))
	for i, backend := range listener.Backends {
		backends[i], err = backendAPI.getBackendResponse(ctx, backend)
		if err != nil {
			logger.Errorf("Failed to get backend response, %+v", err)
			return
		}
	}
	listenerResp.Backends = backends
	return
}

// @Summary list listeners
// @Description list listeners
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} ListenerListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id}/listeners [get]
func (v *ListenerAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	lbID := c.Param("id")
	logger.Debugf("Get load balancer %s", lbID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, lbID)
	if err != nil {
		logger.Errorf("Failed to get load balancer %s, %+v", lbID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	vpcID := strings.TrimSpace(c.DefaultQuery("vpc_id", ""))
	logger.Debugf("List listeners with offset %s, limit %s, query %s, vpc_id %s", offsetStr, limitStr, queryStr, vpcID)

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
	total, listeners, err := listenerAdmin.List(ctx, int64(offset), int64(limit), "-created_at", loadBalancer)
	if err != nil {
		logger.Errorf("Failed to list listeners, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list listeners", err)
		return
	}
	listenerListResp := &ListenerListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(listeners),
	}
	listenerListResp.Listeners = make([]*ListenerResponse, listenerListResp.Limit)
	for i, listener := range listeners {
		listenerListResp.Listeners[i], err = v.getListenerResponse(ctx, listener)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List listeners successfully, %+v", listenerListResp)
	c.JSON(http.StatusOK, listenerListResp)
}
