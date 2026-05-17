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
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
)

var apiKeyAdmin = &APIKeyAdmin{}

type APIKeyAdmin struct{}

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
