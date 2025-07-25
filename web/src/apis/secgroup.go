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

var secgroupAPI = &SecgroupAPI{}
var secgroupAdmin = &routes.SecgroupAdmin{}

type SecgroupAPI struct{}

type SecurityGroupResponse struct {
	*ResourceReference
	IsDefault        bool               `json:"is_default"`
	VPC              *ResourceReference `json:"vpc,omitempty"`
	TargetInterfaces []*TargetInterface `json:"target_interfaces,omitempty"`
}

type SecurityGroupListResponse struct {
	Offset         int                      `json:"offset"`
	Total          int                      `json:"total"`
	Limit          int                      `json:"limit"`
	SecurityGroups []*SecurityGroupResponse `json:"security_groups"`
}

type SecurityGroupPayload struct {
	Name      string         `json:"name" binding:"required,min=2,max=32"`
	VPC       *BaseReference `json:"vpc" binding:"omitempty"`
	IsDefault bool           `json:"is_default" binding:"omitempty"`
}

type SecurityGroupPatchPayload struct {
	Name      string `json:"name" binding:"required,min=2,max=32"`
	IsDefault bool   `json:"is_default" binding:"omitempty"`
}

// @Summary get a secgroup
// @Description get a secgroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} SecurityGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /security_groups/{id} [get]
func (v *SecgroupAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Get secgroup %s", uuID)
	secgroup, err := secgroupAdmin.GetSecgroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get secgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid security group query", err)
		return
	}
	secgroupResp, err := v.getSecgroupResponse(ctx, secgroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get secgroup successfully, %s, %+v", uuID, secgroupResp)
	c.JSON(http.StatusOK, secgroupResp)
}

// @Summary patch a secgroup
// @Description patch a secgroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   SecurityGroupPatchPayload  true   "Secgroup patch payload"
// @Success 200 {object} SecurityGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /security_groups/{id} [patch]
func (v *SecgroupAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Patch secgroup %s", uuID)
	secgroup, err := secgroupAdmin.GetSecgroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get secgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid security group query", err)
		return
	}
	payload := &SecurityGroupPatchPayload{}
	err = c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Patching secgroup %s with %+v", uuID, payload)
	if payload.IsDefault == false {
		logger.Errorf("Not allowed to patch default security group to false")
		ErrorResponse(c, http.StatusBadRequest, "Not allowed to patch default security group to false", err)
		return
	}
	err = secgroupAdmin.Update(ctx, secgroup, payload.Name, payload.IsDefault)
	if err != nil {
		logger.Errorf("Failed to patch secgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Patch security group failed", err)
		return
	}
	secgroupResp, err := v.getSecgroupResponse(ctx, secgroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch secgroup successfully, %s, %+v", uuID, secgroupResp)
	c.JSON(http.StatusOK, secgroupResp)
}

// @Summary delete a secgroup
// @Description delete a secgroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /security_groups/{id} [delete]
func (v *SecgroupAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Delete secgroup %s", uuID)
	secgroup, err := secgroupAdmin.GetSecgroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get secgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = secgroupAdmin.Delete(ctx, secgroup)
	if err != nil {
		logger.Errorf("Failed to delete secgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a secgroup
// @Description create a secgroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   SecurityGroupPayload  true   "Secgroup create payload"
// @Success 200 {object} SecurityGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /security_groups [post]
func (v *SecgroupAPI) Create(c *gin.Context) {
	logger.Debugf("Create secgroup")
	ctx := c.Request.Context()
	payload := &SecurityGroupPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating secgroup with %+v", payload)
	var router *model.Router
	if payload.VPC != nil {
		router, err = routerAdmin.GetRouter(ctx, payload.VPC)
		if err != nil {
			logger.Errorf("Failed to get vpc %+v, %+v", payload.VPC, err)
			ErrorResponse(c, http.StatusBadRequest, "Failed to get vpc", err)
			return
		}
	}
	secgroup, err := secgroupAdmin.Create(ctx, payload.Name, payload.IsDefault, router)
	if err != nil {
		logger.Errorf("Failed to create secgroup %+v, %+v", payload, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	secgroupResp, err := v.getSecgroupResponse(ctx, secgroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create secgroup successfully, %+v", secgroupResp)
	c.JSON(http.StatusOK, secgroupResp)
}

func (v *SecgroupAPI) getSecgroupResponse(ctx context.Context, secgroup *model.SecurityGroup) (secgroupResp *SecurityGroupResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, secgroup.Owner)
	secgroupResp = &SecurityGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        secgroup.UUID,
			Name:      secgroup.Name,
			Owner:     owner,
			CreatedAt: secgroup.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: secgroup.UpdatedAt.Format(TimeStringForMat),
		},
		IsDefault: secgroup.IsDefault,
	}
	if secgroup.Router != nil {
		secgroupResp.VPC = &ResourceReference{
			ID:   secgroup.Router.UUID,
			Name: secgroup.Router.Name,
		}
	}
	err = secgroupAdmin.GetSecgroupInterfaces(ctx, secgroup)
	if err != nil {
		return
	}
	for _, iface := range secgroup.Interfaces {
		targetIface := &TargetInterface{
			ResourceReference: &ResourceReference{
				ID: iface.UUID,
			},
		}
		if iface.Address != nil {
			targetIface.IpAddress = strings.Split(iface.Address.Address, "/")[0]
		}
		if iface.Instance > 0 {
			var instance *model.Instance
			instance, err = instanceAdmin.Get(ctx, iface.Instance)
			if err != nil {
				err = nil
				continue
			}
			owner := orgAdmin.GetOrgName(ctx, instance.Owner)
			targetIface.FromInstance = &InstanceInfo{
				ResourceReference: &ResourceReference{
					ID:    instance.UUID,
					Owner: owner,
				},
				Hostname: instance.Hostname,
			}
		}
		secgroupResp.TargetInterfaces = append(secgroupResp.TargetInterfaces, targetIface)
	}
	return
}

// @Summary list secgroups
// @Description list secgroups
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} SecurityGroupListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /security_groups [get]
func (v *SecgroupAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	vpcID := strings.TrimSpace(c.DefaultQuery("vpc_id", ""))
	logger.Debugf("List secgroups with offset %s, limit %s, query %s, vpc_id %s", offsetStr, limitStr, queryStr, vpcID)

	if vpcID != "" {
		logger.Debugf("Filtering secgroups by VPC ID: %s", vpcID)
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
	total, secgroups, err := secgroupAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("Failed to list secgroups, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list secgroups", err)
		return
	}
	secgroupListResp := &SecurityGroupListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(secgroups),
	}
	secgroupListResp.SecurityGroups = make([]*SecurityGroupResponse, secgroupListResp.Limit)
	for i, secgroup := range secgroups {
		secgroupListResp.SecurityGroups[i], err = v.getSecgroupResponse(ctx, secgroup)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List secgroups successfully, %+v", secgroupListResp)
	c.JSON(http.StatusOK, secgroupListResp)
}
