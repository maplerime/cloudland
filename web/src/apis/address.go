/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"context"
	"net/http"
	"web/src/model"

	. "web/src/common"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var addressAPI = &AddressAPI{}
var addressAdmin = &routes.AddressAdmin{}

type AddressAPI struct{}

type AddressResponse struct {
	*ResourceReference
	Address   string             `json:"address"`
	Netmask   string             `json:"netmask"`
	Type      string             `json:"type"`
	Allocated bool               `json:"allocated"`
	Reserved  bool               `json:"reserved"`
	Remark    string             `json:"remark"`
	Subnet    *ResourceReference `json:"subnet,omitempty"`
}

type AddressRemarkPayload struct {
	Addresses []*BaseID `json:"addresses" binding:"required,min=1"`
	Remark    string    `json:"remark" binding:"omitempty,max=512"`
}

type AddressUpdateLockPayload struct {
	Addresses []*BaseID `json:"addresses" binding:"required,min=1"`
	Lock      bool      `json:"lock" binding:"omitempty"`
}

// @Summary batch patch addresses
// @Description batch patch addresses with unified remark
// @tags Network
// @Accept  json
// @Produce json
// @Param   message  body   AddressRemarkPayload  true   "Address patch payload"
// @Router /addresses/remark [patch]
// @Success 200 {array} AddressResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
func (v *AddressAPI) Remark(c *gin.Context) {
	ctx := c.Request.Context()

	payload := &AddressRemarkPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	addresses := make([]*model.Address, 0, len(payload.Addresses))
	for _, addr := range payload.Addresses {
		address, err := addressAdmin.GetAddressByUUID(ctx, addr.ID)
		if err != nil {
			logger.Errorf("Failed to query address %s, %v", addr.ID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid address", err)
			return
		}
		address.Remark = payload.Remark
		addresses = append(addresses, address)
	}

	responses := make([]*AddressResponse, 0, len(addresses))
	for _, addr := range addresses {
		err := addressAdmin.Update(ctx, addr)
		if err != nil {
			logger.Errorf("Failed to update address: %s %v", addr.Address, err)
			ErrorResponse(c, http.StatusInternalServerError, "Failed to update address "+addr.Address, err)
			return
		}
		addrResp, err := v.getAddressResponse(ctx, addr)
		if err != nil {
			logger.Errorf("Failed to get response for address %s: %v", addr.Address, err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		responses = append(responses, addrResp)
	}
	c.JSON(http.StatusOK, responses)
}

// @Summary batch update address lock
// @Description batch lock or unlock addresses
// @tags Network
// @Accept  json
// @Produce json
// @Param   message  body   AddressUpdateLockPayload  true   "batch lock or unlock payload"
// @Router /addresses/update-lock [patch]
// @Success 200 {array} AddressResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
func (v *AddressAPI) UpdateLock(c *gin.Context) {
	ctx := c.Request.Context()

	payload := &AddressUpdateLockPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	addresses := make([]*model.Address, 0, len(payload.Addresses))
	for _, addr := range payload.Addresses {
		address, err := addressAdmin.GetAddressByUUID(ctx, addr.ID)
		if err != nil {
			logger.Errorf("Failed to query address %s, %v", addr.ID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid address", err)
			return
		}
		address.Reserved = payload.Lock
		addresses = append(addresses, address)
	}

	responses := make([]*AddressResponse, 0, len(addresses))
	for _, addr := range addresses {
		err := addressAdmin.Update(ctx, addr)
		if err != nil {
			logger.Errorf("Failed to update address: %s %v", addr.Address, err)
			ErrorResponse(c, http.StatusInternalServerError, "Failed to update address "+addr.Address, err)
			return
		}
		addrResp, err := v.getAddressResponse(ctx, addr)
		if err != nil {
			logger.Errorf("Failed to get response for address %s: %v", addr.Address, err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		responses = append(responses, addrResp)
	}
	c.JSON(http.StatusOK, responses)
}

type AddressListResponse struct {
	Offset    int                `json:"offset"`
	Total     int                `json:"total"`
	Limit     int                `json:"limit"`
	Addresses []*AddressResponse `json:"addresses"`
}

// @Summary list IP addresses by subnet UUID
// @Description list all IP addresses for a subnet identified by UUID
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} AddressListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /addresses/{uuid} [get]
func (v *AddressAPI) ListIpBySubnetUUID(c *gin.Context) {
	ctx := c.Request.Context()
	subnetUUID := c.Param("uuid")

	// Validate subnet exists
	subnet, err := subnetAdmin.GetSubnetByUUID(ctx, subnetUUID)
	if err != nil {
		logger.Errorf("Failed to query subnet %s, %v", subnetUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet", err)
		return
	}

	// Get addresses for the subnet
	addresses, err := addressAdmin.ListBySubnetID(ctx, subnet.ID)
	if err != nil {
		logger.Errorf("Failed to list addresses for subnet %s, %v", subnetUUID, err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to list addresses", err)
		return
	}

	// Build response
	addressListResp := &AddressListResponse{
		Total:  len(addresses),
		Offset: 0,
		Limit:  len(addresses),
	}
	addressListResp.Addresses = make([]*AddressResponse, len(addresses))
	for i, addr := range addresses {
		addressListResp.Addresses[i], err = v.getAddressResponse(ctx, addr)
		if err != nil {
			logger.Errorf("Failed to get address response, %v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}

	c.JSON(http.StatusOK, addressListResp)
}

func (v *AddressAPI) getAddressResponse(ctx context.Context, addr *model.Address) (addrResp *AddressResponse, err error) {
	addrResp = &AddressResponse{
		ResourceReference: &ResourceReference{
			ID:        addr.UUID,
			CreatedAt: addr.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: addr.UpdatedAt.Format(TimeStringForMat),
		},
		Address:   addr.Address,
		Netmask:   addr.Netmask,
		Type:      addr.Type,
		Allocated: addr.Allocated,
		Reserved:  addr.Reserved,
		Remark:    addr.Remark,
	}
	if addr.Subnet != nil {
		addrResp.Subnet = &ResourceReference{
			ID:   addr.Subnet.UUID,
			Name: addr.Subnet.Name,
		}
	}
	return
}
