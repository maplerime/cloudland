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

	"web/src/routes"
	"web/src/rpcs"
	"web/src/scheduler"
	rlog "web/src/utils/log"

	// Register scheduler filters and weighers via init()
	_ "web/src/scheduler/filters"
	_ "web/src/scheduler/weighers"

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
	// Initialize placement scheduler config
	if err := scheduler.InitPlacementConfig("conf/placement.toml"); err != nil {
		rlog.MustGetLogger("main").Errorf("Failed to init placement config: %v", err)
	}
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
