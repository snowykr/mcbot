package config

import (
	"strings"
	"testing"
	"time"
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
		DiscordToken:           "token",
		MCContainerName:        "mc-server",
		McbotRoleName:          "마크봇",
		EmbedChannelID:         "123456",
		StopTimeoutSeconds:     10,
		ServerOperationTimeout: 60 * time.Second,
		RCONHost:               "mc-server",
		RCONPort:               25575,
		RCONPassword:           "test",
		RCONTimeout:            10 * time.Second,
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
			name: "Zero AutoRecoverInterval with AutoRecoverEnabled=true",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 60 * time.Second
				c.StopTimeoutSeconds = 10
				c.AutoRecoverEnabled = true
				c.AutoRecoverInterval = 0
			},
			expectError: true,
			errorMsg:    "AUTO_RECOVER_INTERVAL_SECONDS must be positive",
		},
		{
			name: "Zero AutoRecoverInterval with AutoRecoverEnabled=false (should pass)",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 60 * time.Second
				c.StopTimeoutSeconds = 10
				c.AutoRecoverEnabled = false
				c.AutoRecoverInterval = 0
				c.CrashDetectionInterval = 1
				c.MaxInspectFailureAttempts = 1
			},
			expectError: false,
		},
		{
			name: "Zero CrashDetectionInterval",
			modifier: func(c *Config) {
				c.ReadyTimeout = 1
				c.EmbedUpdateTimeout = 1
				c.ServerOperationTimeout = 60 * time.Second
				c.StopTimeoutSeconds = 10
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
		ServerOperationTimeout: 60 * time.Second,
		StopTimeoutSeconds:     10,
		AutoRecoverEnabled:     true,
		AutoRecoverInterval:    1,
		CrashDetectionInterval: 1,
		RCONHost:               "mc-server",
		RCONPort:               25575,
		RCONPassword:           "test",
		RCONTimeout:            10 * time.Second,
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
			name: "Negative MaxAutoRecoverAttempts with AutoRecoverEnabled=true",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.AutoRecoverEnabled = true
				c.MaxAutoRecoverAttempts = -1
			},
			expectError: true,
			errorMsg:    "MAX_AUTO_RECOVER_ATTEMPTS must be non-negative (0 = unlimited)",
		},
		{
			name: "Negative MaxAutoRecoverAttempts with AutoRecoverEnabled=false (should pass)",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.AutoRecoverEnabled = false
				c.MaxAutoRecoverAttempts = -1
			},
			expectError: false,
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
		{
			name: "Zero RCONTimeout when RCON disabled (should pass)",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.RCONPassword = ""
				c.RCONTimeout = 0
			},
			expectError: false,
		},
		{
			name: "Negative RCONTimeout when RCON enabled",
			modifier: func(c *Config) {
				c.MaxInspectFailureAttempts = 1
				c.RCONPassword = "test"
				c.RCONTimeout = -1
			},
			expectError: true,
			errorMsg:    "RCON_TIMEOUT_SECONDS must be positive",
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

func TestValidate_StopTimeoutSeconds(t *testing.T) {
	validConfig := Config{
		DiscordToken:              "token",
		MCContainerName:           "mc-server",
		McbotRoleName:             "마크봇",
		EmbedChannelID:            "123456",
		ReadyTimeout:              60 * time.Second,
		EmbedUpdateTimeout:        10 * time.Second,
		ServerOperationTimeout:    150 * time.Second,
		StopTimeoutSeconds:        120,
		AutoRecoverEnabled:        false,
		CrashDetectionInterval:    2 * time.Second,
		MaxInspectFailureAttempts: 3,
		RCONHost:                  "mc-server",
		RCONPort:                  25575,
		RCONPassword:              "test",
		RCONTimeout:               10 * time.Second,
	}

	tests := []struct {
		name        string
		modifier    func(*Config)
		expectError bool
		errContains string
	}{
		{
			name: "Negative StopTimeoutSeconds",
			modifier: func(c *Config) {
				c.StopTimeoutSeconds = -1
			},
			expectError: true,
			errContains: "STOP_TIMEOUT_SECONDS must be non-negative",
		},
		{
			name: "Zero StopTimeoutSeconds (allowed - immediate stop)",
			modifier: func(c *Config) {
				c.StopTimeoutSeconds = 0
			},
			expectError: false,
		},
		{
			name: "Positive StopTimeoutSeconds",
			modifier: func(c *Config) {
				c.StopTimeoutSeconds = 60
			},
			expectError: false,
		},
		{
			name: "ServerOperationTimeout too small for StopTimeoutSeconds",
			modifier: func(c *Config) {
				c.StopTimeoutSeconds = 120
				c.ServerOperationTimeout = 100 * time.Second
			},
			expectError: true,
			errContains: "SERVER_OPERATION_TIMEOUT_SECONDS",
		},
		{
			name: "ServerOperationTimeout exactly at minimum (StopTimeout + 30s)",
			modifier: func(c *Config) {
				c.StopTimeoutSeconds = 60
				c.ServerOperationTimeout = 90 * time.Second
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
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidate_RCONSettingsWhenEnabled(t *testing.T) {
	baseConfig := Config{
		DiscordToken:              "token",
		MCContainerName:           "mc-server",
		McbotRoleName:             "마크봇",
		EmbedChannelID:            "123456",
		ReadyTimeout:              time.Second,
		EmbedUpdateTimeout:        time.Second,
		ServerOperationTimeout:    60 * time.Second,
		StopTimeoutSeconds:        10,
		CrashDetectionInterval:    time.Second,
		MaxInspectFailureAttempts: 1,
		RCONHost:                  "mc-server",
		RCONPort:                  25575,
		RCONPassword:              "configured",
		RCONTimeout:               10 * time.Second,
	}

	tests := []struct {
		name        string
		modifier    func(*Config)
		expectError string
	}{
		{
			name: "Empty host",
			modifier: func(c *Config) {
				c.RCONHost = "   "
			},
			expectError: "RCON_HOST is required when RCON is enabled",
		},
		{
			name: "Zero port",
			modifier: func(c *Config) {
				c.RCONPort = 0
			},
			expectError: "RCON_PORT must be between 1 and 65535 when RCON is enabled",
		},
		{
			name: "Too large port",
			modifier: func(c *Config) {
				c.RCONPort = 65536
			},
			expectError: "RCON_PORT must be between 1 and 65535 when RCON is enabled",
		},
		{
			name: "Disabled RCON skips host and port validation",
			modifier: func(c *Config) {
				c.RCONPassword = ""
				c.RCONHost = ""
				c.RCONPort = 0
				c.RCONTimeout = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig
			tt.modifier(&cfg)

			err := cfg.validate()
			if tt.expectError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error %q but got nil", tt.expectError)
			}
			if err.Error() != tt.expectError {
				t.Fatalf("expected error %q, got %q", tt.expectError, err.Error())
			}
		})
	}
}

func TestRCONEnabled_TrimsWhitespacePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		enabled  bool
	}{
		{name: "Configured password", password: "secret", enabled: true},
		{name: "Empty password", password: "", enabled: false},
		{name: "Whitespace-only password", password: "   \t\n  ", enabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{RCONPassword: tt.password}
			if got := cfg.RCONEnabled(); got != tt.enabled {
				t.Fatalf("expected RCONEnabled=%v, got %v", tt.enabled, got)
			}
		})
	}
}

func TestLoad_InvalidRCONNumericEnv(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("EMBED_CHANNEL_ID", "123456")

	tests := []struct {
		name    string
		key     string
		value   string
		errText string
	}{
		{
			name:    "Invalid RCON port",
			key:     "RCON_PORT",
			value:   "abc",
			errText: "invalid RCON_PORT",
		},
		{
			name:    "Invalid RCON timeout",
			key:     "RCON_TIMEOUT_SECONDS",
			value:   "abc",
			errText: "invalid RCON_TIMEOUT_SECONDS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected load error containing %q but got nil", tt.errText)
			}
			if !strings.Contains(err.Error(), tt.errText) {
				t.Fatalf("expected error containing %q, got %q", tt.errText, err.Error())
			}

			t.Setenv(tt.key, "")
		})
	}
}
