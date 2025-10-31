/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var loadBalancerAPI = &LoadBalancerAPI{}
var loadBalancerAdmin = &routes.LoadBalancerAdmin{}

type LoadBalancerAPI struct{}

type LoadBalancerResponse struct {
	*ResourceReference
	Name      string             `json:"name"`
	FloatingIps        []*FloatingIpInfo    `json:"floating_ips,omitempty"`
	Listeners        []*ListenerResponse    `json:"listeners,omitempty"`
	VPC              *ResourceReference `json:"vpc,omitempty"`
	Status    string             `json:"status"`
}

type LoadBalancerListResponse struct {
	Offset         int                      `json:"offset"`
	Total          int                      `json:"total"`
	Limit          int                      `json:"limit"`
	LoadBalancers []*LoadBalancerResponse `json:"load_balancers"`
}

type LoadBalancerPayload struct {
	Name      string         `json:"name" binding:"required,min=2,max=32"`
	VPC       *BaseReference `json:"vpc" binding:"required"`
	Zone      string         `json:"zone" binding:"omitempty,min=1,max=32"`
}

type LoadBalancerPatchPayload struct {
	Name      string `json:"name" binding:"required,min=2,max=32"`
	Action    string `json:"action" binding:"omitempty,oneof=enable disable"`
}

// @Summary get a loadBalancer
// @Description get a loadBalancer
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} LoadBalancerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id} [get]
func (v *LoadBalancerAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Get loadBalancer %s", uuID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get loadBalancer %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	loadBalancerResp, err := v.getLoadBalancerResponse(ctx, loadBalancer)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get loadBalancer successfully, %s, %+v", uuID, loadBalancerResp)
	c.JSON(http.StatusOK, loadBalancerResp)
}

// @Summary patch a loadBalancer
// @Description patch a loadBalancer
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   LoadBalancerPatchPayload  true   "LoadBalancer patch payload"
// @Success 200 {object} LoadBalancerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id} [patch]
func (v *LoadBalancerAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patch loadBalancer %s", uuID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get loadBalancer %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid load balancer query", err)
		return
	}
	payload := &LoadBalancerPatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Patching loadBalancer %s with %+v", uuID, payload)
	/*
	err = loadBalancerAdmin.Update(ctx, loadBalancer, payload.Name, payload.IsDefault)
	if err != nil {
		logger.Errorf("Failed to patch loadBalancer %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Patch load balancer failed", err)
		return
	}
	*/
	loadBalancerResp, err := v.getLoadBalancerResponse(ctx, loadBalancer)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch loadBalancer successfully, %s, %+v", uuID, loadBalancerResp)
	c.JSON(http.StatusOK, loadBalancerResp)
}

// @Summary delete a loadBalancer
// @Description delete a loadBalancer
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers/{id} [delete]
func (v *LoadBalancerAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Delete loadBalancer %s", uuID)
	loadBalancer, err := loadBalancerAdmin.GetLoadBalancerByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get loadBalancer %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = loadBalancerAdmin.Delete(ctx, loadBalancer)
	if err != nil {
		logger.Errorf("Failed to delete loadBalancer %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a loadBalancer
// @Description create a loadBalancer
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   LoadBalancerPayload  true   "LoadBalancer create payload"
// @Success 200 {object} LoadBalancerResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers [post]
func (v *LoadBalancerAPI) Create(c *gin.Context) {
	logger.Debugf("Create loadBalancer")
	ctx := c.Request.Context()
	payload := &LoadBalancerPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating loadBalancer with %+v", payload)
	var router *model.Router
	if payload.VPC != nil {
		router, err = routerAdmin.GetRouter(ctx, payload.VPC)
		if err != nil {
			logger.Errorf("Failed to get vpc %+v, %+v", payload.VPC, err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get vpc", err)
			return
		}
	}
	var zone *model.Zone
	if payload.Zone != "" {
		zone, err = zoneAdmin.GetZoneByName(ctx, payload.Zone)
		if err != nil {
			logger.Errorf("Failed to get zone %+v, %+v", payload.Zone, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid zone", err)
			return
		}
	}
	loadBalancer, err := loadBalancerAdmin.Create(ctx, payload.Name, router, zone)
	if err != nil {
		logger.Errorf("Failed to create loadBalancer %+v, %+v", payload, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	loadBalancerResp, err := v.getLoadBalancerResponse(ctx, loadBalancer)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create loadBalancer successfully, %+v", loadBalancerResp)
	c.JSON(http.StatusOK, loadBalancerResp)
}

func (v *LoadBalancerAPI) getLoadBalancerResponse(ctx context.Context, loadBalancer *model.LoadBalancer) (loadBalancerResp *LoadBalancerResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, loadBalancer.Owner)
	loadBalancerResp = &LoadBalancerResponse{
		ResourceReference: &ResourceReference{
			ID:        loadBalancer.UUID,
			Name:      loadBalancer.Name,
			Owner:     owner,
			CreatedAt: loadBalancer.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: loadBalancer.UpdatedAt.Format(TimeStringForMat),
		},
		Status: loadBalancer.Status,
	}
	if loadBalancer.Router != nil {
		loadBalancerResp.VPC = &ResourceReference{
			ID:   loadBalancer.Router.UUID,
			Name: loadBalancer.Router.Name,
		}
	}
	listeners := make([]*ListenerResponse, len(loadBalancer.Listeners))
	for i, listener := range loadBalancer.Listeners {
		listeners[i], err = listenerAPI.getListenerResponse(ctx, listener)
		if err != nil {
			logger.Errorf("Failed to get listener response, %+v", err)
			return
		}
	}
	loadBalancerResp.Listeners = listeners
	floatingIps := make([]*FloatingIpInfo, len(loadBalancer.FloatingIps))
	for i, floatingip := range loadBalancer.FloatingIps {
		floatingIps[i] = &FloatingIpInfo{
			ResourceReference: &ResourceReference{
				ID:   floatingip.UUID,
				Name: floatingip.Name,
			},
			FipAddress: floatingip.FipAddress,
		}
	}
	return
}

// @Summary list loadBalancers
// @Description list loadBalancers
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} LoadBalancerListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /load_balancers [get]
func (v *LoadBalancerAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	vpcID := strings.TrimSpace(c.DefaultQuery("vpc_id", ""))
	logger.Debugf("List loadBalancers with offset %s, limit %s, query %s, vpc_id %s", offsetStr, limitStr, queryStr, vpcID)

	if vpcID != "" {
		logger.Debugf("Filtering loadBalancers by VPC ID: %s", vpcID)
		var router *model.Router
		router, err := routerAdmin.GetRouterByUUID(ctx, vpcID)
		if err != nil {
			logger.Errorf("Invalid query vpc_id: %s, %+v", vpcID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid query router by vpc_id UUID: "+vpcID, err)
			return
		}

		logger.Debugf("The router with vpc_id: %+v\n", router)
		logger.Debugf("The router_id in vpc is: %d", router.ID)
		queryStr = fmt.Sprintf("router_id = %d", router.ID)
	}

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
	total, loadBalancers, err := loadBalancerAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("Failed to list loadBalancers, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list loadBalancers", err)
		return
	}
	loadBalancerListResp := &LoadBalancerListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(loadBalancers),
	}
	loadBalancerListResp.LoadBalancers = make([]*LoadBalancerResponse, loadBalancerListResp.Limit)
	for i, loadBalancer := range loadBalancers {
		loadBalancerListResp.LoadBalancers[i], err = v.getLoadBalancerResponse(ctx, loadBalancer)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List loadBalancers successfully, %+v", loadBalancerListResp)
	c.JSON(http.StatusOK, loadBalancerListResp)
}
