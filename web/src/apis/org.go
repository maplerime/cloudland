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

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var orgAPI = &OrgAPI{}
var orgAdmin = &routes.OrgAdmin{}

type OrgAPI struct{}

type MemberInfo struct {
	*ResourceReference
	Role string `json:"role"`
}

type OrgResponse struct {
	*ResourceReference
	Members []*MemberInfo `json:"members"`
}

type OrgListResponse struct {
	Offset int            `json:"offset"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Orgs   []*OrgResponse `json:"orgs"`
}

type OrgPayload struct {
}

type OrgPatchPayload struct {
}

// @Summary get a org
// @Description get a org
// @tags Authorization
// @Accept  json
// @Produce json
// @Success 200 {object} OrgResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /orgs/{id} [get]
func (v *OrgAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Get org by uuid: %s", uuID)
	org, err := orgAdmin.GetOrgByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get org by uuid: %s", uuID)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	orgResp, err := v.getOrgResponse(ctx, org)
	if err != nil {
		logger.Errorf("Failed to get org response: %s", uuID)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Got org : %+v", orgResp)
	c.JSON(http.StatusOK, orgResp)
}

func (v *OrgAPI) getOrgResponse(ctx context.Context, org *model.Organization) (orgResp *OrgResponse, err error) {
	orgResp = &OrgResponse{
		ResourceReference: &ResourceReference{
			ID:   org.UUID,
			Name: org.Name,
		},
	}
	for _, member := range org.Members {
		orgResp.Members = append(orgResp.Members, &MemberInfo{
			ResourceReference: &ResourceReference{
				ID:   member.UUID,
				Name: member.UserName,
			},
			Role: member.Role.String(),
		})
	}
	return
}

// @Summary patch a org
// @Description patch a org
// @tags Authorization
// @Accept  json
// @Produce json
// @Param   message	body   OrgPatchPayload  true   "Org patch payload"
// @Success 200 {object} OrgResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /orgs/{id} [patch]
func (v *OrgAPI) Patch(c *gin.Context) {
	orgResp := &OrgResponse{}
	c.JSON(http.StatusOK, orgResp)
}

// @Summary delete a org
// @Description delete a org
// @tags Authorization
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /orgs/{id} [delete]
func (v *OrgAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Deleting org %s", uuID)
	org, err := orgAdmin.GetOrgByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get org by uuid: %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = orgAdmin.Delete(ctx, org)
	if err != nil {
		logger.Errorf("Failed to delete org %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a org
// @Description create a org
// @tags Authorization
// @Accept  json
// @Produce json
// @Param   message	body   OrgPayload  true   "Org create payload"
// @Success 200 {object} OrgResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /orgs [post]
func (v *OrgAPI) Create(c *gin.Context) {
	orgResp := &OrgResponse{}
	c.JSON(http.StatusOK, orgResp)
}

// @Summary list orgs
// @Description list orgs
// @tags Authorization
// @Accept  json
// @Produce json
// @Success 200 {object} OrgListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /orgs [get]
func (v *OrgAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	logger.Debugf("List users, offset:%s, limit:%s, query:%s", offsetStr, limitStr, queryStr)
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
	total, orgs, err := orgAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("Failed to list orgs, %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list orgs", err)
		return
	}
	orgListResp := &OrgListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(orgs),
	}
	orgListResp.Orgs = make([]*OrgResponse, orgListResp.Limit)
	for i, org := range orgs {
		orgListResp.Orgs[i], err = v.getOrgResponse(ctx, org)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List orgs successfully, %+v", orgListResp)
	c.JSON(http.StatusOK, orgListResp)
}
