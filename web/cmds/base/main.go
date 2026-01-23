/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/viper"

	"web/src/callback"
	"web/src/routes"
	"web/src/rpcs"
	rlog "web/src/utils/log"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var (
	Version = "0.0.1"
)

var (
	clientCmd = &cobra.Command{
		Use: "clbase",
	}
)

func RunDaemon(cmd *cobra.Command, args []string) (err error) {
	// 初始化并启动 callback 功能
	if callback.IsEnabled() {
		// 初始化事件队列
		queueSize := callback.GetQueueSize()
		callback.InitQueue(queueSize)

		// 启动 callback worker
		workerCount := callback.GetWorkerCount()
		ctx := context.Background()
		callback.StartWorkers(ctx, workerCount)
	}

	// 启动 Web UI 和 RPC 服务
	g, _ := errgroup.WithContext(context.Background())
	g.Go(routes.Run)
	g.Go(rpcs.Run)
	return g.Wait()
}

func RootCmd() (cmd *cobra.Command) {
	for _, arg := range os.Args {
		if arg == "--daemon" {
			daemonCmd := &cobra.Command{
				Use:  "CloudlandBase",
				RunE: RunDaemon,
			}
			daemonCmd.Flags().Bool("daemon", false, "daemon")
			return daemonCmd
		}
	}
	return clientCmd
}
func init() {
	viper.Set("AppVersion", Version)
	viper.Set("GoVersion", strings.Title(runtime.Version()))
	viper.SetConfigFile("conf/config.toml")
	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Failed to load configuration file %+v", err)
		os.Exit(1)
	}
	rlog.InitLogger("clbase.log")
	/*
		file := "/opt/cloudland/log/clbase_org.log"
		logFile, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
		if err != nil {
			panic(err)
		}
		log.SetOutput(logFile)
		log.SetPrefix("[webUILog]")
		log.SetFlags(log.LstdFlags | log.Lshortfile | log.LUTC)
	*/
}

func main() {
	rootCmd := RootCmd()
	if err := rootCmd.Execute(); err != nil {
		rootCmd.Println(err)
	}
}
