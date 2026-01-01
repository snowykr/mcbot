package config

import (
	"testing"
)

func TestValidate_RequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "Missing DiscordToken",
			config: Config{
				MCContainerName: "mc-server",
				McbotRoleName:   "마크봇",
				EmbedChannelID:  "123456",
			},
			expectError: true,
			errorMsg:    "DISCORD_TOKEN is required",
		},
		{
			name: "Missing MCContainerName",
			config: Config{
				DiscordToken:   "token",
				McbotRoleName:  "마크봇",
				EmbedChannelID: "123456",
			},
			expectError: true,
			errorMsg:    "MC_CONTAINER_NAME is required",
		},
		{
			name: "Missing McbotRoleName",
			config: Config{
				DiscordToken:    "token",
				MCContainerName: "mc-server",
				EmbedChannelID:  "123456",
			},
			expectError: true,
			errorMsg:    "MCBOT_ROLE_NAME is required",
		},
		{
			name: "Missing EmbedChannelID",
			config: Config{
				DiscordToken:    "token",
				MCContainerName: "mc-server",
				McbotRoleName:   "마크봇",
			},
			expectError: true,
			errorMsg:    "EMBED_CHANNEL_ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error but got nil")
				}
				if err.Error() != tt.errorMsg {
					t.Errorf("Expected error %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidate_DurationFields(t *testing.T) {
	baseConfig := Config{
		DiscordToken:    "token",
		MCContainerName: "mc-server",
		McbotRoleName:   "마크봇",
		EmbedChannelID:  "123456",
	}

	tests := []struct {
		name        string
		modifier    func(*Config)
		expectError bool
		errorMsg    string
	}{
		{
			name: "Zero ReadyTimeout",
			modifier: func(c *Config) {
				c.ReadyTimeout = 0
			},
			expectError: true,
			errorMsg:    "READY_TIMEOUT_SECONDS must be positive",
		},
		{
			name: "Negative ReadyTimeout",
			modifier: func(c *Config) {
				c.ReadyTimeout = -1
			},
			expectError: true,
			errorMsg:    "READY_TIMEOUT_SECONDS must be positive",
		},
		{
			name: "Zero EmbedUpdateTimeout",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 0
			},
			expectError: true,
			errorMsg:    "EMBED_UPDATE_TIMEOUT_SECONDS must be positive",
		},
		{
			name: "Zero ServerOperationTimeout",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 0
			},
			expectError: true,
			errorMsg:    "SERVER_OPERATION_TIMEOUT_SECONDS must be positive",
		},
		{
			name: "Zero AutoRecoverInterval",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 1
				c.AutoRecoverInterval = 0
			},
			expectError: true,
			errorMsg:    "AUTO_RECOVER_INTERVAL_SECONDS must be positive",
		},
		{
			name: "Zero CrashDetectionInterval",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 1
				c.AutoRecoverInterval = 1
				c.CrashDetectionInterval = 0
			},
			expectError: true,
			errorMsg:    "CRASH_DETECTION_INTERVAL_SECONDS must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig
			tt.modifier(&cfg)
			err := cfg.validate()
			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error but got nil")
				}
				if err.Error() != tt.errorMsg {
					t.Errorf("Expected error %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidate_AttemptFields(t *testing.T) {
	validConfig := Config{
		DiscordToken:           "token",
		MCContainerName:        "mc-server",
		McbotRoleName:          "마크봇",
		EmbedChannelID:         "123456",
		ReadyTimeout:           1,
		EmbedUpdateTimeout:     1,
		ServerOperationTimeout: 1,
		AutoRecoverInterval:    1,
		CrashDetectionInterval: 1,
	}

	tests := []struct {
		name        string
		modifier    func(*Config)
		expectError bool
		errorMsg    string
	}{
		{
			name: "Zero MaxInspectFailureAttempts",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 0
			},
			expectError: true,
			errorMsg:    "MAX_INSPECT_FAILURE_ATTEMPTS must be at least 1",
		},
		{
			name: "Negative MaxInspectFailureAttempts",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = -1
			},
			expectError: true,
			errorMsg:    "MAX_INSPECT_FAILURE_ATTEMPTS must be at least 1",
		},
		{
			name: "Valid MaxInspectFailureAttempts = 1",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.MaxAutoRecoverAttempts = 0
			},
			expectError: false,
		},
		{
			name: "Negative MaxAutoRecoverAttempts",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.MaxAutoRecoverAttempts = -1
			},
			expectError: true,
			errorMsg:    "MAX_AUTO_RECOVER_ATTEMPTS must be non-negative (0 = unlimited)",
		},
		{
			name: "Zero MaxAutoRecoverAttempts (unlimited)",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.MaxAutoRecoverAttempts = 0
			},
			expectError: false,
		},
		{
			name: "Positive MaxAutoRecoverAttempts",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.MaxAutoRecoverAttempts = 5
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig
			tt.modifier(&cfg)
			err := cfg.validate()
			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error but got nil")
				}
				if err.Error() != tt.errorMsg {
					t.Errorf("Expected error %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGetEnvBoolOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		envValue   string
		defaultVal bool
		expected   bool
	}{
		{"true string", "true", false, true},
		{"1 string", "1", false, true},
		{"yes string", "yes", false, true},
		{"on string", "on", false, true},
		{"false string", "false", true, false},
		{"0 string", "0", true, false},
		{"no string", "no", true, false},
		{"off string", "off", true, false},
		{"empty uses default true", "", true, true},
		{"empty uses default false", "", false, false},
		{"unrecognized uses default true", "invalid", true, true},
		{"unrecognized uses default false", "invalid", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_BOOL_ENV_VAR"
			t.Setenv(key, tt.envValue)

			result := getEnvBoolOrDefault(key, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
