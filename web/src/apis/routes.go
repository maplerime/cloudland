/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	_ "web/docs/routes"
	"web/src/utils/log"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

var logger = log.MustGetLogger("apis")

func Run() (err error) {
	logger.Info("Start to run cloudland api service")
	r := Register()
	cert := viper.GetString("rest.cert")
	key := viper.GetString("rest.key")
	listen := viper.GetString("rest.listen")
	logger.Infof("cert: %s, key: %s\n", cert, key)
	if cert != "" && key != "" {
		logger.Infof("Running https service isten on %s\n", listen)
		r.RunTLS(listen, cert, key)
	} else {
		logger.Infof("Running http service on %s\n", listen)
		r.Run(listen)
	}
	return
}

// @title CloudLand API
// @version 1.0
// @description APIs for CloudLand Functions
// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
// @BasePath /api/v1
func Register() (r *gin.Engine) {
	r = gin.Default()

	r.POST("/api/v1/login", userAPI.LoginPost)
	r.GET("/api/v1/version", versionAPI.Get)
	r.POST("/api/v1/alerts/process", alarmAPI.ProcessAlertWebhook)
	r.POST("/api/v1/alerts/resource-adjustment", adjustAPI.ProcessResourceAdjustmentWebhook)
	authGroup := r.Group("").Use(Authorize())
	{
		//authGroup.GET("/api/v1/version", versionAPI.Get)
		authGroup.GET("/api/v1/zones", zoneAPI.List)
		authGroup.POST("/api/v1/zones", zoneAPI.Create)
		authGroup.GET("/api/v1/zones/:name", zoneAPI.Get)
		authGroup.DELETE("/api/v1/zones/:name", zoneAPI.Delete)
		authGroup.PATCH("/api/v1/zones/:name", zoneAPI.Patch)

		authGroup.GET("/api/v1/hypers", hyperAPI.List)
		authGroup.GET("/api/v1/hypers/:hostid", hyperAPI.Get)
		authGroup.PATCH("/api/v1/hypers/:hostid", hyperAPI.Patch)

		authGroup.GET("/api/v1/migrations", migrationAPI.List)
		authGroup.POST("/api/v1/migrations", migrationAPI.Create)
		authGroup.GET("/api/v1/migrations/:id", migrationAPI.Get)

		authGroup.GET("/api/v1/users", userAPI.List)
		authGroup.POST("/api/v1/users", userAPI.Create)
		authGroup.GET("/api/v1/users/:id", userAPI.Get)
		authGroup.DELETE("/api/v1/users/:id", userAPI.Delete)
		authGroup.PATCH("/api/v1/users/:id", userAPI.Patch)

		authGroup.GET("/api/v1/orgs", orgAPI.List)
		authGroup.POST("/api/v1/orgs", orgAPI.Create)
		authGroup.GET("/api/v1/orgs/:id", orgAPI.Get)
		authGroup.DELETE("/api/v1/orgs/:id", orgAPI.Delete)
		authGroup.PATCH("/api/v1/orgs/:id", orgAPI.Patch)

		authGroup.GET("/api/v1/vpcs", vpcAPI.List)
		authGroup.POST("/api/v1/vpcs", vpcAPI.Create)
		authGroup.GET("/api/v1/vpcs/:id", vpcAPI.Get)
		authGroup.DELETE("/api/v1/vpcs/:id", vpcAPI.Delete)
		authGroup.PATCH("/api/v1/vpcs/:id", vpcAPI.Patch)

		authGroup.GET("/api/v1/dictionaries", dictionaryAPI.List)
		authGroup.POST("/api/v1/dictionaries", dictionaryAPI.Create)
		authGroup.GET("/api/v1/dictionaries/:id", dictionaryAPI.Get)
		authGroup.DELETE("/api/v1/dictionaries/:id", dictionaryAPI.Delete)
		authGroup.PATCH("/api/v1/dictionaries/:id", dictionaryAPI.Patch)

		authGroup.GET("/api/v1/ip_groups", ipGroupAPI.List)
		authGroup.POST("/api/v1/ip_groups", ipGroupAPI.Create)
		authGroup.GET("/api/v1/ip_groups/:id", ipGroupAPI.Get)
		authGroup.DELETE("/api/v1/ip_groups/:id", ipGroupAPI.Delete)
		authGroup.PATCH("/api/v1/ip_groups/:id", ipGroupAPI.Patch)

		authGroup.GET("/api/v1/subnets", subnetAPI.List)
		authGroup.POST("/api/v1/subnets", subnetAPI.Create)
		authGroup.GET("/api/v1/subnets/:id", subnetAPI.Get)
		authGroup.DELETE("/api/v1/subnets/:id", subnetAPI.Delete)
		authGroup.PATCH("/api/v1/subnets/:id", subnetAPI.Patch)

		authGroup.GET("/api/v1/security_groups", secgroupAPI.List)
		authGroup.POST("/api/v1/security_groups", secgroupAPI.Create)
		authGroup.GET("/api/v1/security_groups/:id", secgroupAPI.Get)
		authGroup.DELETE("/api/v1/security_groups/:id", secgroupAPI.Delete)
		authGroup.PATCH("/api/v1/security_groups/:id", secgroupAPI.Patch)

		authGroup.GET("/api/v1/security_groups/:id/rules", secruleAPI.List)
		authGroup.POST("/api/v1/security_groups/:id/rules", secruleAPI.Create)
		authGroup.GET("/api/v1/security_groups/:id/rules/:rule_id", secruleAPI.Get)
		authGroup.DELETE("/api/v1/security_groups/:id/rules/:rule_id", secruleAPI.Delete)

		authGroup.GET("/api/v1/load_balancers", loadBalancerAPI.List)
		authGroup.POST("/api/v1/load_balancers", loadBalancerAPI.Create)
		authGroup.GET("/api/v1/load_balancers/:id", loadBalancerAPI.Get)
		authGroup.DELETE("/api/v1/load_balancers/:id", loadBalancerAPI.Delete)
		authGroup.PATCH("/api/v1/load_balancers/:id", loadBalancerAPI.Patch)

		authGroup.GET("/api/v1/load_balancers/:id/floating_ips", lbFloatingIpAPI.List)
		authGroup.POST("/api/v1/load_balancers/:id/floating_ips", lbFloatingIpAPI.Create)
		authGroup.GET("/api/v1/load_balancers/:id/floating_ips/:floating_ip_id", lbFloatingIpAPI.Get)
		authGroup.DELETE("/api/v1/load_balancers/:id/floating_ips/:floating_ip_id", lbFloatingIpAPI.Delete)

		authGroup.GET("/api/v1/load_balancers/:id/listeners", listenerAPI.List)
		authGroup.POST("/api/v1/load_balancers/:id/listeners", listenerAPI.Create)
		authGroup.GET("/api/v1/load_balancers/:id/listeners/:listener_id", listenerAPI.Get)
		authGroup.DELETE("/api/v1/load_balancers/:id/listeners/:listener_id", listenerAPI.Delete)

		authGroup.GET("/api/v1/load_balancers/:id/listeners/:listener_id/backends", backendAPI.List)
		authGroup.POST("/api/v1/load_balancers/:id/listeners/:listener_id/backends", backendAPI.Create)
		authGroup.GET("/api/v1/load_balancers/:id/listeners/:listener_id/backends/:backend_id", backendAPI.Get)
		authGroup.DELETE("/api/v1/load_balancers/:id/listeners/:listener_id/backends/:backend_id", backendAPI.Delete)

		authGroup.GET("/api/v1/floating_ips", floatingIpAPI.List)
		authGroup.POST("/api/v1/floating_ips", floatingIpAPI.Create)
		authGroup.GET("/api/v1/floating_ips/:id", floatingIpAPI.Get)
		authGroup.DELETE("/api/v1/floating_ips/:id", floatingIpAPI.Delete)
		authGroup.PATCH("/api/v1/floating_ips/:id", floatingIpAPI.Patch)
		authGroup.POST("/api/v1/floating_ips/site_attach", floatingIpAPI.SiteAttach)
		authGroup.POST("/api/v1/floating_ips/site_detach", floatingIpAPI.SiteDetach)

		// Addresses
		authGroup.PATCH("/api/v1/addresses/remark", addressAPI.Remark)
		authGroup.PATCH("/api/v1/addresses/update-lock", addressAPI.UpdateLock)
		authGroup.GET("/api/v1/addresses/:uuid", addressAPI.ListIpBySubnetUUID)

		authGroup.GET("/api/v1/keys", keyAPI.List)
		authGroup.POST("/api/v1/keys", keyAPI.Create)
		authGroup.GET("/api/v1/keys/:id", keyAPI.Get)
		authGroup.DELETE("/api/v1/keys/:id", keyAPI.Delete)
		authGroup.PATCH("/api/v1/keys/:id", keyAPI.Patch)

		authGroup.GET("/api/v1/flavors", flavorAPI.List)
		authGroup.POST("/api/v1/flavors", flavorAPI.Create)
		authGroup.GET("/api/v1/flavors/:name", flavorAPI.Get)
		authGroup.DELETE("/api/v1/flavors/:name", flavorAPI.Delete)

		authGroup.GET("/api/v1/images", imageAPI.List)
		authGroup.POST("/api/v1/images", imageAPI.Create)
		authGroup.GET("/api/v1/images/:id", imageAPI.Get)
		authGroup.DELETE("/api/v1/images/:id", imageAPI.Delete)
		authGroup.PATCH("/api/v1/images/:id", imageAPI.Patch)
		authGroup.GET("/api/v1/images/:id/storages", imageAPI.ListStorages)

		authGroup.GET("/api/v1/volumes", volumeAPI.List)
		authGroup.POST("/api/v1/volumes", volumeAPI.Create)
		authGroup.GET("/api/v1/volumes/:id", volumeAPI.Get)
		authGroup.DELETE("/api/v1/volumes/:id", volumeAPI.Delete)
		authGroup.PATCH("/api/v1/volumes/:id", volumeAPI.Patch)
		authGroup.POST("/api/v1/volumes/:id/resize", volumeAPI.Resize)
		authGroup.PUT("/api/v1/volumes/:id/qos", volumeAPI.UpdateQos)

		authGroup.GET("/api/v1/backups", volBackupAPI.List)
		authGroup.POST("/api/v1/backups", volBackupAPI.Create)
		authGroup.GET("/api/v1/backups/:id", volBackupAPI.Get)
		authGroup.DELETE("/api/v1/backups/:id", volBackupAPI.Delete)
		authGroup.POST("/api/v1/backups/:id/restore", volBackupAPI.Restore)

		authGroup.GET("/api/v1/instances", instanceAPI.List)
		authGroup.POST("/api/v1/instances", instanceAPI.Create)
		authGroup.GET("/api/v1/instances/:id", instanceAPI.Get)
		authGroup.DELETE("/api/v1/instances/:id", instanceAPI.Delete)
		authGroup.PATCH("/api/v1/instances/:id", instanceAPI.Patch)
		authGroup.GET("/api/v1/instances/rules", instanceAPI.GetInstanceRuleLinks)

		authGroup.POST("/api/v1/instances/:id/set_user_password", instanceAPI.SetUserPassword)
		authGroup.POST("/api/v1/instances/:id/console", consoleAPI.Create)
		authGroup.POST("/api/v1/instances/:id/reinstall", instanceAPI.Reinstall)
		authGroup.POST("/api/v1/instances/:id/resize", instanceAPI.Resize)
		authGroup.POST("/api/v1/instances/:id/rescue", instanceAPI.Rescue)
		authGroup.POST("/api/v1/instances/:id/end_rescue", instanceAPI.EndRescue)

		authGroup.GET("/api/v1/instances/:id/interfaces", interfaceAPI.List)
		authGroup.POST("/api/v1/instances/:id/interfaces", interfaceAPI.Create)
		authGroup.GET("/api/v1/instances/:id/interfaces/:interface_id", interfaceAPI.Get)
		authGroup.DELETE("/api/v1/instances/:id/interfaces/:interface_id", interfaceAPI.Delete)
		authGroup.PATCH("/api/v1/instances/:id/interfaces/:interface_id", interfaceAPI.Patch)

		authGroup.GET("/api/v1/tasks", taskAPI.List)
		authGroup.GET("/api/v1/tasks/:id", taskAPI.Get)

		metricsGroup := authGroup.(*gin.RouterGroup).Group("/api/v1/metrics")
		{
			metricsGroup.POST("/instances/cpu/his_data", monitorAPI.GetCPU)
			metricsGroup.POST("/instances/disk/his_data", monitorAPI.GetDisk)
			metricsGroup.POST("/instances/memory/his_data", monitorAPI.GetMemory)
			metricsGroup.POST("/instances/network/his_data", monitorAPI.GetNetwork)
			metricsGroup.POST("/instances/traffic/his_data", monitorAPI.GetTraffic)
			metricsGroup.POST("/instances/volume/his_data", monitorAPI.GetVolume)

			metricsGroup.POST("/alarm/cpu/rules", alarmAPI.CreateCPURule)
			metricsGroup.GET("/alarm/cpu/rules", alarmAPI.GetCPURules)
			metricsGroup.GET("/alarm/active-rules", alarmAPI.GetActiveRules)
			metricsGroup.GET("/alarm/cpu/rule/:uuid", alarmAPI.GetCPURules)
			metricsGroup.DELETE("/alarm/cpu/rule/:uuid", alarmAPI.DeleteCPURule)

			metricsGroup.POST("/alarm/memory/rules", alarmAPI.CreateMemoryRule)
			metricsGroup.GET("/alarm/memory/rules", alarmAPI.GetMemoryRules)
			metricsGroup.GET("/alarm/memory/rule/:uuid", alarmAPI.GetMemoryRules)
			metricsGroup.DELETE("/alarm/memory/rule/:uuid", alarmAPI.DeleteMemoryRule)

			metricsGroup.POST("/alarm/bw/rules", alarmAPI.CreateBWRule)
			metricsGroup.GET("/alarm/bw/rules", alarmAPI.GetBWRules)
			metricsGroup.GET("/alarm/bw/rule/:uuid", alarmAPI.GetBWRules)
			metricsGroup.DELETE("/alarm/bw/rule/:uuid", alarmAPI.DeleteBWRules)

			// Add new endpoint for synchronizing VM rule mappings
			metricsGroup.POST("/alarm/sync-mappings", alarmAPI.SyncAllVMRuleMappings)

			metricsGroup.GET("/api/v1/current-alarms", alarmAPI.GetCurrentAlarms)
			metricsGroup.GET("/api/v1/history-alarms", alarmAPI.GetHistoryAlarm)
			metricsGroup.POST("/api/v1/alarm/:id/enable", alarmAPI.ToggleRuleStatus("alarm", "enable"))
			metricsGroup.POST("/api/v1/alarm/:id/disable", alarmAPI.ToggleRuleStatus("alarm", "disable"))
			metricsGroup.POST("/api/v1/alarm/link", alarmAPI.LinkRuleToVMWithType("alarm"))
			metricsGroup.POST("/api/v1/alarm/unlink", alarmAPI.UnlinkRuleFromVMWithType("alarm"))

			authGroup.POST("/api/v1/node-alarm-rules", alarmAPI.CreateNodeAlarmRule)
			authGroup.GET("/api/v1/node-alarm-rules", alarmAPI.GetNodeAlarmRules)
			authGroup.DELETE("/api/v1/node-alarm-rules/:uuid", alarmAPI.DeleteNodeAlarmRule)

			// OpenMeter API routes
			authGroup.GET("/api/v1/openmeter/metrics", openMeterAPI.QueryOpenMeterMetrics)
			authGroup.GET("/api/v1/openmeter/metrics/:instance_id/:subject", openMeterAPI.QueryInstanceMetricsBySubject)
			authGroup.GET("/api/v1/openmeter/subjects", openMeterAPI.GetAvailableSubjects)

			// Resource auto adjustment route
			metricsGroup.POST("/adjust/cpu/rules", adjustAPI.CreateCPUAdjustRule)
			metricsGroup.GET("/adjust/cpu/rules", adjustAPI.GetCPUAdjustRules)
			metricsGroup.GET("/adjust/cpu/rule/:uuid", adjustAPI.GetCPUAdjustRules)
			metricsGroup.PATCH("/adjust/cpu/rule/:uuid", adjustAPI.PatchCPUAdjustRule)
			metricsGroup.DELETE("/adjust/cpu/rule/:uuid", adjustAPI.DeleteCPUAdjustRule)

			// Bandwidth auto adjustment route
			metricsGroup.POST("/adjust/bw/rules", adjustAPI.CreateBWAdjustRule)
			metricsGroup.GET("/adjust/bw/rules", adjustAPI.GetBWAdjustRules)
			metricsGroup.GET("/adjust/bw/rule/:uuid", adjustAPI.GetBWAdjustRules)
			metricsGroup.PATCH("/adjust/bw/rule/:uuid", adjustAPI.PatchBWAdjustRule)
			metricsGroup.DELETE("/adjust/bw/rule/:uuid", adjustAPI.DeleteBWAdjustRule)

			// Enable/disable resource adjustment rules
			metricsGroup.POST("/adjust/:uuid/enable", alarmAPI.ToggleRuleStatus("adjust", "enable"))
			metricsGroup.POST("adjust/:uuid/disable", alarmAPI.ToggleRuleStatus("adjust", "disable"))

			// VM adjust rule link management
			metricsGroup.POST("/adjust/link", alarmAPI.LinkRuleToVMWithType("adjust"))
			metricsGroup.DELETE("/adjust/unlink", alarmAPI.UnlinkRuleFromVMWithType("adjust"))

			// Unified rule links query (supports both alarm and adjust rules)
			metricsGroup.GET("/api/v1/rules/links", adjustAPI.GetRuleLinks)

			// Batch get rules (supports both alarm and adjust rules)
			metricsGroup.POST("/rules/batch", alarmAPI.BatchGetRules)

			// Bandwidth configuration metrics regeneration
			metricsGroup.POST("/api/v1/adjust/regenerate-bandwidth-metrics", adjustAPI.RegenerateBandwidthConfigMetrics)
		}

	}

	r.GET("/swagger/api/v1/*any", ginSwagger.WrapHandler(swaggerFiles.NewHandler(), ginSwagger.InstanceName("v1")))
	return
}
