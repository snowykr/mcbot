package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DiscordToken           string
	MCContainerName        string
	ReadyTimeout           time.Duration
	McbotRoleName          string
	StopTimeoutSeconds     int
	EmbedChannelID         string
	MCJoinLogPattern       string
	MCLeaveLogPattern      string
	EmbedUpdateTimeout     time.Duration
	ServerOperationTimeout time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		DiscordToken:       os.Getenv("DISCORD_TOKEN"),
		MCContainerName:    getEnvOrDefault("MC_CONTAINER_NAME", "mc-server"),
		McbotRoleName:      getEnvOrDefault("MCBOT_ROLE_NAME", "마크봇"),
		StopTimeoutSeconds: getEnvIntOrDefault("STOP_TIMEOUT_SECONDS", 120),
		EmbedChannelID:     os.Getenv("EMBED_CHANNEL_ID"),
		MCJoinLogPattern:   getEnvOrDefault("MC_JOIN_LOG_PATTERN", `]: (.+) joined the game`),
		MCLeaveLogPattern:  getEnvOrDefault("MC_LEAVE_LOG_PATTERN", `]: (.+) left the game`),
	}

	readyTimeoutSec := getEnvIntOrDefault("READY_TIMEOUT_SECONDS", 600)
	cfg.ReadyTimeout = time.Duration(readyTimeoutSec) * time.Second

	embedUpdateTimeoutSec := getEnvIntOrDefault("EMBED_UPDATE_TIMEOUT_SECONDS", 10)
	cfg.EmbedUpdateTimeout = time.Duration(embedUpdateTimeoutSec) * time.Second

	serverOpTimeoutSec := getEnvIntOrDefault("SERVER_OPERATION_TIMEOUT_SECONDS", 720)
	cfg.ServerOperationTimeout = time.Duration(serverOpTimeoutSec) * time.Second

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
	if c.McbotRoleName == "" {
		return errors.New("MCBOT_ROLE_NAME is required")
	}
	if c.EmbedChannelID == "" {
		return errors.New("EMBED_CHANNEL_ID is required")
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
