/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"fmt"
	"strings"
	"sync"
	"web/src/utils/log"

	"github.com/spf13/viper"
)

var logger = log.MustGetLogger("callback")

// Cache region only.
var (
	regionCache string
	regionOnce  sync.Once
)

// IsEnabled checks whether callback is enabled.
func IsEnabled() bool {
	return viper.GetBool("callback.enabled")
}

// GetCallbackURL gets callback URL.
func GetCallbackURL() string {
	return viper.GetString("callback.url")
}

func GetAPIKey() string {
	return viper.GetString("callback.api_key")
}

// ValidateConfig validates callback config when callback is enabled.
func ValidateConfig() error {
	if !IsEnabled() {
		return nil
	}

	url := strings.TrimSpace(GetCallbackURL())
	if url == "" {
		return fmt.Errorf("callback enabled but callback.url is empty")
	}

	apiKey := strings.TrimSpace(GetAPIKey())
	if apiKey == "" {
		return fmt.Errorf("callback enabled but callback.api_key is empty")
	}

	region := strings.TrimSpace(viper.GetString("callback.region"))
	if region == "" {
		return fmt.Errorf("callback enabled but callback.region is empty")
	}

	return nil
}

// GetRegion gets event-source region with cache.
func GetRegion() string {
	regionOnce.Do(func() {
		region := viper.GetString("callback.region")
		if region == "" {
			region = "_" // default region
			logger.Warning("callback.region not configured, using default region '_'")
			regionCache = region
			return
		}
		regionCache = strings.TrimSpace(region)
		logger.Debugf("Using configured region: %s", regionCache)
	})

	return regionCache
}

// GetWorkerCount gets worker count.
func GetWorkerCount() int {
	count := viper.GetInt("callback.workers")
	if count <= 0 {
		count = 3 // default: 3 workers
	}
	return count
}

// GetQueueSize gets queue size.
func GetQueueSize() int {
	size := viper.GetInt("callback.queue_size")
	if size <= 0 {
		size = 10000 // default: 10000
	}
	return size
}

// GetTimeout gets HTTP request timeout (seconds).
func GetTimeout() int {
	timeout := viper.GetInt("callback.timeout")
	if timeout <= 0 {
		timeout = 30 // default: 30 seconds
	}
	return timeout
}

// GetRetryMax gets max retry count.
func GetRetryMax() int {
	retry := viper.GetInt("callback.retry_max")
	if retry < 0 {
		retry = 3 // default: 3 retries
	}
	return retry
}

// GetRetryInterval gets retry interval (seconds).
func GetRetryInterval() int {
	interval := viper.GetInt("callback.retry_interval")
	if interval <= 0 {
		interval = 5 // default: 5 seconds
	}
	return interval
}
