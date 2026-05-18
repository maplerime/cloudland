/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"net/http"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

const (
	TokenType = "bearer"
	AppName   = "Cloudland"
)

// Authorize checks X-API-Key first; if absent falls back to JWT.
func Authorize() gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKeyStr := c.Request.Header.Get("X-API-Key"); apiKeyStr != "" {
			authorizeWithAPIKey(c, apiKeyStr)
			return
		}
		authorizeWithJWT(c)
	}
}

func authorizeWithAPIKey(c *gin.Context, fullKey string) {
	apiKey, err := apiKeyAdmin.ValidateAPIKey(fullKey)
	if err != nil {
		ErrorResponse(c, http.StatusUnauthorized, "Invalid API Key", err)
		c.Abort()
		return
	}
	user, err := userAdmin.Get(c.Request.Context(), apiKey.UserID)
	if err != nil {
		ErrorResponse(c, http.StatusUnauthorized, "API Key user not found", err)
		c.Abort()
		return
	}
	realOrg := user.Username
	org, err := orgAdmin.GetOrgByName(c.Request.Context(), realOrg)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid resource org", err)
		c.Abort()
		return
	}
	memberShip, err := GetDBMemberShip(user.ID, org.ID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid membership", err)
		c.Abort()
		return
	}
	ctx := memberShip.SetContext(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

func authorizeWithJWT(c *gin.Context) {
	tokenStr := c.Request.Header.Get("Authorization")
	if tokenStr == "" {
		ErrorResponse(c, http.StatusUnauthorized, "Invalid Token", nil)
		c.Abort()
		return
	}
	tokenStr = tokenStr[len(TokenType)+1:]
	_, claims, err := routes.ParseToken(tokenStr)
	if err != nil {
		ErrorResponse(c, http.StatusUnauthorized, "Invalid Token", err)
		c.Abort()
		return
	}
	if claims.Issuer != AppName {
		ErrorResponse(c, http.StatusUnauthorized, "Invalid Token", nil)
		c.Abort()
		return
	}

	reqUser := ""
	if len(claims.Audience) > 0 {
		reqUser = claims.Audience[0]
	}
	reqOrg := claims.Subject
	realUser := c.Request.Header.Get("X-Resource-User")
	realOrg := c.Request.Header.Get("X-Resource-Org")
	if realUser != "" || realOrg != "" {
		if reqUser != "admin" {
			ErrorResponse(c, http.StatusUnauthorized, "Not authorized to change resource owner", nil)
			c.Abort()
			return
		}
	}
	if realUser == "" {
		realUser = reqUser
		realOrg = reqOrg
	}
	user, err := userAdmin.GetUserByName(realUser)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid resource user", err)
		c.Abort()
		return
	}
	if realOrg == "" {
		realOrg = realUser
	}
	org, err := orgAdmin.GetOrgByName(c.Request.Context(), realOrg)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid resource org", err)
		c.Abort()
		return
	}
	memberShip, err := GetDBMemberShip(user.ID, org.ID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid resource user with org membership", err)
		c.Abort()
		return
	}
	if realUser == "admin" {
		memberShip.Role = model.Admin
	}
	logger.Infof("MemberShip: %v\n", memberShip)
	ctx := memberShip.SetContext(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}
