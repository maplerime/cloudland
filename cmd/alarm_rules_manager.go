package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RuleFileRequest 定义了规则文件操作请求
type RuleFileRequest struct {
	Operation string `json:"operation"` // 操作类型: write, symlink, chown, reload, delete
	FileUser  string `json:"file_user"` // 文件所有者用户名
	Content   string `json:"content"`   // 规则文件内容
	FilePath  string `json:"file_path"` // 文件路径
	LinkPath  string `json:"link_path"` // 链接路径(用于symlink)
}

// AlarmRulesManager handles Prometheus related API requests
type AlarmRulesManager struct {
	Port int // HTTP server port
}

// NewAlarmRulesManager creates a new Prometheus server handler
func NewAlarmRulesManager(port int) *AlarmRulesManager {
	return &AlarmRulesManager{
		Port: port,
	}
}

// RegisterRoutes registers Prometheus related API routes
func (s *AlarmRulesManager) RegisterRoutes(router *gin.Engine) {
	api := router.Group("/api/v1/rules")
	{
		api.POST("/file", s.handleRuleFile)
		api.POST("/symlink", s.handleRuleFile)
		api.POST("/chown", s.handleRuleFile)
		api.POST("/reload", s.handleRuleFile)
		api.POST("/delete", s.handleRuleFile)
	}
}

// handleRuleFile handles rule file operation requests
func (s *AlarmRulesManager) handleRuleFile(c *gin.Context) {
	var req RuleFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	var err error
	switch req.Operation {
	case "write":
		err = s.handleWriteFile(req)
	case "symlink":
		err = s.handleCreateSymlink(req)
	case "chown":
		err = s.handleChownFile(req)
	case "reload":
		err = s.handleReloadPrometheus()
	case "delete":
		err = s.handleDeleteFile(req)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("Unsupported operation type: %s", req.Operation),
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("%s operation successful", req.Operation),
		"path":    req.FilePath,
	})
}

// handleWriteFile handles write file requests
func (s *AlarmRulesManager) handleWriteFile(req RuleFileRequest) error {
	// Ensure directory exists
	dir := filepath.Dir(req.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("Failed to create directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(req.FilePath, []byte(req.Content), 0644); err != nil {
		return fmt.Errorf("Failed to write file: %w", err)
	}

	// Change file ownership if specified
	if req.FileUser != "" {
		if err := s.changeOwner(req.FilePath, req.FileUser); err != nil {
			return err
		}
	}

	return nil
}

// handleCreateSymlink handles symlink creation requests
func (s *AlarmRulesManager) handleCreateSymlink(req RuleFileRequest) error {
	// Ensure target directory exists
	targetDir := filepath.Dir(req.LinkPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("Failed to create target directory: %w", err)
	}

	// Remove existing link if it exists
	if _, err := os.Lstat(req.LinkPath); err == nil {
		if err := os.Remove(req.LinkPath); err != nil {
			return fmt.Errorf("Failed to remove existing link: %w", err)
		}
	}

	// Create symlink
	if err := os.Symlink(req.FilePath, req.LinkPath); err != nil {
		return fmt.Errorf("Failed to create symlink: %w", err)
	}

	// Change link ownership if specified
	if req.FileUser != "" {
		if err := s.changeOwner(req.LinkPath, req.FileUser); err != nil {
			return err
		}
	}

	return nil
}

// handleChownFile handles file ownership change requests
func (s *AlarmRulesManager) handleChownFile(req RuleFileRequest) error {
	if req.FileUser == "" {
		return fmt.Errorf("File owner not specified")
	}

	return s.changeOwner(req.FilePath, req.FileUser)
}

// handleDeleteFile handles file deletion requests
func (s *AlarmRulesManager) handleDeleteFile(req RuleFileRequest) error {
	if _, err := os.Stat(req.FilePath); os.IsNotExist(err) {
		// File doesn't exist, consider deletion successful
		return nil
	}

	if err := os.Remove(req.FilePath); err != nil {
		return fmt.Errorf("Failed to delete file: %w", err)
	}

	return nil
}

// handleReloadPrometheus handles Prometheus configuration reload requests
func (s *AlarmRulesManager) handleReloadPrometheus() error {
	cmd := exec.Command("sudo", "systemctl", "kill", "-s", "SIGHUP", "prometheus.service")
	output, err := cmd.CombinedOutput()

	// Ignore process not found errors
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		// Prometheus process not found, but not considered an error
		return nil
	}

	if err != nil {
		return fmt.Errorf("Failed to reload Prometheus configuration: %v, output: %s", err, string(output))
	}

	return nil
}

// changeOwner changes the owner of a file or link
func (s *AlarmRulesManager) changeOwner(path, username string) error {
	// Look up user
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("Failed to look up user: %w", err)
	}

	// Convert uid and gid to integers
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("Failed to convert uid: %w", err)
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("Failed to convert gid: %w", err)
	}

	// Change ownership
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("Failed to change ownership: %w", err)
	}

	return nil
}

// Main function to run the server independently
func main() {
	// 设置默认值
	port := 8256 // 默认端口

	// 解析命令行参数
	if len(os.Args) > 1 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil {
			port = p
		}
	}

	// 创建 manager
	manager := NewAlarmRulesManager(port)

	// 创建路由
	router := gin.Default()

	// 注册路由
	manager.RegisterRoutes(router)

	// 启动服务器
	portStr := fmt.Sprintf(":%d", manager.Port)
	fmt.Printf("Starting alarm rules manager on port %s\n", portStr)
	if err := router.Run(portStr); err != nil {
		fmt.Printf("Failed to start server: %v\n", err)
	}
}