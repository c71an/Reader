package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Port         string
	DataDir      string
	DBPath       string
	AdminUser    string
	AdminPass    string
	SessionSecret string
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

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = "reader-secret-key-change-me"
	}

	return &Config{
		Port:          port,
		DataDir:       dataDir,
		DBPath:        filepath.Join(dataDir, "reader.db"),
		AdminUser:     adminUser,
		AdminPass:     adminPass,
		SessionSecret: sessionSecret,
	}
}

