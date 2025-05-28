package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type RuleFileRequest struct {
	Operation string `json:"operation"`
	FileUser  string `json:"file_user"`
	Content   string `json:"content"`
	FilePath  string `json:"file_path"`
	LinkPath  string `json:"link_path"`
}

type AlarmRulesManager struct {
	Port     int
	CertFile string
	KeyFile  string
}

// NewAlarmRulesManager creates a new Prometheus server handler
func NewAlarmRulesManager(port int, certFile, keyFile string) *AlarmRulesManager {
	return &AlarmRulesManager{
		Port:     port,
		CertFile: certFile,
		KeyFile:  keyFile,
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
	fmt.Printf("request received: %s %s\n", c.Request.Method, c.Request.URL.Path)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}
	fmt.Printf("processing: %s, path: %s\n", req.Operation, req.FilePath)
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
	case "read":
		var content []byte
		content, err = s.handleOpenFile(req.FilePath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Read operation successful",
			"path":    req.FilePath,
			"content": string(content),
		})
		return
	case "check":
		exists, err := s.handleCheckFile(req)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"exists":  false,
				"message": err.Error(),
			})
			return
		}
		if !exists {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"exists":  false,
				"message": "File does not exist",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"exists":  true,
			"message": "File exists",
		})
		return
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

func (s *AlarmRulesManager) handleOpenFile(filePath string) ([]byte, error) {
	// Check if file exists
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("File does not exist: %s", filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to stat file: %w", err)
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read file: %w", err)
	}

	return content, nil
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

func (s *AlarmRulesManager) handleDeleteFile(req RuleFileRequest) error {
	// Handle wildcard pattern
	if strings.ContainsAny(req.FilePath, "*?[") {
		matches, err := filepath.Glob(req.FilePath)
		if err != nil {
			return fmt.Errorf("failed to expand wildcard pattern: %w", err)
		}
		for _, match := range matches {
			if err := os.Remove(match); err != nil {
				fmt.Printf("[handleDeleteFile] failed to delete %s: %v\n", match, err)
			} else {
				fmt.Printf("[handleDeleteFile] deleted: %s\n", match)
			}
		}
		return nil
	}

	// Handle direct path
	info, err := os.Lstat(req.FilePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		fmt.Printf("[handleDeleteFile] path: %s. is a link\n", req.FilePath)
	} else {
		fmt.Printf("[handleDeleteFile] path: %s. is a file\n", req.FilePath)
	}

	if err := os.Remove(req.FilePath); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
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

func (s *AlarmRulesManager) handleCheckFile(req RuleFileRequest) (bool, error) {
	info, err := os.Lstat(req.FilePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat file: %w", err)
	}

	// Symlink is considered existing
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}

	// For regular file, check if real file exists
	_, err = os.Stat(req.FilePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat file target: %w", err)
	}

	return true, nil
}

// Main function to run the server independently
func main() {
	port := 8256
	listenHost := "0.0.0.0"

	if listenEnv := os.Getenv("ALARM_RULES_LISTEN"); listenEnv != "" {
		parts := strings.Split(listenEnv, ":")
		if len(parts) == 2 {
			listenHost = parts[0]
			if p, err := strconv.Atoi(parts[1]); err == nil {
				port = p
			}
		}
	}
	certFile := ""
	if certEnv := os.Getenv("ALARM_RULES_CERT"); certEnv != "" {
		certFile = certEnv
	} else {
		fmt.Printf("start server failed with invalid certFile")
		os.Exit(1)
	}
	keyFile := ""
	if keyEnv := os.Getenv("ALARM_RULES_KEY"); keyEnv != "" {
		keyFile = keyEnv
	} else {
		fmt.Printf("start server failed with invalid keyFile")
		os.Exit(1)

	}

	server := NewAlarmRulesManager(port, certFile, keyFile)
	router := gin.Default()
	router.SetTrustedProxies(nil)
	server.RegisterRoutes(router)

	listenAddr := fmt.Sprintf("%s:%d", listenHost, port)
	fmt.Printf("Starting alarm rules manager on %s with TLS\n", listenAddr)

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		fmt.Printf("warning: certFile %s not exist\n", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		fmt.Printf("warning: keyFile %s not exist\n", keyFile)
	}

	if err := router.RunTLS(listenAddr, certFile, keyFile); err != nil {
		fmt.Printf("start TLS server failed: %v\n", err)
		fmt.Println("try start with no-TLS mode...")
		if err := router.Run(listenAddr); err != nil {
			fmt.Printf("start server failed: %v\n", err)
			os.Exit(1)
		}
	}
}
