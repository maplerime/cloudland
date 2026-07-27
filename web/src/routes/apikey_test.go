/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestGenerateAPIKey(t *testing.T) {
	fullKey, uuidKey, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey error: %v", err)
	}
	if !strings.HasPrefix(fullKey, "cl_") {
		t.Fatalf("expected key to start with 'cl_', got: %s", fullKey)
	}
	if uuidKey == "" {
		t.Fatal("uuidKey is empty")
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
	parsedUUID, parsedSecret, err := ParseAPIKey(fullKey)
	if err != nil {
		t.Fatalf("ParseAPIKey error: %v", err)
	}
	if parsedUUID != uuidKey {
		t.Fatalf("uuid mismatch: expected %s, got %s", uuidKey, parsedUUID)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(parsedSecret)); err != nil {
		t.Fatalf("hash mismatch: %v", err)
	}
}

func TestParseAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "valid key",
			key:     "cl_550e8400-e29b-41d4-a716-446655440000_a3f8b2d1e4c7f09a5b6e3d2c1f8a4b7e0011223344556677889900aabbccddeeff",
			wantErr: false,
		},
		{
			name:    "missing prefix",
			key:     "550e8400-e29b-41d4-a716-446655440000_a3f8b2d1",
			wantErr: true,
		},
		{
			name:    "no underscore separator",
			key:     "cl_nounderscore",
			wantErr: true,
		},
		{
			name:    "bad separator at position 36",
			key:     "cl_550e8400-e29b-41d4-a716-446655440000Xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantErr: true,
		},
		{
			name:    "empty",
			key:     "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uuidKey, secret, err := ParseAPIKey(tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got uuidKey=%s secret=%s", uuidKey, secret)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if uuidKey == "" || secret == "" {
					t.Fatal("uuidKey or secret is empty")
				}
			}
		})
	}
}

func TestAPIKeyIsExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	if !isAPIKeyExpired(&past) {
		t.Fatal("expected past time to be expired")
	}
	if isAPIKeyExpired(&future) {
		t.Fatal("expected future time to not be expired")
	}
	if isAPIKeyExpired(nil) {
		t.Fatal("nil expiry should never expire")
	}
}
