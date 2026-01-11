package config

import (
	"errors"
	"log"
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
	AutoRecoverEnabled     bool
	AutoRecoverInterval    time.Duration
	MaxAutoRecoverAttempts int

	CrashDetectionInterval    time.Duration
	MaxInspectFailureAttempts int
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

	cfg.AutoRecoverEnabled = getEnvBoolOrDefault("AUTO_RECOVER_ENABLED", true)

	autoRecoverIntervalSec := getEnvIntOrDefault("AUTO_RECOVER_INTERVAL_SECONDS", 30)
	cfg.AutoRecoverInterval = time.Duration(autoRecoverIntervalSec) * time.Second

	cfg.MaxAutoRecoverAttempts = getEnvIntOrDefault("MAX_AUTO_RECOVER_ATTEMPTS", 3)

	crashDetectionIntervalSec := getEnvIntOrDefault("CRASH_DETECTION_INTERVAL_SECONDS", 2)
	cfg.CrashDetectionInterval = time.Duration(crashDetectionIntervalSec) * time.Second

	cfg.MaxInspectFailureAttempts = getEnvIntOrDefault("MAX_INSPECT_FAILURE_ATTEMPTS", 3)

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
	if c.ReadyTimeout <= 0 {
		return errors.New("READY_TIMEOUT_SECONDS must be positive")
	}
	if c.EmbedUpdateTimeout <= 0 {
		return errors.New("EMBED_UPDATE_TIMEOUT_SECONDS must be positive")
	}
	if c.ServerOperationTimeout <= 0 {
		return errors.New("SERVER_OPERATION_TIMEOUT_SECONDS must be positive")
	}
	if c.AutoRecoverEnabled && c.AutoRecoverInterval <= 0 {
		return errors.New("AUTO_RECOVER_INTERVAL_SECONDS must be positive")
	}
	if c.CrashDetectionInterval <= 0 {
		return errors.New("CRASH_DETECTION_INTERVAL_SECONDS must be positive")
	}
	if c.MaxInspectFailureAttempts < 1 {
		return errors.New("MAX_INSPECT_FAILURE_ATTEMPTS must be at least 1")
	}
	if c.MaxAutoRecoverAttempts < 0 {
		return errors.New("MAX_AUTO_RECOVER_ATTEMPTS must be non-negative (0 = unlimited)")
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

func getEnvBoolOrDefault(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		switch val {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		default:
			log.Printf("[WARN] unrecognized boolean value %q for %s, using default %v", val, key, defaultVal)
		}
	}
	return defaultVal
}
