/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package callback

import (
	"strings"
	"sync"
	"testing"

	"github.com/spf13/viper"
)

func resetCallbackConfigTestState() {
	viper.Reset()
	regionCache = ""
	regionOnce = sync.Once{}
}

func TestValidateConfig_Disabled_AllowsMissingFields(t *testing.T) {
	resetCallbackConfigTestState()
	t.Cleanup(resetCallbackConfigTestState)

	viper.Set("callback.enabled", false)

	if err := ValidateConfig(); err != nil {
		t.Fatalf("expected nil error when callback is disabled, got: %v", err)
	}
}

func TestValidateConfig_EnabledRequiresURL(t *testing.T) {
	resetCallbackConfigTestState()
	t.Cleanup(resetCallbackConfigTestState)

	viper.Set("callback.enabled", true)
	viper.Set("callback.api_key", "k")
	viper.Set("callback.region", "r1")

	err := ValidateConfig()
	if err == nil {
		t.Fatal("expected error when callback.url is missing")
	}
	if !strings.Contains(err.Error(), "callback.url") {
		t.Fatalf("expected callback.url error, got: %v", err)
	}
}

func TestValidateConfig_EnabledRequiresAPIKey(t *testing.T) {
	resetCallbackConfigTestState()
	t.Cleanup(resetCallbackConfigTestState)

	viper.Set("callback.enabled", true)
	viper.Set("callback.url", "https://example.com/callback")
	viper.Set("callback.region", "r1")

	err := ValidateConfig()
	if err == nil {
		t.Fatal("expected error when callback.api_key is missing")
	}
	if !strings.Contains(err.Error(), "callback.api_key") {
		t.Fatalf("expected callback.api_key error, got: %v", err)
	}
}

func TestValidateConfig_EnabledRequiresRegion(t *testing.T) {
	resetCallbackConfigTestState()
	t.Cleanup(resetCallbackConfigTestState)

	viper.Set("callback.enabled", true)
	viper.Set("callback.url", "https://example.com/callback")
	viper.Set("callback.api_key", "k")

	err := ValidateConfig()
	if err == nil {
		t.Fatal("expected error when callback.region is missing")
	}
	if !strings.Contains(err.Error(), "callback.region") {
		t.Fatalf("expected callback.region error, got: %v", err)
	}
}

func TestValidateConfig_EnabledWithAllRequiredFields(t *testing.T) {
	resetCallbackConfigTestState()
	t.Cleanup(resetCallbackConfigTestState)

	viper.Set("callback.enabled", true)
	viper.Set("callback.url", "https://example.com/callback")
	viper.Set("callback.api_key", "k")
	viper.Set("callback.region", "r1")

	if err := ValidateConfig(); err != nil {
		t.Fatalf("expected nil error with full config, got: %v", err)
	}
}
