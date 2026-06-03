/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-macaron/session"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	macaron "gopkg.in/macaron.v1"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
)

var (
	apiKeyAdmin = &APIKeyAdmin{}
	apiKeyView  = &APIKeyView{}
)

type APIKeyAdmin struct{}
type APIKeyView struct{}

// GenerateAPIKey creates a new key in format cl_<uuid>_<32-byte-hex>.
// Returns full plain-text key (show once), UUID lookup key, and bcrypt hash.
func GenerateAPIKey() (fullKey, uuidKey, hash string, err error) {
	uuidKey = uuid.New().String()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to generate random secret", err)
		return
	}
	secretHex := hex.EncodeToString(secret)
	fullKey = fmt.Sprintf("cl_%s_%s", uuidKey, secretHex)
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(secretHex), bcrypt.DefaultCost)
	if err != nil {
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to hash API key secret", err)
		return
	}
	hash = string(hashBytes)
	return
}

// ParseAPIKey splits cl_<uuid>_<secret> into UUID and secret components.
// UUID is always 36 chars, so position is deterministic.
func ParseAPIKey(fullKey string) (uuidKey, secret string, err error) {
	if !strings.HasPrefix(fullKey, "cl_") {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: missing 'cl_' prefix", nil)
		return
	}
	rest := fullKey[3:]
	if len(rest) < 101 { // uuid(36) + separator(1) + hex-secret(64)
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: too short", nil)
		return
	}
	if rest[36] != '_' {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: missing separator after UUID", nil)
		return
	}
	uuidKey = rest[:36]
	secret = rest[37:]
	if secret == "" {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: empty secret", nil)
	}
	return
}

func isAPIKeyExpired(expiresAt *time.Time) bool {
	if expiresAt == nil {
		return false
	}
	return expiresAt.Before(time.Now())
}

func (a *APIKeyAdmin) Create(ctx context.Context, name, description string, expiresAt *time.Time) (
	apiKey *model.APIKey, plainKey string, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		err = NewCLError(ErrPermissionDenied, "Not authorized to create API keys", nil)
		return
	}
	fullKey, uuidKey, hash, err := GenerateAPIKey()
	if err != nil {
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	apiKey = &model.APIKey{
		Model:       model.Model{Creater: memberShip.UserID, UUID: uuidKey},
		Owner:       memberShip.OrgID,
		UserID:      memberShip.UserID,
		Name:        name,
		Description: description,
		APIKey:      uuidKey,
		APIKeyHash:  hash,
		ExpiresAt:   expiresAt,
	}
	err = db.Create(apiKey).Error
	if err != nil {
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to create API key", err)
		return
	}
	plainKey = fullKey
	return
}

func (a *APIKeyAdmin) List(ctx context.Context, offset, limit int64, order, query string) (
	total int64, keys []*model.APIKey, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}
	if order == "" {
		order = "-created_at"
	}
	q := db.Model(&model.APIKey{}).Where("user_id = ?", memberShip.UserID)
	if query != "" {
		q = q.Where("name LIKE ?", "%"+query+"%")
	}
	if err = q.Count(&total).Error; err != nil {
		err = NewCLError(ErrDatabaseError, "Failed to count API keys", err)
		return
	}
	q2 := db.Where("user_id = ?", memberShip.UserID)
	if query != "" {
		q2 = q2.Where("name LIKE ?", "%"+query+"%")
	}
	q2 = dbs.Sortby(q2.Offset(offset).Limit(limit), order)
	if err = q2.Find(&keys).Error; err != nil {
		err = NewCLError(ErrDatabaseError, "Failed to list API keys", err)
	}
	return
}

func (a *APIKeyAdmin) GetByUUID(ctx context.Context, uuID string) (apiKey *model.APIKey, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	apiKey = &model.APIKey{}
	err = db.Where("uuid = ? AND user_id = ?", uuID, memberShip.UserID).Take(apiKey).Error
	if err != nil {
		err = NewCLError(ErrAPIKeyNotFound, "API key not found", err)
	}
	return
}

func (a *APIKeyAdmin) Update(ctx context.Context, uuID string, disabled bool) (apiKey *model.APIKey, err error) {
	apiKey, err = a.GetByUUID(ctx, uuID)
	if err != nil {
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	err = db.Model(apiKey).Update("disabled", disabled).Error
	if err != nil {
		err = NewCLError(ErrAPIKeyUpdateFailed, "Failed to update API key", err)
		return
	}
	apiKey.Disabled = disabled
	return
}

func (a *APIKeyAdmin) Delete(ctx context.Context, uuID string) (err error) {
	apiKey, err := a.GetByUUID(ctx, uuID)
	if err != nil {
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if err = db.Delete(apiKey).Error; err != nil {
		err = NewCLError(ErrAPIKeyDeleteFailed, "Failed to delete API key", err)
	}
	return
}

// ValidateAPIKey looks up the key by UUID prefix and verifies the bcrypt hash.
func (a *APIKeyAdmin) ValidateAPIKey(fullKey string) (apiKey *model.APIKey, err error) {
	uuidKey, secret, err := ParseAPIKey(fullKey)
	if err != nil {
		return
	}
	db := DB()
	apiKey = &model.APIKey{}
	if err = db.Where("api_key = ?", uuidKey).Take(apiKey).Error; err != nil {
		err = NewCLError(ErrAPIKeyNotFound, "API key not found", err)
		return
	}
	if apiKey.Disabled {
		err = NewCLError(ErrAPIKeyDisabled, "API key is disabled", nil)
		return
	}
	if isAPIKeyExpired(apiKey.ExpiresAt) {
		err = NewCLError(ErrAPIKeyExpired, "API key has expired", nil)
		return
	}
	if err = bcrypt.CompareHashAndPassword([]byte(apiKey.APIKeyHash), []byte(secret)); err != nil {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key secret", err)
		return
	}
	return
}

func (v *APIKeyView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Reader) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listConfig, offset, limit := GetPaginationParams(c, "api_keys")
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, keys, err := apiKeyAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["APIKeys"] = keys
	c.Data["Query"] = query
	SetPaginationData(c, "api_keys", total, limit, offset, listConfig,
		`["Name", "ExpiresAt", "Disabled", "CreatedAt", "Action"]`,
		[]string{"UUID", "Name", "ExpiresAt", "Disabled", "CreatedAt", "Action"})
	c.HTML(200, "api_keys")
}

func (v *APIKeyView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Writer) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "api_keys_new")
}

func (v *APIKeyView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Writer) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	description := c.QueryTrim("description")
	expiresAtStr := c.QueryTrim("expires_at")
	var expiresAt *time.Time
	if expiresAtStr != "" {
		t, err := time.Parse(TimeStringForMat, expiresAtStr)
		if err != nil {
			c.Data["ErrorMsg"] = "Invalid expires_at format, expected: " + TimeStringForMat
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		expiresAt = &t
	}
	apiKey, plainKey, err := apiKeyAdmin.Create(ctx, name, description, expiresAt)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["APIKey"] = apiKey
	c.Data["PlainKey"] = plainKey
	c.HTML(200, "api_keys_created")
}

func (v *APIKeyView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Writer) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	uuID := c.Params("id")
	apiKey, err := apiKeyAdmin.GetByUUID(ctx, uuID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["APIKey"] = apiKey
	c.HTML(200, "api_keys_patch")
}

func (v *APIKeyView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Writer) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	uuID := c.Params("id")
	disabledStr := c.QueryTrim("disabled")
	disabled, err := strconv.ParseBool(disabledStr)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid disabled value"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err = apiKeyAdmin.Update(ctx, uuID, disabled)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect("../api_keys")
}

func (v *APIKeyView) Delete(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	uuID := c.Params("id")
	if uuID == "" {
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	if err := apiKeyAdmin.Delete(ctx, uuID); err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "api_keys",
	})
}
