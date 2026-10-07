package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Port         string
	DataDir      string
	DBPath       string
	AdminUser    string
	AdminPass    string
	FetchTimeout int // 抓取超时秒数，默认 45s
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}

	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = "admin123"
	}

	fetchTimeout := 45
	if timeoutStr := os.Getenv("FETCH_TIMEOUT"); timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			fetchTimeout = t
		}
	}

	return &Config{
		Port:         port,
		DataDir:      dataDir,
		DBPath:       filepath.Join(dataDir, "reader.db"),
		AdminUser:    adminUser,
		AdminPass:    adminPass,
		FetchTimeout: fetchTimeout,
	}
}


