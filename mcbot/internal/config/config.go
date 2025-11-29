package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DiscordToken       string
	MCContainerName    string
	ReadyLogPattern    string
	ReadyTimeout       time.Duration
	McbotRoleName      string
	StopTimeoutSeconds int
}

func Load() (*Config, error) {
	cfg := &Config{
		DiscordToken:       os.Getenv("DISCORD_TOKEN"),
		MCContainerName:    getEnvOrDefault("MC_CONTAINER_NAME", "stardew_create_forge_server"),
		ReadyLogPattern:    getEnvOrDefault("READY_LOG_PATTERN", "Dedicated server took"),
		McbotRoleName:      getEnvOrDefault("MCBOT_ROLE_NAME", "마크봇"),
		StopTimeoutSeconds: getEnvIntOrDefault("STOP_TIMEOUT_SECONDS", 120),
	}

	readyTimeoutSec := getEnvIntOrDefault("READY_TIMEOUT_SECONDS", 600)
	cfg.ReadyTimeout = time.Duration(readyTimeoutSec) * time.Second

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.DiscordToken == "" {
		return errors.New("DISCORD_TOKEN is required")
	}
	if c.MCContainerName == "" {
		return errors.New("MC_CONTAINER_NAME is required")
	}
	if c.ReadyLogPattern == "" {
		return errors.New("READY_LOG_PATTERN is required")
	}
	if c.McbotRoleName == "" {
		return errors.New("MCBOT_ROLE_NAME is required")
	}
	return nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvIntOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}
