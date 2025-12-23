package mcserver

import (
	"fmt"
	"testing"
)

func TestBuiltinReadyPatterns(t *testing.T) {
	testCases := []struct {
		name            string
		logLine         string
		shouldMatch     bool
		expectedSeconds float64
	}{
		{
			name:            "itzg vanilla default pattern",
			logLine:         `[12:34:56] [Server thread/INFO]: Done (23.456s)! For help, type "help"`,
			shouldMatch:     true,
			expectedSeconds: 23.456,
		},
		{
			name:            "itzg vanilla short pattern",
			logLine:         "[12:34:56] [Server thread/INFO]: Done (15.789s)!",
			shouldMatch:     true,
			expectedSeconds: 15.789,
		},
		{
			name:            "paper/spigot dedicated server pattern",
			logLine:         "[12:34:56] [Server thread/INFO]: Dedicated server took 42.123 seconds to load",
			shouldMatch:     true,
			expectedSeconds: 42.123,
		},
		{
			name:        "non-matching log line",
			logLine:     "[12:34:56] [Server thread/INFO]: Starting minecraft server version 1.20.1",
			shouldMatch: false,
		},
	}

	patterns, err := newReadyMatchers()
	if err != nil {
		t.Fatalf("Failed to initialize ready patterns: %v", err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			matched := false
			var actualSeconds float64

			for _, pattern := range patterns {
				matches := pattern.re.FindStringSubmatch(tc.logLine)
				if len(matches) >= 2 {
					matched = true
					var parseErr error
					actualSeconds, parseErr = parseFloat(matches[1])
					if parseErr != nil {
						t.Errorf("Pattern '%s' matched but failed to parse seconds: %v", pattern.name, parseErr)
						continue
					}
					break
				}
			}

			if matched != tc.shouldMatch {
				t.Errorf("Expected match=%v, got match=%v for log line: %s", tc.shouldMatch, matched, tc.logLine)
			}

			if tc.shouldMatch && matched {
				if actualSeconds != tc.expectedSeconds {
					t.Errorf("Expected seconds=%v, got seconds=%v", tc.expectedSeconds, actualSeconds)
				}
			}
		})
	}
}

func TestNewReadyMatchers(t *testing.T) {
	patterns, err := newReadyMatchers()
	if err != nil {
		t.Fatalf("newReadyMatchers() failed: %v", err)
	}

	if len(patterns) == 0 {
		t.Fatal("Expected at least one builtin pattern, got none")
	}

	for i, p := range patterns {
		if p.re == nil {
			t.Errorf("Pattern %d (%s) has nil regexp", i, p.name)
		}
		if p.name == "" {
			t.Errorf("Pattern %d has empty name", i)
		}
		if p.re != nil && p.re.NumSubexp() < 1 {
			t.Errorf("Pattern %s must have at least 1 capture group", p.name)
		}
	}
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
