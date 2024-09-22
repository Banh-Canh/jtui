package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/Banh-Canh/jtui/internal/utils"
)

type Config struct {
	Jellyfin JellyfinConfig `yaml:"jellyfin"`
}

type JellyfinConfig struct {
	ServerURL string `yaml:"server_url"`
}

// Creates the YAML config file
func CreateDefaultConfigFile(filePath string) {
	viper.SetDefault("logLevel", "info")
	viper.SetDefault("jellyfin", map[string]interface{}{
		"server_url": "http://localhost:8096",
	})
	viper.SetConfigType("yaml")
	viper.SafeWriteConfigAs(filePath) // nolint:all
}

func GetConfigDirPath() (string, error) {
	// Construct the directory path to the config directory
	configDirPath := filepath.Join(xdg.ConfigHome, "jtui")
	if err := os.MkdirAll(configDirPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}
	return configDirPath, nil
}

func ReadConfig(filePath string) error {
	// Set up Viper to read from the config file
	viper.SetConfigFile(filePath)
	utils.Logger.Debug("Reading config file...", zap.String("filePath", filePath))
	// Read the config file
	if err := viper.ReadInConfig(); err != nil {
		utils.Logger.Error("Failed to read config file.", zap.Error(err))
		return fmt.Errorf("failed to read config file: %v", err)
	}
	return nil
}
