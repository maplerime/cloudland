/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var floatingIpAPI = &FloatingIpAPI{}
var floatingIpAdmin = &routes.FloatingIpAdmin{}

type FloatingIpAPI struct{}

type FloatingIpInfo struct {
	*ResourceReference
	IpAddress string `json:"ip_address"`
}

type TargetInterface struct {
	*ResourceReference
	IpAddress    string        `json:"ip_address"`
	FromInstance *InstanceInfo `json:"from_instance"`
}

type InstanceInfo struct {
	*ResourceReference
	Hostname string `json:"hostname"`
}

type FloatingIpResponse struct {
	*ResourceReference
	PublicIp        string           `json:"public_ip"`
	TargetInterface *TargetInterface `json:"target_interface,omitempty"`
	VPC             *BaseReference   `json:"vpc,omitempty"`
	Inbound         int32            `json:"inbound"`
	Outbound        int32            `json:"outbound"`
}

type FloatingIpListResponse struct {
	Offset      int                   `json:"offset"`
	Total       int                   `json:"total"`
	Limit       int                   `json:"limit"`
	FloatingIps []*FloatingIpResponse `json:"floating_ips"`
}

type FloatingIpPayload struct {
	PublicSubnets   []*BaseReference `json:"public_subnets" binding:"omitempty"`
	SiteSubnets     []*BaseReference `json:"site_subnets" binding:"omitempty"`
	PublicIp        string           `json:"public_ip" binding:"omitempty,ipv4"`
	Name            string           `json:"name" binding:"required,min=2,max=32"`
	Instance        *BaseID          `json:"instance" binding:"omitempty"`
	Inbound         int32            `json:"inbound" binding:"omitempty,min=1,max=20000"`
	Outbound        int32            `json:"outbound" binding:"omitempty,min=1,max=20000"`
	ActivationCount int32            `json:"activation_count" binding:"omitempty,min=0,max=64"`
}

type FloatingIpPatchPayload struct {
	Instance *BaseID `json:"instance" binding:"omitempty"`
	Inbound  *int32  `json:"inbound" binding:"omitempty,min=1,max=20000"`
	Outbound *int32  `json:"outbound" binding:"omitempty,min=1,max=20000"`
}

// @Summary get a floating ip
// @Description get a floating ip
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} FloatingIpResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips/{id} [get]
func (v *FloatingIpAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Get floating ip %s", uuID)
	floatingIp, err := floatingIpAdmin.GetFloatingIpByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	floatingIpResp, err := v.getFloatingIpResponse(ctx, floatingIp)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, floatingIpResp)
}

// @Summary patch a floating ip
// @Description patch a floating ip
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   FloatingIpPatchPayload  true   "Floating ip patch payload"
// @Success 200 {object} FloatingIpResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips/{id} [patch]
func (v *FloatingIpAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patching floating ip %s", uuID)
	floatingIp, err := floatingIpAdmin.GetFloatingIpByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid floating ip query", err)
		return
	}
	payload := &FloatingIpPatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Patching floating ip %s with %+v", uuID, payload)
	if payload.Inbound != nil {
		floatingIp.Inbound = *payload.Inbound
	}
	if payload.Outbound != nil {
		floatingIp.Outbound = *payload.Outbound
	}
	if payload.Instance == nil {
		err = floatingIpAdmin.Detach(ctx, floatingIp)
		if err != nil {
			logger.Errorf("Failed to detach floating ip %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to detach floating ip", err)
			return
		}
	} else {
		var instance *model.Instance
		instance, err = instanceAdmin.GetInstanceByUUID(ctx, payload.Instance.ID)
		if err != nil {
			logger.Errorf("Failed to get instance %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
			return
		}
		err = floatingIpAdmin.Attach(ctx, floatingIp, instance)
		if err != nil {
			logger.Errorf("Failed to attach floating ip %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to attach floating ip", err)
			return
		}
	}
	floatingIpResp, err := v.getFloatingIpResponse(ctx, floatingIp)
	if err != nil {
		logger.Errorf("Failed to create floating ip response: %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patched floating ip %s, response: %+v", uuID, floatingIpResp)
	c.JSON(http.StatusOK, floatingIpResp)
}

// @Summary delete a floating ip
// @Description delete a floating ip
// @tags Network
// @Accept  json
// @Produce json
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips/{id} [delete]
func (v *FloatingIpAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Delete floating ip %s", uuID)
	floatingIp, err := floatingIpAdmin.GetFloatingIpByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = floatingIpAdmin.Delete(ctx, floatingIp)
	if err != nil {
		logger.Errorf("Failed to delete floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a floating ip
// @Description create a floating ip
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   FloatingIpPayload  true   "Floating ip create payload"
// @Success 200 {object} FloatingIpResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips [post]
func (v *FloatingIpAPI) Create(c *gin.Context) {
	logger.Debugf("Creating floating ip")
	ctx := c.Request.Context()
	payload := &FloatingIpPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating floating ip with %+v", payload)
	var siteSubnets []*model.Subnet
	if payload.SiteSubnets != nil {
		for _, subnetRef := range payload.SiteSubnets {
			subnet, err := subnetAdmin.GetSubnet(ctx, subnetRef)
			if err != nil {
				logger.Errorf("Failed to get site subnet %+v", err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get site subnet", err)
				return
			}
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				return
			}
			if idleCount == 0 {
				logger.Errorf("No idle addresses for site subnet %s", subnet.Name)
				ErrorResponse(c, http.StatusBadRequest, "No idle addresses for site subnet", err)
				return
			}
			subnet.IdleCount = idleCount
			siteSubnets = append(siteSubnets, subnet)
		}
	}
	var activationCount = payload.ActivationCount
	if len(siteSubnets) < 1 {
		if activationCount == 0 {
			activationCount = 1
		}
	}

	var publicSubnets []*model.Subnet
	if payload.PublicSubnets != nil {
		idleCountTotal := int64(0)
		for _, subnetRef := range payload.PublicSubnets {
			subnet, err := subnetAdmin.GetSubnet(ctx, subnetRef)
			if err != nil {
				logger.Errorf("Failed to get public subnet %+v", err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get public subnet", err)
				return
			}
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				return
			}
			idleCountTotal += idleCount
			subnet.IdleCount = idleCount
			publicSubnets = append(publicSubnets, subnet)
		}
		if idleCountTotal < int64(activationCount) {
			logger.Errorf("Not enough idle addresses for public subnets, idleCountTotal: %d, activationCount: %d", idleCountTotal, payload.ActivationCount)
			ErrorResponse(c, http.StatusBadRequest, "Not enough idle addresses for public subnets", err)
			return
		}
	}

	var instance *model.Instance
	if payload.Instance != nil {
		instance, err = instanceAdmin.GetInstanceByUUID(ctx, payload.Instance.ID)
		if err != nil {
			logger.Errorf("Failed to get instance %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
			return
		}
	}

	logger.Debugf("publicSubnets: %v, instance: %v, publicIp: %s, name: %s, inbound: %d, outbound: %d, activationCount: %d, siteSubnets: %v", publicSubnets, instance, payload.PublicIp, payload.Name, payload.Inbound, payload.Outbound, activationCount, siteSubnets)
	floatingIps, err := floatingIpAdmin.Create(ctx, instance, publicSubnets, payload.PublicIp, payload.Name, payload.Inbound, payload.Outbound, activationCount, siteSubnets)
	if err != nil {
		logger.Errorf("Failed to create floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create floating ip", err)
		return
	}
	floatingIpResps := make([]*FloatingIpResponse, 0, len(floatingIps))
	for _, fip := range floatingIps {
		resp, err := v.getFloatingIpResponse(ctx, fip)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		floatingIpResps = append(floatingIpResps, resp)
	}
	logger.Debugf("Created floating ips %+v", floatingIpResps)
	c.JSON(http.StatusOK, floatingIpResps)
}

func (v *FloatingIpAPI) getFloatingIpResponse(ctx context.Context, floatingIp *model.FloatingIp) (floatingIpResp *FloatingIpResponse, err error) {
	owner := orgAdmin.GetOrgName(floatingIp.Owner)
	floatingIpResp = &FloatingIpResponse{
		ResourceReference: &ResourceReference{
			ID:        floatingIp.UUID,
			Name:      floatingIp.Name,
			Owner:     owner,
			CreatedAt: floatingIp.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: floatingIp.UpdatedAt.Format(TimeStringForMat),
		},
		PublicIp: floatingIp.FipAddress,
		Inbound:  floatingIp.Inbound,
		Outbound: floatingIp.Outbound,
	}
	if floatingIp.Router != nil {
		floatingIpResp.VPC = &BaseReference{
			ID:   floatingIp.Router.UUID,
			Name: floatingIp.Router.Name,
		}
	}
	if floatingIp.Instance != nil && len(floatingIp.Instance.Interfaces) > 0 {
		instance := floatingIp.Instance
		interIp := strings.Split(floatingIp.IntAddress, "/")[0]
		owner := orgAdmin.GetOrgName(instance.Owner)
		floatingIpResp.TargetInterface = &TargetInterface{
			ResourceReference: &ResourceReference{
				ID: instance.Interfaces[0].UUID,
			},
			IpAddress: interIp,
			FromInstance: &InstanceInfo{
				ResourceReference: &ResourceReference{
					ID:    instance.UUID,
					Owner: owner,
				},
				Hostname: instance.Hostname,
			},
		}
	}
	return
}

// @Summary list floating ips
// @Description list floating ips
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} FloatingIpListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips [get]
func (v *FloatingIpAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	logger.Debugf("List floating ips with offset %s, limit %s, query %s", offsetStr, limitStr, queryStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("Invalid query offset or limit %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	total, floatingIps, err := floatingIpAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("Failed to list floatingIps %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list floatingIps", err)
		return
	}
	floatingIpListResp := &FloatingIpListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(floatingIps),
	}
	floatingIpListResp.FloatingIps = make([]*FloatingIpResponse, floatingIpListResp.Limit)
	for i, floatingIp := range floatingIps {
		floatingIpListResp.FloatingIps[i], err = v.getFloatingIpResponse(ctx, floatingIp)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	c.JSON(http.StatusOK, floatingIpListResp)
}
