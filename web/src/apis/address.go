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

type AddressPatchPayload struct {
	Remark string `json:"remark" binding:"omitempty,max=512"`
}

type AddressUpdateLockPayload struct {
	Lock bool `json:"lock" binding:"omitempty"`
}

// @Summary patch an address
// @Description patch an address
// @tags Network
// @Accept  json
// @Produce json
// @Param   message  body   AddressPatchPayload  true   "Address patch payload"
// @Router /subnets/{id}/addresses/{address_id} [patch]
// @Success 200 {object} AddressResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
func (v *AddressAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	subnetUUID := c.Param("id")
	addressUUID := c.Param("address_id")

	payload := &AddressPatchPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	subnet, err := subnetAdmin.GetSubnetByUUID(ctx, subnetUUID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet", err)
		return
	}
	addr, err := addressAdmin.GetAddressByUUID(ctx, addressUUID, subnet)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid address", err)
		return
	}
	addr.Remark = payload.Remark
	err = addressAdmin.Update(ctx, addr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to update address", err)
		return
	}
	addrResp, err := v.getAddressResponse(ctx, addr)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, addrResp)
}

// @Summary update address lock
// @Description lock or unlock an address
// @tags Network
// @Accept  json
// @Produce json
// @Param   message  body   AddressUpdateLockPayload  true   "Address lock or unlock payload"
// @Router /subnets/{id}/addresses/{address_id}/update-lock [patch]
// @Success 200 {object} AddressResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
func (v *AddressAPI) UpdateLock(c *gin.Context) {
	ctx := c.Request.Context()
	subnetUUID := c.Param("id")
	addressUUID := c.Param("address_id")

	payload := &AddressUpdateLockPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	subnet, err := subnetAdmin.GetSubnetByUUID(ctx, subnetUUID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet", err)
		return
	}
	addr, err := addressAdmin.GetAddressByUUID(ctx, addressUUID, subnet)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid address", err)
		return
	}

	addr.Reserved = payload.Lock
	//addr.Allocated = payload.Lock || addr.Interface > 0

	err = addressAdmin.Update(ctx, addr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to update address", err)
		return
	}
	addrResp, err := v.getAddressResponse(ctx, addr)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, addrResp)
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
