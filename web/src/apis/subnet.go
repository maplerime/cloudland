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
	Network        string             `json:"network"`
	Netmask        string             `json:"netmask"`
	Gateway        string             `json:"gateway"`
	NameServer     string             `json:"dns,omitempty"`
	VPC            *ResourceReference `json:"vpc,omitempty"`
	Group          *ResourceReference `json:"group,omitempty"`
	Type           SubnetType         `json:"type"`
	Vlan           int                `json:"vlan,omitempty"`
	IPCount        int64              `json:"ip_count"`        // total
	IdleCount      int64              `json:"idle_count"`      // idle
	ReservedCount  int64              `json:"reserved_count"`  // reserved
	AllocatedCount int64              `json:"allocated_count"` // allocated
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

type AddressResponse struct {
	*ResourceReference
	Address   string     `json:"address"`
	Netmask   string     `json:"netmask"`
	Type      SubnetType `json:"type"`
	Allocated bool       `json:"allocated"`
	Reserved  bool       `json:"reserved"`
	SubnetID  int64      `json:"subnet_id"`
}

type AddressListResponse struct {
	Offset    int                `json:"offset"`
	Total     int                `json:"total"`
	Limit     int                `json:"limit"`
	Addresses []*AddressResponse `json:"addresses"`
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
	var total, allocated, reserved, idle int64
	total, allocated, reserved, idle, err = subnetAdmin.CountsAddressesForSubnet(ctx, subnet)
	if err != nil {
		logger.Errorf("Failed to count addresses for subnet, err=%v", err)
		return
	}
	subnetResp.IPCount = total
	subnetResp.AllocatedCount = allocated
	subnetResp.ReservedCount = reserved
	subnetResp.IdleCount = idle

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
	typeStr := c.DefaultQuery("type", "")
	queryStr := c.DefaultQuery("query", "")
	minIdleIpCountStr := c.DefaultQuery("min_idle_ip_count", "0")
	groupID := strings.TrimSpace(c.DefaultQuery("group_id", "")) // Retrieve group_id from query params
	orderStr := c.DefaultQuery("order", "-created_at")
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

	var conditions []string
	if queryStr != "" {
		queryStr = fmt.Sprintf("(subnets.name like '%%%s%%' OR addresses.address like '%%%s%%')", queryStr, queryStr)
		conditions = append(conditions, queryStr)
	}
	if groupID != "" {
		logger.Debugf("Filtering subnets by group_id: %s", groupID)
		var ipGroup *model.IpGroup
		ipGroup, err = ipGroupAdmin.GetIpGroupByUUID(ctx, groupID)
		if err != nil {
			logger.Errorf("Invalid query group_id: %s, %+v", groupID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid query subnets by group_id UUID: "+groupID, err)
			return
		}

		logger.Debugf("The ipGroup with group_id: %+v\n", ipGroup)
		logger.Debugf("The group_id in ipGroup is: %d", ipGroup.ID)
		queryStr = fmt.Sprintf("subnets.group_id = %d", ipGroup.ID)
		conditions = append(conditions, queryStr)
	}
	//if minIdleIpCountStr != "" {
	//	minIdleIpCount := 0
	//	minIdleIpCount, err = strconv.Atoi(minIdleIpCountStr)
	//	if err != nil {
	//		ErrorResponse(c, http.StatusBadRequest, "Invalid query min_idle_ip_count: "+minIdleIpCountStr, err)
	//		return
	//	}
	//	subnetIDs := make([]int64, 0)
	//	subnetIDs, err = subnetAdmin.GetSubnetIDsByMinIdleIPCount(ctx, int64(minIdleIpCount))
	//	if err != nil {
	//		ErrorResponse(c, http.StatusBadRequest, "Failed to get subnet IDs by min idle IP count", err)
	//		return
	//	}
	//	if len(subnetIDs) > 0 {
	//		ids := make([]string, len(subnetIDs))
	//		for i, id := range subnetIDs {
	//			ids[i] = strconv.FormatInt(id, 10)
	//		}
	//		queryStr = fmt.Sprintf("subnets.id IN (%s)", strings.Join(ids, ","))
	//		conditions = append(conditions, queryStr)
	//	} else {
	//		conditions = append(conditions, "subnets.id = -1") // No subnets found
	//	}
	//}
	minIdleIpCount := 0
	if minIdleIpCountStr != "" {
		minIdleIpCount, err = strconv.Atoi(minIdleIpCountStr)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, "Invalid query min_idle_ip_count: "+minIdleIpCountStr, err)
			return
		}
	}
	if len(conditions) > 0 {
		queryStr = strings.Join(conditions, " AND ")
	}
	total, subnets, err := subnetAdmin.List(ctx, int64(offset), int64(limit), orderStr, queryStr, typeStr, int64(minIdleIpCount))

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

// @Summary list subnet addresses
// @Description list subnet addresses
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} AddressListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets/{id}/addresses [get]
func (v *SubnetAPI) AddressList(c *gin.Context) {
	ctx := c.Request.Context()
	subnetID := c.Param("id")
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	orderStr := c.DefaultQuery("order", "-created_at")
	queryStr := c.DefaultQuery("query", "")
	typeStr := c.DefaultQuery("type", "")
	allocatedStr := c.DefaultQuery("allocated", "")
	reservedStr := c.DefaultQuery("reserved", "")

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
	var subnet *model.Subnet
	subnet, err = subnetAdmin.GetSubnetByUUID(ctx, subnetID)
	if err != nil {
		logger.Errorf("Failed to get subnet, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get subnet", err)
		return
	}
	var allocated *bool
	var reserved *bool
	boolVal := false
	if allocatedStr != "" {
		boolVal, err = strconv.ParseBool(allocatedStr)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, "Invalid query allocated: "+allocatedStr, err)
			return
		}
		allocated = &boolVal
	}
	if reservedStr != "" {
		boolVal, err = strconv.ParseBool(reservedStr)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, "Invalid query reserved: "+reservedStr, err)
			return
		}
		reserved = &boolVal
	}
	total, addresses, err := subnetAdmin.AddressList(ctx, int64(offset), int64(limit), orderStr, queryStr, typeStr, subnet.ID, allocated, reserved)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list address", err)
		return
	}
	adderessListResp := &AddressListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(addresses),
	}
	adderessListResp.Addresses = make([]*AddressResponse, adderessListResp.Limit)
	for i, address := range addresses {
		adderessListResp.Addresses[i], err = v.getAddressResponse(ctx, address)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	c.JSON(http.StatusOK, adderessListResp)
}

func (v *SubnetAPI) getAddressResponse(ctx context.Context, address *model.Address) (addressResp *AddressResponse, err error) {
	owner := orgAdmin.GetOrgName(address.Owner)
	addressResp = &AddressResponse{
		ResourceReference: &ResourceReference{
			ID:        address.UUID,
			Owner:     owner,
			CreatedAt: address.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: address.UpdatedAt.Format(TimeStringForMat),
		},
		Address:   address.Address,
		Netmask:   address.Netmask,
		Type:      SubnetType(address.Type),
		Allocated: address.Allocated,
		Reserved:  address.Reserved,
		SubnetID:  address.SubnetID,
	}
	return
}
