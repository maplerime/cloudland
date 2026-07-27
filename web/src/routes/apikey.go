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

// APIKeyAdmin provides the service-layer CRUD and validation operations for
// API keys. It enforces membership-based permission checks and org-scoped
// visibility, and is shared by both the REST API and the Web Console handlers.
type APIKeyAdmin struct{}

// APIKeyView holds the Web Console (Macaron) request handlers for API keys.
type APIKeyView struct{}

// GenerateAPIKey creates a new key in the format cl_<uuid>_<32-byte-hex>.
// It returns the full plain-text key (to be shown to the user exactly once),
// the UUID lookup key persisted in the api_key column, and the bcrypt hash of
// the secret persisted in the api_key_hash column.
// It returns ErrAPIKeyCreationFailed if secure random generation or hashing fails.
func GenerateAPIKey() (fullKey, uuidKey, hash string, err error) {
	uuidKey = uuid.New().String()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		// Secure random source failed; cannot mint a key.
		logger.Errorf("Failed to read random bytes for API key secret: %v", err)
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to generate random secret", err)
		return
	}
	secretHex := hex.EncodeToString(secret)
	fullKey = fmt.Sprintf("cl_%s_%s", uuidKey, secretHex)
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(secretHex), bcrypt.DefaultCost)
	if err != nil {
		// Hashing failed; do not return a key that cannot be validated later.
		logger.Errorf("Failed to bcrypt-hash API key secret: %v", err)
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to hash API key secret", err)
		return
	}
	hash = string(hashBytes)
	logger.Debugf("Generated API key with uuid: %s", uuidKey)
	return
}

// ParseAPIKey splits a cl_<uuid>_<secret> key into its UUID and secret
// components. The UUID is always 36 chars so the separator position is
// deterministic. It returns ErrAPIKeyInvalid if the format is malformed.
func ParseAPIKey(fullKey string) (uuidKey, secret string, err error) {
	// Branch: reject keys without the expected scheme prefix.
	if !strings.HasPrefix(fullKey, "cl_") {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: missing 'cl_' prefix", nil)
		return
	}
	rest := fullKey[3:]
	// Branch: a valid key is at least uuid(36) + separator(1) + hex-secret(64).
	if len(rest) < 101 {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: too short", nil)
		return
	}
	// Branch: the 37th char (index 36) must be the uuid/secret separator.
	if rest[36] != '_' {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: missing separator after UUID", nil)
		return
	}
	uuidKey = rest[:36]
	secret = rest[37:]
	// Branch: guard against an empty secret portion.
	if secret == "" {
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key format: empty secret", nil)
	}
	return
}

// isAPIKeyExpired reports whether the given expiry time is in the past.
// A nil expiry means the key never expires.
func isAPIKeyExpired(expiresAt *time.Time) bool {
	if expiresAt == nil {
		return false
	}
	return expiresAt.Before(time.Now())
}

// Create mints a new API key owned by the caller's org and user. It returns
// the persisted record and the plain-text key (plainKey), which the caller
// must surface to the user exactly once and never store.
// It returns ErrPermissionDenied if the caller lacks Writer permission, or
// ErrAPIKeyCreationFailed if key generation or the DB insert fails.
func (a *APIKeyAdmin) Create(ctx context.Context, name, description string, expiresAt *time.Time) (
	apiKey *model.APIKey, plainKey string, err error) {
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Creating API key, name: %s, owner: %d, creater: %d", name, memberShip.OrgID, memberShip.UserID)
	// Branch: only writers (or higher) may create API keys.
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Errorf("Not authorized to create API key, user: %d", memberShip.UserID)
		err = NewCLError(ErrPermissionDenied, "Not authorized to create API keys", nil)
		return
	}
	fullKey, uuidKey, hash, err := GenerateAPIKey()
	if err != nil {
		// Generation already logged the underlying cause.
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
		Name:        name,
		Description: description,
		APIKey:      uuidKey,
		APIKeyHash:  hash,
		ExpiresAt:   expiresAt,
	}
	err = db.Create(apiKey).Error
	if err != nil {
		// Error handling: surface a typed creation error.
		logger.Errorf("Failed to insert API key (uuid: %s): %v", uuidKey, err)
		err = NewCLError(ErrAPIKeyCreationFailed, "Failed to create API key", err)
		return
	}
	plainKey = fullKey
	logger.Debugf("Created API key, uuid: %s, name: %s", uuidKey, name)
	return
}

// List returns the API keys visible to the caller, scoped by membership:
// a regular user sees only their own org's keys (owner = OrgID), while a global
// admin sees all keys. For admins, each key's owning organization is preloaded
// into OwnerInfo so the Console can display it. It supports an optional name
// LIKE filter plus offset/limit/order pagination.
// It returns ErrPermissionDenied without Reader permission, ErrDatabaseError on
// query failure, or ErrOwnerNotFound if an owner org cannot be resolved.
func (a *APIKeyAdmin) List(ctx context.Context, offset, limit int64, order, query string) (
	total int64, keys []*model.APIKey, err error) {
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Listing API keys, offset: %d, limit: %d, query: %s", offset, limit, query)
	// Branch: listing requires at least Reader permission.
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Errorf("Not authorized to list API keys, user: %d", memberShip.UserID)
		err = NewCLError(ErrPermissionDenied, "Not authorized to list API keys", nil)
		return
	}
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}
	if order == "" {
		order = "-created_at"
	}
	// where is empty for global admins (all rows) and "owner = <OrgID>" otherwise.
	where := memberShip.GetWhere()
	q := db.Model(&model.APIKey{}).Where(where)
	if query != "" {
		q = q.Where("name LIKE ?", "%"+query+"%")
	}
	if err = q.Count(&total).Error; err != nil {
		// Error handling: count query failed.
		logger.Errorf("Failed to count API keys: %v", err)
		err = NewCLError(ErrDatabaseError, "Failed to count API keys", err)
		return
	}
	q2 := db.Where(where)
	if query != "" {
		q2 = q2.Where("name LIKE ?", "%"+query+"%")
	}
	q2 = dbs.Sortby(q2.Offset(offset).Limit(limit), order)
	if err = q2.Find(&keys).Error; err != nil {
		// Error handling: page query failed.
		logger.Errorf("Failed to query API keys: %v", err)
		err = NewCLError(ErrDatabaseError, "Failed to list API keys", err)
		return
	}
	// Branch: only admins need (and are allowed to see) the owning org of each key.
	if memberShip.CheckPermission(model.Admin) {
		db = db.Offset(0).Limit(-1)
		// Loop: resolve the owning organization for every returned key.
		for _, key := range keys {
			key.OwnerInfo = &model.Organization{Model: model.Model{ID: key.Owner}}
			if err = db.Take(key.OwnerInfo).Error; err != nil {
				logger.Errorf("Failed to query owner info for API key %s: %v", key.UUID, err)
				err = NewCLError(ErrOwnerNotFound, "Owner organization not found", err)
				return
			}
		}
	}
	logger.Debugf("Listed %d API key(s), total: %d", len(keys), total)
	return
}

// GetByUUID fetches a single API key by its UUID, constrained to the caller's
// visibility scope (own org for regular users, all for global admin).
// It returns ErrAPIKeyNotFound if no matching key is visible to the caller.
func (a *APIKeyAdmin) GetByUUID(ctx context.Context, uuID string) (apiKey *model.APIKey, err error) {
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Getting API key by uuid: %s", uuID)
	ctx, db := GetContextDB(ctx)
	apiKey = &model.APIKey{}
	// The GetWhere() clause enforces org scoping; admins get an empty (all) clause.
	err = db.Where(memberShip.GetWhere()).Where("uuid = ?", uuID).Take(apiKey).Error
	if err != nil {
		// Error handling: not found or not visible to this caller.
		logger.Errorf("Failed to query API key %s: %v", uuID, err)
		err = NewCLError(ErrAPIKeyNotFound, "API key not found", err)
	}
	return
}

// Update enables or disables the API key identified by uuID. The key must be
// visible to the caller (via GetByUUID's scoping). It returns ErrPermissionDenied
// without Writer permission, or ErrAPIKeyUpdateFailed if the DB update fails.
func (a *APIKeyAdmin) Update(ctx context.Context, uuID string, disabled bool) (apiKey *model.APIKey, err error) {
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Updating API key %s, disabled: %t", uuID, disabled)
	// Branch: mutating a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to update API key, user: %d", memberShip.UserID)
		err = NewCLError(ErrPermissionDenied, "Not authorized to update API keys", nil)
		return
	}
	apiKey, err = a.GetByUUID(ctx, uuID)
	if err != nil {
		// GetByUUID already logged the cause.
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
		// Error handling: surface a typed update error.
		logger.Errorf("Failed to update API key %s: %v", uuID, err)
		err = NewCLError(ErrAPIKeyUpdateFailed, "Failed to update API key", err)
		return
	}
	// State change: reflect the new disabled flag on the returned record.
	apiKey.Disabled = disabled
	logger.Debugf("Updated API key %s, disabled: %t", uuID, disabled)
	return
}

// Delete removes the API key identified by uuID. The key must be visible to the
// caller (via GetByUUID's scoping). It returns ErrPermissionDenied without Writer
// permission, or ErrAPIKeyDeleteFailed if the DB delete fails.
func (a *APIKeyAdmin) Delete(ctx context.Context, uuID string) (err error) {
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Deleting API key %s", uuID)
	// Branch: deleting a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to delete API key, user: %d", memberShip.UserID)
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete API keys", nil)
		return
	}
	apiKey, err := a.GetByUUID(ctx, uuID)
	if err != nil {
		// GetByUUID already logged the cause.
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if err = db.Delete(apiKey).Error; err != nil {
		// Error handling: surface a typed delete error.
		logger.Errorf("Failed to delete API key %s: %v", uuID, err)
		err = NewCLError(ErrAPIKeyDeleteFailed, "Failed to delete API key", err)
		return
	}
	logger.Debugf("Deleted API key %s", uuID)
	return
}

// ValidateAPIKey authenticates a full plain-text key. It parses the key, looks
// it up by its UUID portion using a raw DB query (no membership scope, since the
// caller is not yet authenticated), and verifies it is not disabled, not expired,
// and that the secret matches the stored bcrypt hash.
// It returns the matching record, or one of ErrAPIKeyInvalid / ErrAPIKeyNotFound /
// ErrAPIKeyDisabled / ErrAPIKeyExpired describing why validation failed.
func (a *APIKeyAdmin) ValidateAPIKey(fullKey string) (apiKey *model.APIKey, err error) {
	// Note: never log fullKey or the secret — only the non-sensitive uuid below.
	uuidKey, secret, err := ParseAPIKey(fullKey)
	if err != nil {
		// Error handling: malformed key.
		logger.Errorf("Failed to parse API key: %v", err)
		return
	}
	logger.Debugf("Validating API key, uuid: %s", uuidKey)
	db := DB()
	apiKey = &model.APIKey{}
	if err = db.Where("api_key = ?", uuidKey).Take(apiKey).Error; err != nil {
		// Error handling: no key with this uuid.
		logger.Errorf("API key not found, uuid: %s: %v", uuidKey, err)
		err = NewCLError(ErrAPIKeyNotFound, "API key not found", err)
		return
	}
	// Branch: reject disabled keys.
	if apiKey.Disabled {
		logger.Errorf("API key is disabled, uuid: %s", uuidKey)
		err = NewCLError(ErrAPIKeyDisabled, "API key is disabled", nil)
		return
	}
	// Branch: reject expired keys.
	if isAPIKeyExpired(apiKey.ExpiresAt) {
		logger.Errorf("API key has expired, uuid: %s", uuidKey)
		err = NewCLError(ErrAPIKeyExpired, "API key has expired", nil)
		return
	}
	// Branch: the presented secret must match the stored bcrypt hash.
	if err = bcrypt.CompareHashAndPassword([]byte(apiKey.APIKeyHash), []byte(secret)); err != nil {
		logger.Errorf("API key secret mismatch, uuid: %s", uuidKey)
		err = NewCLError(ErrAPIKeyInvalid, "Invalid API key secret", err)
		return
	}
	logger.Debugf("Validated API key, uuid: %s", uuidKey)
	return
}

// List renders the API key management page for the current user/org.
func (v *APIKeyView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	logger.Debugf("Web console listing API keys, user: %d", memberShip.UserID)
	// Branch: viewing the list requires Reader permission.
	if !memberShip.CheckPermission(model.Reader) {
		logger.Errorf("Not authorized to view API keys, user: %d", memberShip.UserID)
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
		// Error handling: render the error page.
		logger.Errorf("Failed to list API keys for console: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["APIKeys"] = keys
	c.Data["Query"] = query
	SetPaginationData(c, "api_keys", total, limit, offset, listConfig,
		`["Name", "ExpiresAt", "Disabled", "CreatedAt", "Owner", "Action"]`,
		[]string{"UUID", "Name", "ExpiresAt", "Disabled", "CreatedAt", "Owner", "Action"})
	c.HTML(200, "api_keys")
}

// New renders the API key creation form.
func (v *APIKeyView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	// Branch: creating a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to open API key form, user: %d", memberShip.UserID)
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "api_keys_new")
}

// Create handles the API key creation form submission and renders the
// one-time plain-key display page on success.
func (v *APIKeyView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	logger.Debugf("Web console creating API key, user: %d", memberShip.UserID)
	// Branch: creating a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to create API key, user: %d", memberShip.UserID)
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	description := c.QueryTrim("description")
	expiresAtStr := c.QueryTrim("expires_at")
	var expiresAt *time.Time
	// Branch: parse the optional expiry only when provided.
	if expiresAtStr != "" {
		t, err := time.Parse(TimeStringForMat, expiresAtStr)
		if err != nil {
			// Error handling: invalid expiry format from the form.
			logger.Errorf("Invalid expires_at %q: %v", expiresAtStr, err)
			c.Data["ErrorMsg"] = "Invalid expires_at format, expected: " + TimeStringForMat
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		expiresAt = &t
	}
	apiKey, plainKey, err := apiKeyAdmin.Create(ctx, name, description, expiresAt)
	if err != nil {
		// Error handling: surface the creation failure.
		logger.Errorf("Failed to create API key from console: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	// PlainKey is shown to the user exactly once here and never persisted in plain text.
	c.Data["APIKey"] = apiKey
	c.Data["PlainKey"] = plainKey
	c.HTML(200, "api_keys_created")
}

// Edit renders the enable/disable form for a single API key.
func (v *APIKeyView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	// Branch: editing a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to edit API key, user: %d", memberShip.UserID)
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	uuID := c.Params("id")
	apiKey, err := apiKeyAdmin.GetByUUID(ctx, uuID)
	if err != nil {
		// Error handling: key not found or not visible.
		logger.Errorf("Failed to load API key %s for edit: %v", uuID, err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["APIKey"] = apiKey
	c.HTML(200, "api_keys_patch")
}

// Patch handles the enable/disable form submission for a single API key.
func (v *APIKeyView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	uuID := c.Params("id")
	logger.Debugf("Web console patching API key %s", uuID)
	// Branch: patching a key requires Writer permission.
	if !memberShip.CheckPermission(model.Writer) {
		logger.Errorf("Not authorized to patch API key, user: %d", memberShip.UserID)
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	disabledStr := c.QueryTrim("disabled")
	disabled, err := strconv.ParseBool(disabledStr)
	if err != nil {
		// Error handling: the disabled field was not a valid bool.
		logger.Errorf("Invalid disabled value %q: %v", disabledStr, err)
		c.Data["ErrorMsg"] = "Invalid disabled value"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err = apiKeyAdmin.Update(ctx, uuID, disabled)
	if err != nil {
		// Error handling: surface the update failure.
		logger.Errorf("Failed to patch API key %s: %v", uuID, err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect("../api_keys")
}

// Delete handles the delete action for a single API key and returns a JSON
// redirect target consumed by the Console front-end.
func (v *APIKeyView) Delete(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	uuID := c.Params("id")
	logger.Debugf("Web console deleting API key %s", uuID)
	// Branch: a UUID is required to identify the key to delete.
	if uuID == "" {
		logger.Error("Delete API key called with empty id")
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	if err := apiKeyAdmin.Delete(ctx, uuID); err != nil {
		// Error handling: surface the delete failure.
		logger.Errorf("Failed to delete API key %s: %v", uuID, err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "api_keys",
	})
}
