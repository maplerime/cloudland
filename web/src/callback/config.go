/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"github.com/spf13/viper"
	"web/src/utils/log"
)

var logger = log.MustGetLogger("callback")

// IsEnabled 检查 callback 功能是否启用
func IsEnabled() bool {
	return viper.GetBool("callback.enabled")
}

// GetCallbackURL 获取 callback URL
func GetCallbackURL() string {
	return viper.GetString("callback.url")
}

// GetWorkerCount 获取 worker 数量
func GetWorkerCount() int {
	count := viper.GetInt("callback.workers")
	if count <= 0 {
		count = 3 // 默认 3 个 worker
	}
	return count
}

// GetQueueSize 获取队列大小
func GetQueueSize() int {
	size := viper.GetInt("callback.queue_size")
	if size <= 0 {
		size = 10000 // 默认 10000
	}
	return size
}

// GetTimeout 获取 HTTP 请求超时时间 (秒)
func GetTimeout() int {
	timeout := viper.GetInt("callback.timeout")
	if timeout <= 0 {
		timeout = 30 // 默认 30 秒
	}
	return timeout
}

// GetRetryMax 获取最大重试次数
func GetRetryMax() int {
	retry := viper.GetInt("callback.retry_max")
	if retry < 0 {
		retry = 3 // 默认 3 次
	}
	return retry
}

// GetRetryInterval 获取重试间隔 (秒)
func GetRetryInterval() int {
	interval := viper.GetInt("callback.retry_interval")
	if interval <= 0 {
		interval = 5 // 默认 5 秒
	}
	return interval
}
