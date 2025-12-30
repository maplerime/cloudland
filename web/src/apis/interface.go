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

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var interfaceAPI = &InterfaceAPI{}
var interfaceAdmin = &routes.InterfaceAdmin{}

type InterfaceAPI struct{}

type InterfaceListResponse struct {
	Offset     int                  `json:"offset"`
	Total      int                  `json:"total"`
	Limit      int                  `json:"limit"`
	Interfaces []*InterfaceResponse `json:"interfaces"`
}

type AddressInfo struct {
	IPAddress string             `json:"ip_address"`
	Subnet    *ResourceReference `json:"subnet"`
}

type InterfaceResponse struct {
	*BaseReference
	*AddressInfo
	SecondaryAddresses []*AddressInfo       `json:"secondary_addresses,omitempty"`
	MacAddress         string               `json:"mac_address"`
	IsPrimary          bool                 `json:"is_primary"`
	Inbound            int32                `json:"inbound"`
	Outbound           int32                `json:"outbound"`
	SiteSubnets        []*SiteSubnetInfo    `json:"site_subnets,omitempty"`
	FloatingIps        []*FloatingIpInfo    `json:"floating_ips,omitempty"`
	SecurityGroups     []*ResourceReference `json:"security_groups,omitempty"`
}

type InterfacePayload struct {
	Subnet          *BaseReference   `json:"subnet" binding:"omitempty"`
	Subnets         []*BaseReference `json:"subnets" binding:"omitempty,gte=1,lte=16"`
	IpAddress       string           `json:"ip_address", binding:"omitempty,ipv4"`
	MacAddress      string           `json:"mac_address" binding:"omitempty,mac"`
	PublicAddresses []*BaseReference `json:"public_addresses,omitempty"`
	Count           int              `json:"count" binding:"omitempty,gte=1,lte=512"`
	SiteSubnets     []*BaseReference `json:"site_subnets" binding:"omitempty,gte=1,lte=32"`
	Name            string           `json:"name" binding:"omitempty,min=2,max=32"`
	Inbound         int32            `json:"inbound" binding:"omitempty,min=0,max=20000"`
	Outbound        int32            `json:"outbound" binding:"omitempty,min=0,max=20000"`
	AllowSpoofing   bool             `json:"allow_spoofing" binding:"omitempty"`
	SecurityGroups  []*BaseReference `json:"security_groups" binding:"omitempty"`
}

type InterfacePatchPayload struct {
	Name            string           `json:"name" binding:"omitempty,min=2,max=32"`
	Inbound         *int32           `json:"inbound" binding:"omitempty,min=0,max=20000"`
	Outbound        *int32           `json:"outbound" binding:"omitempty,min=0,max=20000"`
	PublicAddresses []*BaseReference `json:"public_addresses,omitempty"`
	Subnets         []*BaseReference `json:"subnets" binding:"omitempty,gte=1,lte=32"`
	Count           *int             `json:"count" binding:"omitempty,gte=1,lte=512"`
	AllowSpoofing   *bool            `json:"allow_spoofing" binding:"omitempty"`
	SiteSubnets     []*BaseReference `json:"site_subnets" binding:"omitempty"`
	SecurityGroups  []*BaseReference `json:"security_groups" binding:"omitempty"`
}

// @Summary get a interface
// @Description get a interface
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} InterfaceResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /instances/{id}/interfaces/{interface_id} [get]
func (v *InterfaceAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patch instance interface %s", uuID)
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get instance %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid instance query", err)
		return
	}
	ifaceID := c.Param("interface_id")
	iface, err := interfaceAdmin.GetInterfaceByUUID(ctx, ifaceID)
	if err != nil {
		logger.Errorf("Failed to get interface %s, %+v", ifaceID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid interface query", err)
		return
	}
	interfaceResp, err := v.getInterfaceResponse(ctx, instance, iface)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get interface successfully, %s, %+v", ifaceID, interfaceResp)
	c.JSON(http.StatusOK, interfaceResp)
}

func (v *InterfaceAPI) getInterfaceResponse(ctx context.Context, instance *model.Instance, iface *model.Interface) (interfaceResp *InterfaceResponse, err error) {
	interfaceResp = &InterfaceResponse{
		BaseReference: &BaseReference{
			ID:   iface.UUID,
			Name: iface.Name,
		},
		AddressInfo: &AddressInfo{
			IPAddress: iface.Address.Address,
			Subnet: &ResourceReference{
				ID:   iface.Address.Subnet.UUID,
				Name: iface.Address.Subnet.Name,
			},
		},
		MacAddress: iface.MacAddr,
		IsPrimary:  iface.PrimaryIf,
		Inbound:    iface.Inbound,
		Outbound:   iface.Outbound,
	}
	if iface.PrimaryIf {
		if len(instance.FloatingIps) > 0 {
			floatingIps := make([]*FloatingIpInfo, len(instance.FloatingIps))
			for i, floatingip := range instance.FloatingIps {
				err = floatingIpAdmin.EnsureSubnetID(ctx, floatingip)
				if err != nil {
					logger.Error("Failed to ensure subnet_id", err)
					err = nil
					continue
				}

				floatingIps[i] = &FloatingIpInfo{
					ResourceReference: &ResourceReference{
						ID:   floatingip.UUID,
						Name: floatingip.Name,
					},
					IpAddress:  floatingip.IPAddress,
					FipAddress: floatingip.FipAddress,
					Type:       floatingip.Type,
				}

				if floatingip.Subnet != nil {
					floatingIps[i].Vlan = floatingip.Subnet.Vlan
				}
				if floatingip.Group != nil {
					floatingIps[i].Group = &BaseReference{
						ID:   floatingip.Group.UUID,
						Name: floatingip.Group.Name,
					}
				}
			}
			interfaceResp.FloatingIps = floatingIps
		}
		if len(iface.SiteSubnets) > 0 {
			for _, site := range iface.SiteSubnets {
				siteInfo := &SiteSubnetInfo{
					ResourceReference: &ResourceReference{
						ID:   site.UUID,
						Name: site.Name,
					},
					Network: site.Network,
					Gateway: site.Gateway,
					Netmask: site.Netmask,
					Start:   site.Start,
					End:     site.End,
				}
				if site.Group != nil {
					siteInfo.Group = &BaseReference{
						ID:   site.Group.UUID,
						Name: site.Group.Name,
					}
				}
				siteInfo.Vlan = site.Vlan
				interfaceResp.SiteSubnets = append(interfaceResp.SiteSubnets, siteInfo)
			}
		}
		if len(iface.SecondAddresses) > 0 {
			for _, secondAddr := range iface.SecondAddresses {
				interfaceResp.SecondaryAddresses = append(interfaceResp.SecondaryAddresses, &AddressInfo{
					IPAddress: secondAddr.Address,
					Subnet: &ResourceReference{
						ID:   secondAddr.Subnet.UUID,
						Name: secondAddr.Subnet.Name,
					},
				})
			}
		}
	}
	for _, sg := range iface.SecurityGroups {
		interfaceResp.SecurityGroups = append(interfaceResp.SecurityGroups, &ResourceReference{
			ID:   sg.UUID,
			Name: sg.Name,
		})
	}
	return
}

// @Summary patch a interface
// @Description patch a interface
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   InterfacePatchPayload  true   "Interface patch payload"
// @Success 200 {object} InterfaceResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /instances/{id}/interfaces/{interface_id} [patch]
func (v *InterfaceAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patch instance interface %s", uuID)
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get instance %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid instance query", err)
		return
	}
	ifaceID := c.Param("interface_id")
	iface, err := interfaceAdmin.GetInterfaceByUUID(ctx, ifaceID)
	if err != nil {
		logger.Errorf("Failed to get interface %s, %+v", ifaceID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid interface query", err)
		return
	}
	payload := &InterfacePatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind JSON, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	ifaceName := iface.Name
	if payload.Name != "" {
		ifaceName = payload.Name
		logger.Debugf("Update interface name to %s", ifaceName)
	}
	inbound := iface.Inbound
	if payload.Inbound != nil {
		inbound = *payload.Inbound
	}
	outbound := iface.Outbound
	if payload.Outbound != nil {
		outbound = *payload.Outbound
	}
	count := len(iface.SecondAddresses)
	if payload.Count != nil {
		count = *payload.Count - 1
	}
	allowSpoofing := iface.AllowSpoofing
	if payload.AllowSpoofing != nil {
		allowSpoofing = *payload.AllowSpoofing
	}
	secgroups := []*model.SecurityGroup{}
	if payload.SecurityGroups == nil {
		secgroups = iface.SecurityGroups
	} else {
		if len(payload.SecurityGroups) > 0 {
			for _, sg := range payload.SecurityGroups {
				var secgroup *model.SecurityGroup
				secgroup, err = secgroupAdmin.GetSecurityGroup(ctx, sg)
				if err != nil {
					logger.Errorf("Get security group failed, %+v", err)
					ErrorResponse(c, http.StatusBadRequest, "Invalid security group", err)
					return
				}
				if secgroup.RouterID != iface.Address.Subnet.RouterID {
					err = fmt.Errorf("Security group not in instance vpc")
					ErrorResponse(c, http.StatusBadRequest, "Invalid security group", err)
					return
				}
				secgroups = append(secgroups, secgroup)
			}
		} else {
			var secgroup *model.SecurityGroup
			if instance.Router != nil {
				secgroup, err = secgroupAdmin.Get(ctx, instance.Router.DefaultSG)
				if err != nil {
					logger.Errorf("Get security group failed, %+v", err)
					ErrorResponse(c, http.StatusBadRequest, "Invalid security group", err)
					return
				}
			} else {
				secgroup, err = secgroupAdmin.GetDefaultSecgroup(ctx)
				if err != nil {
					logger.Error("Get default security group failed", err)
					return
				}
			}
			secgroups = append(secgroups, secgroup)
		}
	}
	var ifaceSubnets []*model.Subnet
	var publicIps []*model.FloatingIp
	if len(payload.PublicAddresses) > 0 {
		for _, pubAddr := range payload.PublicAddresses {
			var floatingIp *model.FloatingIp
			floatingIp, err = floatingIpAdmin.GetFloatingIpByUUID(ctx, pubAddr.ID)
			if err != nil {
				return
			}
			publicIps = append(publicIps, floatingIp)
		}
	} else {
		for _, subnet := range payload.Subnets {
			var ifaceSubnet *model.Subnet
			ifaceSubnet, err = subnetAdmin.GetSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to get interface subnet")
				ErrorResponse(c, http.StatusBadRequest, "Failed to get interface subnet", err)
				return
			}
			if ifaceSubnet.Vlan != iface.Address.Subnet.Vlan {
				logger.Errorf("Invalid subnet vlan for interface")
				ErrorResponse(c, http.StatusBadRequest, "Invalid subnet vlan for interface", err)
				return
			}
			ifaceSubnets = append(ifaceSubnets, ifaceSubnet)
		}
	}
	var siteSubnets []*model.Subnet
	if !iface.PrimaryIf && len(payload.SiteSubnets) > 0 {
		logger.Errorf("Only primary interface can have site subnets")
		ErrorResponse(c, http.StatusBadRequest, "Only primary interface can have site subnets", err)
		return
	}
	for _, site := range payload.SiteSubnets {
		var siteSubnet *model.Subnet
		siteSubnet, err = subnetAdmin.GetSubnet(ctx, site)
		if err != nil {
			logger.Errorf("Failed to get site subnet")
			ErrorResponse(c, http.StatusBadRequest, "Failed to get site subnet", err)
			return
		}
		siteSubnets = append(siteSubnets, siteSubnet)
	}
	err = interfaceAdmin.Update(ctx, instance, iface, ifaceName, inbound, outbound, allowSpoofing, secgroups, ifaceSubnets, siteSubnets, count, publicIps)
	if err != nil {
		logger.Errorf("Patch instance failed, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Patch instance failed", err)
		return
	}
	interfaceResp, err := v.getInterfaceResponse(ctx, instance, iface)
	if err != nil {
		logger.Errorf("Get interface responsefailed, %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, interfaceResp)
}

// @Summary delete a interface
// @Description delete a interface
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /instance/{id}/interfaces/{interface_id} [delete]
func (v *InterfaceAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get instance: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
		return
	}
	ifaceID := c.Param("interface_id")
	iface, err := interfaceAdmin.GetInterfaceByUUID(ctx, ifaceID)
	if err != nil {
		logger.Errorf("Failed to get interface %s, %+v", ifaceID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid interface query", err)
		return
	}
	err = interfaceAdmin.Delete(ctx, instance, iface)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (v *InterfaceAPI) getInterfaceInfo(ctx context.Context, vpc *model.Router, ifacePayload *InterfacePayload) (router *model.Router, ifaceInfo *routes.InterfaceInfo, err error) {
	logger.Debugf("Get interface info with VPC %+v, ifacePayload %+v", vpc, ifacePayload)
	if ifacePayload == nil {
		err = fmt.Errorf("Interface can not be nill")
		return
	}
	if len(ifacePayload.Subnets) == 0 && ifacePayload.Subnet != nil {
		ifacePayload.Subnets = append(ifacePayload.Subnets, ifacePayload.Subnet)
	}
	routerID := int64(0)
	router = vpc
	if router != nil {
		routerID = router.ID
	}
	ifaceInfo = &routes.InterfaceInfo{
		AllowSpoofing: ifacePayload.AllowSpoofing,
		Count:         ifacePayload.Count,
	}
	vlan := int64(0)
	if len(ifacePayload.PublicAddresses) > 0 {
		for _, pubAddr := range ifacePayload.PublicAddresses {
			var floatingIp *model.FloatingIp
			floatingIp, err = floatingIpAdmin.GetFloatingIpByUUID(ctx, pubAddr.ID)
			if err != nil {
				return
			}

			err = floatingIpAdmin.EnsureSubnetID(ctx, floatingIp)
			if err != nil {
				logger.Error("Failed to ensure subnet_id", err)
				return
			}

			if vlan == 0 {
				vlan = floatingIp.Interface.Address.Subnet.Vlan
			} else if vlan != floatingIp.Interface.Address.Subnet.Vlan {
				err = fmt.Errorf("All public IPs must be from the same vlan")
				return
			}
			ifaceInfo.PublicIps = append(ifaceInfo.PublicIps, floatingIp)
		}
	} else {
		if len(ifacePayload.Subnets) == 0 {
			err = fmt.Errorf("Subnets or public addresses must be provided")
			return
		}
		for _, snet := range ifacePayload.Subnets {
			var subnet *model.Subnet
			subnet, err = subnetAdmin.GetSubnet(ctx, snet)
			if err != nil {
				return
			}
			if vlan == 0 {
				vlan = subnet.Vlan
			} else if vlan != subnet.Vlan {
				err = fmt.Errorf("All subnets must be in the same vlan")
				return
			}
			if router == nil && subnet.RouterID > 0 {
				router, err = routerAdmin.Get(ctx, subnet.RouterID)
				if err != nil {
					return
				}
				routerID = subnet.RouterID
			}
			if router != nil && router.ID != subnet.RouterID {
				err = fmt.Errorf("VPC of subnet must be the same with VPC of instance")
				return
			}
			ifaceInfo.Subnets = append(ifaceInfo.Subnets, subnet)
		}
		if len(ifaceInfo.Subnets) == 0 {
			err = fmt.Errorf("No valid subnets specified")
			return
		}
	}
	for _, ipSite := range ifacePayload.SiteSubnets {
		var site *model.Subnet
		site, err = subnetAdmin.GetSubnet(ctx, ipSite)
		if err != nil {
			return
		}
		if vlan != site.Vlan {
			err = fmt.Errorf("All subnets including sites must be in the same vlan")
			return
		}
		if site.Interface > 0 {
			err = fmt.Errorf("Site subnet is not available")
			return
		}
		ifaceInfo.SiteSubnets = append(ifaceInfo.SiteSubnets, site)
	}
	if ifacePayload.IpAddress != "" {
		ifaceInfo.IpAddress = ifacePayload.IpAddress
	}
	if ifacePayload.MacAddress != "" {
		ifaceInfo.MacAddress = ifacePayload.MacAddress
	}
	if ifacePayload.Inbound > 0 {
		ifaceInfo.Inbound = ifacePayload.Inbound
	}
	if ifacePayload.Outbound > 0 {
		ifaceInfo.Outbound = ifacePayload.Outbound
	}
	if len(ifacePayload.SecurityGroups) == 0 {
		var routerID, sgID int64
		var secgroup *model.SecurityGroup
		if router != nil {
			routerID = router.ID
			sgID = router.DefaultSG
			secgroup, err = secgroupAdmin.Get(ctx, sgID)
			if err != nil {
				return
			}
			if secgroup.RouterID != routerID {
				err = fmt.Errorf("Security group not in subnet vpc")
				return
			}
		} else {
			secgroup, err = secgroupAdmin.GetDefaultSecgroup(ctx)
			if err != nil {
				logger.Error("Get default security group failed", err)
				return
			}
		}
		ifaceInfo.SecurityGroups = append(ifaceInfo.SecurityGroups, secgroup)
	} else {
		for _, sg := range ifacePayload.SecurityGroups {
			var secgroup *model.SecurityGroup
			secgroup, err = secgroupAdmin.GetSecurityGroup(ctx, sg)
			if err != nil {
				return
			}
			if secgroup.RouterID != routerID {
				err = fmt.Errorf("Security group not in subnet vpc")
				return
			}
			ifaceInfo.SecurityGroups = append(ifaceInfo.SecurityGroups, secgroup)
		}
	}
	logger.Debugf("Get interface info success, router %+v, ifaceInfo %+v", router, ifaceInfo)
	return
}

// @Summary create a interface
// @Description create a interface
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   InterfacePayload  true   "Interface create payload"
// @Success 200 {object} InterfaceResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /instance/{id}/interfaces [post]
func (v *InterfaceAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	payload := &InterfacePayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get instance: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
		return
	}
	_, ifaceInfo, err := v.getInterfaceInfo(ctx, nil, payload)
	if err != nil {
		logger.Errorf("Failed to get interface %+v, %+v", payload, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid primary interface", err)
		return
	}
	iface, err := interfaceAdmin.Create(ctx, instance, ifaceInfo.MacAddress, ifaceInfo.IpAddress, ifaceInfo.Inbound, ifaceInfo.Outbound, ifaceInfo.AllowSpoofing, ifaceInfo.SecurityGroups, ifaceInfo.Subnets, ifaceInfo.Count-1)
	if err != nil {
		logger.Errorf("Failed to create subnet, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create subnet", err)
		return
	}
	interfaceResp, err := v.getInterfaceResponse(ctx, instance, iface)
	if err != nil {
		logger.Errorf("Failed to get interface response, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, interfaceResp)
}

// @Summary list interfaces
// @Description list interfaces
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {array} InterfaceResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /instance/{id}/interfaces [get]
func (v *InterfaceAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	logger.Debugf("List interfaces for instance %s, offset:%s, limit:%s", uuID, offsetStr, limitStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Failed to parse offset: %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Failed to parse limit: %s, %+v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		errStr := "Invalid query offset or limit, cannot be negative"
		logger.Errorf(errStr)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", errors.New(errStr))
		return
	}
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get instance: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to get instance", err)
		return
	}
	total, interfaces, err := interfaceAdmin.List(ctx, int64(offset), int64(limit), "-created_at", instance)
	if err != nil {
		logger.Errorf("Failed to list interfaces, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list secrules", err)
		return
	}
	interfaceListResp := &InterfaceListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(interfaces),
	}
	interfaceListResp.Interfaces = make([]*InterfaceResponse, interfaceListResp.Limit)
	for i, iface := range interfaces {
		interfaceListResp.Interfaces[i], err = v.getInterfaceResponse(ctx, instance, iface)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List secrules successfully for SG %s, %+v", uuID, interfaceListResp)
	c.JSON(http.StatusOK, interfaceListResp)
}
