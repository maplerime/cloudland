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
	Group           *BaseReference   `json:"group,omitempty"`
}

type FloatingIpListResponse struct {
	Offset      int                   `json:"offset"`
	Total       int                   `json:"total"`
	Limit       int                   `json:"limit"`
	FloatingIps []*FloatingIpResponse `json:"floating_ips"`
}

type FloatingIpPayload struct {
	PublicSubnet    *BaseReference   `json:"public_subnet" binding:"omitempty"`
	PublicSubnets   []*BaseReference `json:"public_subnets" binding:"omitempty"`
	SiteSubnets     []*BaseReference `json:"site_subnets" binding:"omitempty"`
	PublicIp        string           `json:"public_ip" binding:"omitempty,ipv4"`
	Name            string           `json:"name" binding:"required,min=2,max=32"`
	Instance        *BaseID          `json:"instance" binding:"omitempty"`
	Inbound         int32            `json:"inbound" binding:"omitempty,min=1,max=20000"`
	Outbound        int32            `json:"outbound" binding:"omitempty,min=1,max=20000"`
	ActivationCount int32            `json:"activation_count" binding:"omitempty,min=0,max=64"`
	Group           *BaseID          `json:"group" binding:"omitempty"`
}

type FloatingIpPatchPayload struct {
	Instance *BaseID `json:"instance" binding:"omitempty"`
	Inbound  *int32  `json:"inbound" binding:"omitempty,min=1,max=20000"`
	Outbound *int32  `json:"outbound" binding:"omitempty,min=1,max=20000"`
	Group    *BaseID `json:"group" binding:"omitempty"`
}

// BatchAttachPayload represents the payload for batch attach floating IPs
type BatchAttachPayload struct {
	Instance    *BaseID          `json:"instance" binding:"required"`
	SiteSubnets []*BaseReference `json:"site_subnets" binding:"required"`
}

// BatchDetachPayload represents the payload for batch detach floating IPs
type BatchDetachPayload struct {
	Instance    *BaseID          `json:"instance" binding:"required"`
	SiteSubnets []*BaseReference `json:"site_subnets" binding:"required"`
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
	if ElasticType(floatingIp.Type) != PublicFloating {
		logger.Errorf("Wrong public ip type %+v", floatingIp.Type)
		ErrorResponse(c, http.StatusBadRequest, "Invalid public ip type", err)
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
	var instance *model.Instance
	if payload.Instance != nil {
		instance, err = instanceAdmin.GetInstanceByUUID(ctx, payload.Instance.ID)
		if err != nil {
			logger.Errorf("Failed to get instance %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
			return
		}
	}

	var group *model.IpGroup
	if payload.Group != nil {
		groupID, err := strconv.Atoi(payload.Group.ID)
		if err != nil {
			logger.Errorf("Invalid group ID %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid group ID", err)
			return
		}
		group, err = ipGroupAdmin.Get(ctx, int64(groupID))
		if err != nil {
			logger.Errorf("Failed to get ip group %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get ip group", err)
			return
		}
	}
	logger.Debugf("Updating floating ip %s with instance %s, group %s", uuID, instance, group)
	floatingIp, err = floatingIpAdmin.Update(ctx, floatingIp, instance, group)
	if err != nil {
		logger.Errorf("Failed to update floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to update floating ip", err)
		return
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
		for _, subnetRef := range payload.PublicSubnets {
			subnet, err := subnetAdmin.GetSubnet(ctx, subnetRef)
			if err != nil {
				logger.Errorf("Failed to get public subnet %+v", err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get public subnet", err)
				return
			}
			publicSubnets = append(publicSubnets, subnet)
		}
	} else {
		if payload.PublicSubnet != nil {
			subnet, err := subnetAdmin.GetSubnet(ctx, payload.PublicSubnet)
			if err != nil {
				logger.Errorf("Failed to get public subnet %+v", err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to get public subnet", err)
				return
			}
			publicSubnets = append(publicSubnets, subnet)
		} else {
			publicSubnets = make([]*model.Subnet, 0)
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
	var group *model.IpGroup
	if payload.Group != nil {
		groupID, err := strconv.Atoi(payload.Group.ID)
		if err != nil {
			logger.Errorf("Invalid group ID %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid group ID", err)
			return
		}
		group, err = ipGroupAdmin.Get(ctx, int64(groupID))
		if err != nil {
			logger.Errorf("Failed to get ip group %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get ip group", err)
			return
		}
	}

	logger.Debugf("publicSubnets: %v, instance: %v, publicIp: %s, name: %s, inbound: %d, outbound: %d, activationCount: %d, siteSubnets: %v, group: %v", publicSubnets, instance, payload.PublicIp, payload.Name, payload.Inbound, payload.Outbound, activationCount, siteSubnets, group)
	floatingIps, err := floatingIpAdmin.Create(ctx, instance, publicSubnets, payload.PublicIp, payload.Name, payload.Inbound, payload.Outbound, activationCount, siteSubnets, group)
	if err != nil {
		logger.Errorf("Failed to create floating ip %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create floating ip", err)
		return
	}
	floatingIpResp := make([]*FloatingIpResponse, 0, len(floatingIps))
	for _, fip := range floatingIps {
		resp, err := v.getFloatingIpResponse(ctx, fip)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		floatingIpResp = append(floatingIpResp, resp)
	}
	logger.Debugf("Created floating ips %+v", floatingIpResp)
	c.JSON(http.StatusOK, floatingIpResp)
}

func (v *FloatingIpAPI) getFloatingIpResponse(ctx context.Context, floatingIp *model.FloatingIp) (floatingIpResp *FloatingIpResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, floatingIp.Owner)
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
		owner := orgAdmin.GetOrgName(ctx, instance.Owner)
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
	total, floatingIps, err := floatingIpAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr, "")
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

// @Summary batch attach floating ips
// @Description batch attach existing floating ips from site subnets to an instance
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   BatchAttachPayload  true   "Batch attach payload"
// @Success 200 {object} []FloatingIpResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips/batch_attach [post]
func (v *FloatingIpAPI) BatchAttach(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Debugf("Batch attaching floating ips")

	payload := &BatchAttachPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	logger.Debugf("Batch attaching floating ips with payload %+v", payload)

	// Get instance
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, payload.Instance.ID)
	if err != nil {
		logger.Errorf("Failed to get instance %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
		return
	}

	// Get and validate site subnets
	var siteSubnets []*model.Subnet
	for _, subnetRef := range payload.SiteSubnets {
		subnet, err := subnetAdmin.GetSubnet(ctx, subnetRef)
		if err != nil {
			logger.Errorf("Failed to get site subnet %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get site subnet", err)
			return
		}

		// Verify that the subnet type is "site"
		if subnet.Type != "site" {
			logger.Errorf("Subnet %s is not a site type subnet", subnet.Name)
			ErrorResponse(c, http.StatusBadRequest, "All subnets must be site type", err)
			return
		}

		siteSubnets = append(siteSubnets, subnet)
	}

	// Find and attach floating IPs for each site subnet
	var attachedFloatingIps []*model.FloatingIp
	for _, subnet := range siteSubnets {
		logger.Debugf("Processing site subnet: %s (ID: %d)", subnet.Name, subnet.ID)

		// Find floating IPs associated with this site subnet that are not attached to any instance
		_, floatingIps, err := floatingIpAdmin.List(ctx, 0, -1, "", "", fmt.Sprintf("type = '%s' AND instance_id = 0", PublicSite))
		if err != nil {
			logger.Errorf("Failed to list floating ips %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to list floating ips", err)
			return
		}

		logger.Debugf("Found %d available floating IPs for site subnet %s", len(floatingIps), subnet.Name)

		// Find floating IPs that belong to this specific site subnet
		var subnetFloatingIps []*model.FloatingIp
		for _, fip := range floatingIps {
			logger.Debugf("Checking floating IP: %s (ID: %d)", fip.FipAddress, fip.ID)

			if fip.Interface == nil {
				logger.Debugf("Floating IP %s has no interface", fip.FipAddress)
				continue
			}

			if fip.Interface.Address == nil {
				logger.Debugf("Floating IP %s interface has no address", fip.FipAddress)
				continue
			}

			if fip.Interface.Address.Subnet == nil {
				logger.Debugf("Floating IP %s interface address has no subnet", fip.FipAddress)
				continue
			}

			logger.Debugf("Floating IP %s belongs to subnet %s (ID: %d), checking against target subnet %s (ID: %d)",
				fip.FipAddress, fip.Interface.Address.Subnet.Name, fip.Interface.Address.Subnet.ID,
				subnet.Name, subnet.ID)

			if fip.Interface.Address.Subnet.ID == subnet.ID {
				logger.Debugf("Found matching floating IP %s for subnet %s, adding to attach list", fip.FipAddress, subnet.Name)
				subnetFloatingIps = append(subnetFloatingIps, fip)
			} else {
				logger.Debugf("Floating IP %s subnet ID (%d) doesn't match target subnet ID (%d)",
					fip.FipAddress, fip.Interface.Address.Subnet.ID, subnet.ID)
			}
		}

		// Check if there are floating IPs available for this subnet
		if len(subnetFloatingIps) == 0 {
			logger.Errorf("No floating IPs found for site subnet %s", subnet.Name)
			ErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("No floating IPs found for site subnet %s", subnet.Name), err)
			return
		}

		logger.Debugf("Found %d floating IPs to attach for site subnet %s", len(subnetFloatingIps), subnet.Name)

		// Attach the floating IPs to the instance
		for _, fip := range subnetFloatingIps {
			logger.Debugf("Attaching floating IP %s to instance %s", fip.FipAddress, instance.UUID)
			err = floatingIpAdmin.Attach(ctx, fip, instance)
			if err != nil {
				logger.Errorf("Failed to attach floating ip %s to instance %s: %v", fip.FipAddress, instance.UUID, err)
				ErrorResponse(c, http.StatusBadRequest, "Failed to attach floating ip", err)
				return
			}
			logger.Debugf("Successfully attached floating IP %s to instance %s", fip.FipAddress, instance.UUID)
			attachedFloatingIps = append(attachedFloatingIps, fip)
		}
	}

	// Convert to response format
	floatingIpResp := make([]*FloatingIpResponse, 0, len(attachedFloatingIps))
	for _, fip := range attachedFloatingIps {
		resp, err := v.getFloatingIpResponse(ctx, fip)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		floatingIpResp = append(floatingIpResp, resp)
	}

	logger.Debugf("Batch attached %d floating ips to instance %s", len(attachedFloatingIps), instance.UUID)
	c.JSON(http.StatusOK, floatingIpResp)
}

// @Summary batch detach floating ips
// @Description batch detach floating ips from site subnets from an instance
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   BatchDetachPayload  true   "Batch detach payload"
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /floating_ips/batch_detach [post]
func (v *FloatingIpAPI) BatchDetach(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Debugf("Batch detaching floating ips")

	payload := &BatchDetachPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	logger.Debugf("Batch detaching floating ips with payload %+v", payload)

	// Get instance
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, payload.Instance.ID)
	if err != nil {
		logger.Errorf("Failed to get instance %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
		return
	}

	// Get and validate site subnets
	var siteSubnets []*model.Subnet
	for _, subnetRef := range payload.SiteSubnets {
		subnet, err := subnetAdmin.GetSubnet(ctx, subnetRef)
		if err != nil {
			logger.Errorf("Failed to get site subnet %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get site subnet", err)
			return
		}

		// Verify that the subnet type is "site"
		if subnet.Type != "site" {
			logger.Errorf("Subnet %s is not a site type subnet", subnet.Name)
			ErrorResponse(c, http.StatusBadRequest, "All subnets must be site type", err)
			return
		}

		siteSubnets = append(siteSubnets, subnet)
	}

	// Find and detach floating IPs for each site subnet
	detachedCount := 0
	for _, subnet := range siteSubnets {
		logger.Debugf("Processing site subnet: %s (ID: %d)", subnet.Name, subnet.ID)

		// Find floating IPs associated with this subnet and instance
		_, floatingIps, err := floatingIpAdmin.List(ctx, 0, -1, "", "", fmt.Sprintf("instance_id = %d AND type = '%s'", instance.ID, PublicSite))
		if err != nil {
			logger.Errorf("Failed to list floating ips %+v", err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to list floating ips", err)
			return
		}

		logger.Debugf("Found %d floating IPs for instance %d (ID: %d)", len(floatingIps), instance.ID, instance.ID)

		// Detach floating IPs that are associated with the specified site subnet
		for _, fip := range floatingIps {
			logger.Debugf("Checking floating IP: %s (ID: %d)", fip.FipAddress, fip.ID)

			if fip.Interface == nil {
				logger.Debugf("Floating IP %s has no interface", fip.FipAddress)
				continue
			}

			if fip.Interface.Address == nil {
				logger.Debugf("Floating IP %s interface has no address", fip.FipAddress)
				continue
			}

			if fip.Interface.Address.Subnet == nil {
				logger.Debugf("Floating IP %s interface address has no subnet", fip.FipAddress)
				continue
			}

			logger.Debugf("Floating IP %s belongs to subnet %s (ID: %d), checking against target subnet %s (ID: %d)",
				fip.FipAddress, fip.Interface.Address.Subnet.Name, fip.Interface.Address.Subnet.ID,
				subnet.Name, subnet.ID)

			if fip.Interface.Address.Subnet.ID == subnet.ID {
				logger.Debugf("Found matching floating IP %s for subnet %s, detaching...", fip.FipAddress, subnet.Name)
				err = floatingIpAdmin.Detach(ctx, fip)
				if err != nil {
					logger.Errorf("Failed to detach floating ip %s: %v", fip.FipAddress, err)
					ErrorResponse(c, http.StatusBadRequest, "Failed to detach floating ip", err)
					return
				}
				detachedCount++
				logger.Debugf("Successfully detached floating IP %s", fip.FipAddress)
			} else {
				logger.Debugf("Floating IP %s subnet ID (%d) doesn't match target subnet ID (%d)",
					fip.FipAddress, fip.Interface.Address.Subnet.ID, subnet.ID)
			}
		}
	}

	logger.Debugf("Batch detached %d floating ips from instance %s", detachedCount, instance.UUID)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Batch detach completed successfully. Detached %d floating IPs.", detachedCount)})
}
