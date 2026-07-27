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

// authorizeWithAPIKey authenticates a request using the X-API-Key header value.
// It validates the key, resolves the caller's membership from the key's owning
// user and org, injects it into the request context, and continues the chain.
// On any failure it writes an error response and aborts the request.
func authorizeWithAPIKey(c *gin.Context, fullKey string) {
	apiKey, err := apiKeyAdmin.ValidateAPIKey(fullKey)
	if err != nil {
		// Error handling: key invalid / not found / disabled / expired.
		logger.Errorf("API key validation failed: %v", err)
		ErrorResponse(c, http.StatusUnauthorized, "Invalid API Key", err)
		c.Abort()
		return
	}
	// Resolve membership directly from the key's creater (user ID) and owner (org ID).
	// GetDBMemberShip uses a raw DB query, so it does not depend on the membership
	// being present in the context yet (avoids the GetWhere() owner=0 lookup bug).
	memberShip, err := GetDBMemberShip(apiKey.Creater, apiKey.Owner)
	if err != nil {
		// Error handling: the key's user/org membership no longer exists.
		logger.Errorf("Failed to resolve membership for API key %s (user: %d, org: %d): %v",
			apiKey.UUID, apiKey.Creater, apiKey.Owner, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid membership", err)
		c.Abort()
		return
	}
	// Branch: same as the JWT / Web path — elevate the built-in admin user to the
	// Admin role. Its member record is Role=Owner, and GetWhere() only returns the
	// unscoped (all-rows) clause when Role==Admin, so without this an admin's API
	// key would be confined to the admin org.
	if memberShip.UserName == "admin" {
		memberShip.Role = model.Admin
	}
	logger.Debugf("Authorized via API key %s, user: %s, org: %s, role: %d",
		apiKey.UUID, memberShip.UserName, memberShip.OrgName, memberShip.Role)
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
