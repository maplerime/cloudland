/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var subnetAPI = &SubnetAPI{}
var subnetAdmin = &routes.SubnetAdmin{}

type SubnetAPI struct{}

type SubnetResponse struct {
	*ResourceReference
	Network    string             `json:"network"`
	Netmask    string             `json:"netmask"`
	Gateway    string             `json:"gateway"`
	NameServer string             `json:"dns,omitempty"`
	VPC        *ResourceReference `json:"vpc,omitempty"`
	Group      *ResourceReference `json:"group,omitempty"`
	Type       SubnetType         `json:"type"`
	IdleCount  int64              `json:"idle_count"`
}

type SiteSubnetInfo struct {
	*ResourceReference
	Network string `json:"network"`
	Gateway string `json:"gateway"`
}

type SubnetListResponse struct {
	Offset  int               `json:"offset"`
	Total   int               `json:"total"`
	Limit   int               `json:"limit"`
	Subnets []*SubnetResponse `json:"subnets"`
}

type SubnetPayload struct {
	Name        string         `json:"name" binding:"required,min=2,max=32"`
	NetworkCIDR string         `json:"network_cidr" binding:"required,cidrv4"`
	Gateway     string         `json:"gateway" binding:"omitempty,ipv4"`
	StartIP     string         `json:"start_ip" binding:"omitempty,ipv4"`
	EndIP       string         `json:"end_ip" binding:"omitempty",ipv4`
	NameServer  string         `json:"dns" binding:"omitempty"`
	BaseDomain  string         `json:"base_domain" binding:"omitempty"`
	Dhcp        bool           `json:"dhcp" binding:"omitempty"`
	VPC         *BaseReference `json:"vpc" binding:"omitempty"`
	Group       *BaseReference `json:"group" binding:"omitempty"`
	Vlan        int            `json:"vlan" binding:"omitempty,gte=1,lte=16777215"`
	Type        SubnetType     `json:"type" binding:"omitempty,oneof=public internal"`
}

type SubnetPatchPayload struct {
}

// @Summary get a subnet
// @Description get a subnet
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} SubnetResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets/{id} [get]
func (v *SubnetAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	subnet, err := subnetAdmin.GetSubnetByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet query", err)
		return
	}
	subnetResp, err := v.getSubnetResponse(ctx, subnet)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, subnetResp)
}

// @Summary patch a subnet
// @Description patch a subnet
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   SubnetPatchPayload  true   "Subnet patch payload"
// @Success 200 {object} SubnetResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets/{id} [patch]
func (v *SubnetAPI) Patch(c *gin.Context) {
	subnetResp := &SubnetResponse{}
	c.JSON(http.StatusOK, subnetResp)
}

// @Summary delete a subnet
// @Description delete a subnet
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets/{id} [delete]
func (v *SubnetAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	subnet, err := subnetAdmin.GetSubnetByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = subnetAdmin.Delete(ctx, subnet)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a subnet
// @Description create a subnet
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   SubnetPayload  true   "Subnet create payload"
// @Success 200 {object} SubnetResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets [post]
func (v *SubnetAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &SubnetPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	if payload.VPC == nil && payload.Type != Public {
		ErrorResponse(c, http.StatusBadRequest, "VPC must be specified if network type not public", err)
		return
	}
	var router *model.Router
	if payload.VPC != nil {
		router, err = routerAdmin.GetRouter(ctx, payload.VPC)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, "Failed to get router", err)
			return
		}
	}
	var ipGroup *model.IpGroup
	if payload.Group != nil {
		if payload.Group.ID == "" && payload.Group.Name == "" {
			logger.Errorf("Group ID or Name is required")
			ErrorResponse(c, http.StatusBadRequest, "Group ID or Name is required", err)
			return
		}
		if payload.Group.ID != "" {
			ipGroup, err = ipGroupAdmin.GetIpGroupByUUID(ctx, payload.Group.ID)
			if err != nil {
				logger.Errorf("Failed to get ipGroup, uuid=%s, err=%v", payload.Group.ID, err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get ipGroup", err)
				return
			}
		} else {
			ipGroup, err = ipGroupAdmin.GetIpGroupByName(ctx, payload.Group.Name)
			if err != nil {
				logger.Errorf("Failed to get ipGroup, name=%s, err=%v", payload.Group.Name, err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get ipGroup", err)
				return
			}
		}
	}
	subnet, err := subnetAdmin.Create(ctx, payload.Vlan, payload.Name, payload.NetworkCIDR, payload.Gateway, payload.StartIP, payload.EndIP, string(payload.Type), payload.NameServer, payload.BaseDomain, payload.Dhcp, router, ipGroup)
	if err != nil {
		logger.Errorf("Failed to create subnet, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create subnet", err)
		return
	}
	subnetResp, err := v.getSubnetResponse(ctx, subnet)
	if err != nil {
		logger.Errorf("Failed to get subnet response, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Subnet created successfully, subnet=%+v", subnetResp)
	c.JSON(http.StatusOK, subnetResp)
}

func (v *SubnetAPI) getSubnetResponse(ctx context.Context, subnet *model.Subnet) (subnetResp *SubnetResponse, err error) {
	owner := orgAdmin.GetOrgName(subnet.Owner)
	subnetResp = &SubnetResponse{
		ResourceReference: &ResourceReference{
			ID:        subnet.UUID,
			Name:      subnet.Name,
			Owner:     owner,
			CreatedAt: subnet.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: subnet.UpdatedAt.Format(TimeStringForMat),
		},
		Network:    subnet.Network,
		Netmask:    subnet.Netmask,
		Gateway:    subnet.Gateway,
		NameServer: subnet.NameServer,
		Type:       SubnetType(subnet.Type),
	}
	if subnet.Router != nil {
		router := subnet.Router
		subnetResp.VPC = &ResourceReference{
			ID:   router.UUID,
			Name: router.Name,
		}
	}
	if subnet.Group != nil {
		group := subnet.Group
		subnetResp.Group = &ResourceReference{
			ID:   group.UUID,
			Name: group.Name,
		}
	}
	var idleCount int64
	idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
	if err != nil {
		logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
		return
	}
	subnetResp.IdleCount = idleCount
	return
}

// @Summary list subnets
// @Description list subnets
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} SubnetListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets [get]
func (v *SubnetAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	groupID := strings.TrimSpace(c.DefaultQuery("group_id", "")) // Retrieve group_id from query params
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
	if offset < 0 || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}

	if queryStr != "" {
		queryStr = fmt.Sprintf("name like '%%%s%%'", queryStr)
	}
	if groupID != "" {
		logger.Debugf("Filtering subnets by group_id: %s", groupID)
		var ipGroup *model.IpGroup
		ipGroup, err := ipGroupAdmin.GetIpGroupByUUID(ctx, groupID)
		if err != nil {
			logger.Errorf("Invalid query group_id: %s, %+v", groupID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid query subnets by group_id UUID: "+groupID, err)
			return
		}

		logger.Debugf("The ipGroup with group_id: %+v\n", ipGroup)
		logger.Debugf("The group_id in ipGroup is: %d", ipGroup.ID)
		queryStr = fmt.Sprintf("group_id = %d", ipGroup.ID)
	}
	total, subnets, err := subnetAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr, "")

	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list subnets", err)
		return
	}
	subnetListResp := &SubnetListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(subnets),
	}
	subnetListResp.Subnets = make([]*SubnetResponse, subnetListResp.Limit)
	for i, subnet := range subnets {
		subnetListResp.Subnets[i], err = v.getSubnetResponse(ctx, subnet)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	c.JSON(http.StatusOK, subnetListResp)
}
