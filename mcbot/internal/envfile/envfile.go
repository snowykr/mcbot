package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/snowy/mcbot/internal/atomicfile"
)

const (
	DefaultPath = ".env"
	MaskedValue = "***MASKED***"
)

type File struct {
	Values map[string]string
	lines  []line
}

type Entry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type RevealPolicy struct {
	ShowSecrets bool
}

type ValidationResult struct {
	Path  string `json:"path"`
	Valid bool   `json:"valid"`
}

type line struct {
	raw   string
	key   string
	value string
	entry bool
}

func Init(path string, force ...bool) error {
	if !allowOverwrite(force) {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf(".env already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat env file: %w", err)
		}
	}
	return atomicWrite(path, []byte(Template()))
}

func allowOverwrite(force []bool) bool {
	return len(force) > 0 && force[0]
}

func Load(path string) (File, error) {
	parsed, err := parseFile(path, false)
	if err != nil {
		return File{}, err
	}
	return parsed, nil
}

func Show(path string, policy RevealPolicy) ([]Entry, error) {
	file, err := parseFile(path, false)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(file.Values))
	for key := range file.Values {
		if err := ValidateOwnedKey(key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]Entry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, Entry{Key: key, Value: displayValue(key, file.Values[key], policy), Secret: IsSecretKey(key)})
	}
	return entries, nil
}

func Get(path, key string, policy RevealPolicy) (Entry, error) {
	if err := ValidateOwnedKey(key); err != nil {
		return Entry{}, err
	}
	file, err := parseFile(path, false)
	if err != nil {
		return Entry{}, err
	}
	value, ok := file.Values[key]
	if !ok {
		return Entry{}, fmt.Errorf("%s is not set in %s", key, path)
	}
	return Entry{Key: key, Value: displayValue(key, value, policy), Secret: IsSecretKey(key)}, nil
}

func Set(path, key, value string) error {
	if err := ValidateOwnedKey(key); err != nil {
		return err
	}
	file, err := parseFile(path, false)
	if err != nil {
		return err
	}
	if err := validateValue(key, value); err != nil {
		return err
	}

	lastMatch := -1
	for i := range file.lines {
		if file.lines[i].entry && file.lines[i].key == key {
			lastMatch = i
		}
	}
	if lastMatch >= 0 {
		file.lines[lastMatch].raw = key + "=" + value
		file.lines[lastMatch].value = value
	} else {
		if len(file.lines) > 0 && strings.TrimSpace(file.lines[len(file.lines)-1].raw) != "" {
			file.lines = append(file.lines, line{})
		}
		file.lines = append(file.lines, line{raw: key + "=" + value, key: key, value: value, entry: true})
	}
	return atomicWrite(path, []byte(renderLines(file.lines)))
}

func Unset(path, key string) error {
	if err := ValidateOwnedKey(key); err != nil {
		return err
	}
	file, err := parseFile(path, false)
	if err != nil {
		return err
	}
	lines := make([]line, 0, len(file.lines))
	for _, current := range file.lines {
		if current.entry && current.key == key {
			continue
		}
		lines = append(lines, current)
	}
	return atomicWrite(path, []byte(renderLines(lines)))
}

func ValidateFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(".env does not exist: %s", path)
		}
		return fmt.Errorf("stat env file: %w", err)
	}
	file, err := parseFile(path, true)
	if err != nil {
		return err
	}
	for key, value := range file.Values {
		if err := ValidateOwnedKey(key); err != nil {
			return err
		}
		if err := validateValue(key, value); err != nil {
			return err
		}
	}
	return nil
}

func Validate(path string) (ValidationResult, error) {
	err := ValidateFile(path)
	return ValidationResult{Path: path, Valid: err == nil}, err
}

func Render(entries []Entry) string {
	var b strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&b, "%s=%s\n", entry.Key, entry.Value)
	}
	return b.String()
}

func Template() string {
	return strings.Join([]string{
		"# Discord Bot Token (required)",
		"DISCORD_TOKEN=your_discord_bot_token_here",
		"",
		"# Discord trusted guild snowflake ID (required)",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"",
		"# Initial persistent embed channel ID (optional)",
		"# EMBED_CHANNEL_ID=123456789012345678",
		"",
		"# Minecraft server container name (default: mc-server)",
		"# MC_CONTAINER_NAME=mc-server",
		"",
		"# RCON settings (optional)",
		"# RCON_PASSWORD=your_rcon_password_here",
		"# RCON_HOST=mc-server",
		"# RCON_PORT=25575",
		"# RCON_TIMEOUT_SECONDS=10",
		"# ENABLE_RCON=true",
		"# RCON_CMDS_STARTUP=gamerule keepInventory true",
		"",
		"# Debug logging (optional, default: false)",
		"# MCBOT_DEBUG=false",
		"",
	}, "\n")
}

func IsSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET")
}

func ParseBoolString(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be a boolean (true/false, 1/0, yes/no, on/off)")
	}
}

func CanonicalBoolString(value string) (string, error) {
	parsed, err := ParseBoolString(value)
	if err != nil {
		return "", err
	}
	if parsed {
		return "true", nil
	}
	return "false", nil
}

func parseFile(path string, strict bool) (File, error) {
	file := File{Values: map[string]string{}}
	data, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && !strict {
			return file, nil
		}
		return File{}, fmt.Errorf("read env file: %w", err)
	}
	defer data.Close()

	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(data)
	for scanner.Scan() {
		raw := scanner.Text()
		parsed := parseLine(raw)
		if parsed.entry {
			if strict {
				if err := ValidateOwnedKey(parsed.key); err != nil {
					return File{}, err
				}
				if _, exists := seen[parsed.key]; exists {
					return File{}, fmt.Errorf("duplicate .env key %s", parsed.key)
				}
				seen[parsed.key] = struct{}{}
			}
			file.Values[parsed.key] = parsed.value
		}
		file.lines = append(file.lines, parsed)
	}
	if err := scanner.Err(); err != nil {
		return File{}, fmt.Errorf("scan env file: %w", err)
	}
	return file, nil
}

func parseLine(raw string) line {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line{raw: raw}
	}
	key, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return line{raw: raw}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return line{raw: raw}
	}
	return line{raw: raw, key: key, value: strings.TrimSpace(value), entry: true}
}

func renderLines(lines []line) string {
	var b strings.Builder
	for _, current := range lines {
		b.WriteString(current.raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func displayValue(key, value string, policy RevealPolicy) string {
	if IsSecretKey(key) && !policy.ShowSecrets {
		return MaskedValue
	}
	return value
}

func validateValue(key, value string) error {
	switch key {
	case "DISCORD_TOKEN", "MCBOT_ROLE_NAME", "MC_CONTAINER_NAME":
		return validateNonBlank(key, value)
	case "MCBOT_TRUSTED_GUILD_ID":
		return validateRequiredSnowflakeID(key, value)
	case "EMBED_CHANNEL_ID":
		if value == "" {
			return nil
		}
		return validateRequiredSnowflakeID(key, value)
	case "READY_TIMEOUT_SECONDS", "SERVER_OPERATION_TIMEOUT_SECONDS", "EMBED_UPDATE_TIMEOUT_SECONDS", "AUTO_RECOVER_INTERVAL_SECONDS", "CRASH_DETECTION_INTERVAL_SECONDS", "RCON_TIMEOUT_SECONDS":
		return validatePositiveInt(key, value)
	case "STOP_TIMEOUT_SECONDS", "MAX_AUTO_RECOVER_ATTEMPTS":
		return validateMinInt(key, value, 0)
	case "MAX_INSPECT_FAILURE_ATTEMPTS":
		return validateMinInt(key, value, 1)
	case "AUTO_RECOVER_ENABLED", "ENABLE_RCON", "MCBOT_DEBUG":
		return validateBool(key, value)
	case "RCON_PORT":
		return validatePort(key, value)
	case "RCON_PASSWORD":
		if value != "" && strings.TrimSpace(value) != value {
			return fmt.Errorf("%s must not have leading or trailing whitespace", key)
		}
		if strings.TrimSpace(value) == "" && value != "" {
			return fmt.Errorf("%s must not be blank", key)
		}
	case "RCON_HOST":
		if value != "" {
			return validateNoOuterWhitespace(key, value)
		}
	}
	return nil
}

func validateNonBlank(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be blank", key)
	}
	return validateNoOuterWhitespace(key, value)
}

func validateNoOuterWhitespace(key, value string) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must not have leading or trailing whitespace", key)
	}
	return nil
}

func validateRequiredSnowflakeID(key, value string) error {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return fmt.Errorf("%s is required", key)
	}
	if value != trimmedValue {
		return fmt.Errorf("%s must not have leading or trailing whitespace", key)
	}
	parsedValue, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return fmt.Errorf("%s must be a Discord snowflake ID (digits only)", key)
	}
	if strconv.FormatUint(parsedValue, 10) != value {
		return fmt.Errorf("%s must be a canonical Discord snowflake ID (no leading zeros)", key)
	}
	if parsedValue == 0 {
		return fmt.Errorf("%s must be a non-zero Discord snowflake ID", key)
	}
	return nil
}

func validatePositiveInt(key, value string) error {
	return validateMinInt(key, value, 1)
}

func validateMinInt(key, value string, min int) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer", key)
	}
	if parsed < min {
		return fmt.Errorf("%s must be at least %d", key, min)
	}
	return nil
}

func validatePort(key, value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", key)
	}
	return nil
}

func validateBool(key, value string) error {
	if _, err := ParseBoolString(value); err != nil {
		return fmt.Errorf("%s %v", key, err)
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create env file directory: %w", err)
	}
	tempFile, err := os.CreateTemp(dir, ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("create env temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write env temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync env temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close env temp file: %w", err)
	}
	if err := atomicfile.Replace(tempPath, path); err != nil {
		return fmt.Errorf("replace env file: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
