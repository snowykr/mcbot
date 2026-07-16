package mcconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/snowy/mcbot/internal/atomicfile"
)

const DefaultPath = "mc-server.toml"

var memoryPattern = regexp.MustCompile(`^[1-9][0-9]*[MG]$`)

type Config struct {
	Server    ServerConfig    `json:"server" toml:"server"`
	Container ContainerConfig `json:"container" toml:"container"`
	Backup    BackupConfig    `json:"backup" toml:"backup"`
}

type InvalidFileError struct {
	Err error
}

func (e *InvalidFileError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *InvalidFileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ServerConfig struct {
	Version            string `json:"version" toml:"version"`
	Type               string `json:"type" toml:"type"`
	Difficulty         string `json:"difficulty" toml:"difficulty"`
	Memory             string `json:"memory" toml:"memory"`
	InitMemory         string `json:"init_memory" toml:"init_memory"`
	MOTD               string `json:"motd" toml:"motd"`
	ViewDistance       int    `json:"view_distance" toml:"view_distance"`
	SimulationDistance int    `json:"simulation_distance" toml:"simulation_distance"`
}

type ContainerConfig struct {
	RestartPolicy string `json:"restart_policy" toml:"restart_policy"`
	PortPublish   string `json:"port_publish" toml:"port_publish"`
	UID           int    `json:"uid" toml:"uid"`
	GID           int    `json:"gid" toml:"gid"`
}

type BackupConfig struct {
	Enabled               bool   `json:"enabled" toml:"enabled"`
	Directory             string `json:"directory" toml:"directory"`
	RetentionCount        int    `json:"retention_count" toml:"retention_count"`
	RetentionDays         int    `json:"retention_days" toml:"retention_days"`
	RetentionMaxBytes     int64  `json:"retention_max_bytes" toml:"retention_max_bytes"`
	DailyTime             string `json:"daily_time" toml:"daily_time"`
	Timezone              string `json:"timezone" toml:"timezone"`
	QuiesceTimeoutSeconds int    `json:"quiesce_timeout_seconds" toml:"quiesce_timeout_seconds"`
	IncludeMCServerTOML   bool   `json:"include_mc_server_toml" toml:"include_mc_server_toml"`
}

func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Version:            "1.20.1",
			Type:               "FORGE",
			Difficulty:         "easy",
			Memory:             "14G",
			InitMemory:         "14G",
			MOTD:               "SNOWY'S SERVER",
			ViewDistance:       8,
			SimulationDistance: 8,
		},
		Container: ContainerConfig{
			RestartPolicy: "no",
			PortPublish:   "25565:25565",
			UID:           1000,
			GID:           1000,
		},
		Backup: BackupConfig{
			Enabled:               true,
			Directory:             "backups",
			RetentionCount:        14,
			RetentionDays:         0,
			RetentionMaxBytes:     0,
			DailyTime:             "04:00",
			Timezone:              "Local",
			QuiesceTimeoutSeconds: 30,
			IncludeMCServerTOML:   true,
		},
	}
}

func Init(path string, force ...bool) error {
	if !allowOverwrite(force) {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("mc-server.toml already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat mc-server config: %w", err)
		}
	}
	return Write(path, Defaults())
}

func allowOverwrite(force []bool) bool {
	return len(force) > 0 && force[0]
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read mc-server config: %w", err)
	}
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, &InvalidFileError{Err: fmt.Errorf("parse mc-server config: %w", err)}
	}
	if err := validateDecodedKeys(meta); err != nil {
		return Config{}, &InvalidFileError{Err: err}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, &InvalidFileError{Err: err}
	}
	return cfg, nil
}

func validateDecodedKeys(meta toml.MetaData) error {
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}

	keys := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return fmt.Errorf("unknown mc-server.toml key %q", keys[0])
	}
	return fmt.Errorf("unknown mc-server.toml keys: %s", strings.Join(keys, ", "))
}

func ValidateFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mc-server.toml does not exist: %s", path)
		}
		return fmt.Errorf("stat mc-server config: %w", err)
	}
	_, err := Load(path)
	return err
}

func Write(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return atomicWrite(path, []byte(Render(cfg)))
}

func Set(path, key, value string) error {
	if err := ValidateOwnedKey(key); err != nil {
		return err
	}
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := cfg.set(key, value); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return Write(path, cfg)
}

// ApplyValue mutates cfg using the same key ownership and typed parsing rules as Set.
func ApplyValue(cfg *Config, key, value string) error {
	if cfg == nil {
		return fmt.Errorf("mc-server config must not be nil")
	}
	if err := ValidateOwnedKey(key); err != nil {
		return err
	}
	return cfg.set(key, value)
}

func Get(path, key string) (any, error) {
	if err := ValidateOwnedKey(key); err != nil {
		return nil, err
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return cfg.Value(key)
}

func Show(path string) (Config, error) {
	return Load(path)
}

func Render(c Config) string {
	var b strings.Builder
	b.WriteString("[server]\n")
	fmt.Fprintf(&b, "version = %s\n", quote(c.Server.Version))
	fmt.Fprintf(&b, "type = %s\n", quote(c.Server.Type))
	fmt.Fprintf(&b, "difficulty = %s\n", quote(c.Server.Difficulty))
	fmt.Fprintf(&b, "memory = %s\n", quote(c.Server.Memory))
	fmt.Fprintf(&b, "init_memory = %s\n", quote(c.Server.InitMemory))
	fmt.Fprintf(&b, "motd = %s\n", quote(c.Server.MOTD))
	fmt.Fprintf(&b, "view_distance = %d\n", c.Server.ViewDistance)
	fmt.Fprintf(&b, "simulation_distance = %d\n", c.Server.SimulationDistance)
	b.WriteString("\n[container]\n")
	fmt.Fprintf(&b, "restart_policy = %s\n", quote(c.Container.RestartPolicy))
	fmt.Fprintf(&b, "port_publish = %s\n", quote(c.Container.PortPublish))
	fmt.Fprintf(&b, "uid = %d\n", c.Container.UID)
	fmt.Fprintf(&b, "gid = %d\n", c.Container.GID)
	b.WriteString("\n[backup]\n")
	fmt.Fprintf(&b, "enabled = %t\n", c.Backup.Enabled)
	fmt.Fprintf(&b, "directory = %s\n", quote(c.Backup.Directory))
	fmt.Fprintf(&b, "retention_count = %d\n", c.Backup.RetentionCount)
	fmt.Fprintf(&b, "retention_days = %d\n", c.Backup.RetentionDays)
	fmt.Fprintf(&b, "retention_max_bytes = %d\n", c.Backup.RetentionMaxBytes)
	fmt.Fprintf(&b, "daily_time = %s\n", quote(c.Backup.DailyTime))
	fmt.Fprintf(&b, "timezone = %s\n", quote(c.Backup.Timezone))
	fmt.Fprintf(&b, "quiesce_timeout_seconds = %d\n", c.Backup.QuiesceTimeoutSeconds)
	fmt.Fprintf(&b, "include_mc_server_toml = %t\n", c.Backup.IncludeMCServerTOML)
	return b.String()
}

func (c Config) Validate() error {
	if c.Server.Version == "" {
		return fmt.Errorf("server.version must not be empty")
	}
	if !oneOf(c.Server.Type, "VANILLA", "FORGE", "FABRIC", "PAPER") {
		return fmt.Errorf("server.type must be one of VANILLA, FORGE, FABRIC, PAPER")
	}
	if !oneOf(c.Server.Difficulty, "peaceful", "easy", "normal", "hard") {
		return fmt.Errorf("server.difficulty must be one of peaceful, easy, normal, hard")
	}
	if !memoryPattern.MatchString(c.Server.Memory) {
		return fmt.Errorf("server.memory must be a JVM memory string like 512M or 14G")
	}
	if !memoryPattern.MatchString(c.Server.InitMemory) {
		return fmt.Errorf("server.init_memory must be a JVM memory string like 512M or 14G")
	}
	if c.Server.ViewDistance < 0 {
		return fmt.Errorf("server.view_distance must be a non-negative integer")
	}
	if c.Server.SimulationDistance < 0 {
		return fmt.Errorf("server.simulation_distance must be a non-negative integer")
	}
	if !oneOf(c.Container.RestartPolicy, "no", "always", "on-failure", "unless-stopped") {
		return fmt.Errorf("container.restart_policy must be one of no, always, on-failure, unless-stopped")
	}
	if !validPortPublish(c.Container.PortPublish) {
		return fmt.Errorf("container.port_publish must use HOST:CONTAINER format")
	}
	if c.Container.UID < 0 {
		return fmt.Errorf("container.uid must be a non-negative integer")
	}
	if c.Container.GID < 0 {
		return fmt.Errorf("container.gid must be a non-negative integer")
	}
	if err := validateBackup(c.Backup); err != nil {
		return err
	}
	return nil
}

func validateBackup(cfg BackupConfig) error {
	if err := ValidateBackupDirectory(cfg.Directory); err != nil {
		return err
	}
	if cfg.RetentionCount < 1 {
		return fmt.Errorf("backup.retention_count must be at least 1")
	}
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("backup.retention_days must be a non-negative integer")
	}
	if cfg.RetentionMaxBytes < 0 {
		return fmt.Errorf("backup.retention_max_bytes must be a non-negative integer")
	}
	if !validDailyTime(cfg.DailyTime) {
		return fmt.Errorf("backup.daily_time must use strict HH:MM 24-hour format")
	}
	if !validTimezone(cfg.Timezone) {
		return fmt.Errorf("backup.timezone must be Local or a valid IANA timezone")
	}
	if cfg.QuiesceTimeoutSeconds < 5 || cfg.QuiesceTimeoutSeconds > 300 {
		return fmt.Errorf("backup.quiesce_timeout_seconds must be between 5 and 300")
	}
	return nil
}

func ValidateBackupDirectory(value string) error {
	if value == "" {
		return fmt.Errorf("backup.directory must not be empty")
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("backup.directory must be a relative path under the repository root")
	}
	clean := filepath.Clean(value)
	if clean != value {
		return fmt.Errorf("backup.directory must be a clean relative path")
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup.directory must stay under the repository root")
	}
	slash := filepath.ToSlash(clean)
	protected := []string{"data/minecraft", "data/mcbot", "mcbot", ".git", ".omx", ".env", "mc-server.toml"}
	for _, path := range protected {
		if slash == path || strings.HasPrefix(slash, path+"/") {
			return fmt.Errorf("backup.directory must not be inside protected path %s", path)
		}
	}
	if slash == "data" {
		return fmt.Errorf("backup.directory must not be the data root")
	}
	if strings.HasPrefix(slash, "data/") {
		return fmt.Errorf("backup.directory must not be inside protected path data")
	}
	return nil
}

func validDailyTime(value string) bool {
	if len(value) != len("04:00") {
		return false
	}
	if value[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

func validTimezone(value string) bool {
	if value == "" {
		return false
	}
	if value == "Local" {
		return true
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func (c Config) Value(path string) (any, error) {
	switch path {
	case "server.version":
		return c.Server.Version, nil
	case "server.type":
		return c.Server.Type, nil
	case "server.difficulty":
		return c.Server.Difficulty, nil
	case "server.memory":
		return c.Server.Memory, nil
	case "server.init_memory":
		return c.Server.InitMemory, nil
	case "server.motd":
		return c.Server.MOTD, nil
	case "server.view_distance":
		return c.Server.ViewDistance, nil
	case "server.simulation_distance":
		return c.Server.SimulationDistance, nil
	case "container.restart_policy":
		return c.Container.RestartPolicy, nil
	case "container.port_publish":
		return c.Container.PortPublish, nil
	case "container.uid":
		return c.Container.UID, nil
	case "container.gid":
		return c.Container.GID, nil
	case "backup.enabled":
		return c.Backup.Enabled, nil
	case "backup.directory":
		return c.Backup.Directory, nil
	case "backup.retention_count":
		return c.Backup.RetentionCount, nil
	case "backup.retention_days":
		return c.Backup.RetentionDays, nil
	case "backup.retention_max_bytes":
		return c.Backup.RetentionMaxBytes, nil
	case "backup.daily_time":
		return c.Backup.DailyTime, nil
	case "backup.timezone":
		return c.Backup.Timezone, nil
	case "backup.quiesce_timeout_seconds":
		return c.Backup.QuiesceTimeoutSeconds, nil
	case "backup.include_mc_server_toml":
		return c.Backup.IncludeMCServerTOML, nil
	default:
		return nil, ValidateOwnedKey(path)
	}
}

func (c *Config) set(path, rawValue string) error {
	switch path {
	case "server.version":
		c.Server.Version = parseStringValue(rawValue)
	case "server.type":
		c.Server.Type = parseStringValue(rawValue)
	case "server.difficulty":
		c.Server.Difficulty = parseStringValue(rawValue)
	case "server.memory":
		c.Server.Memory = parseStringValue(rawValue)
	case "server.init_memory":
		c.Server.InitMemory = parseStringValue(rawValue)
	case "server.motd":
		c.Server.MOTD = parseStringValue(rawValue)
	case "server.view_distance":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("server.view_distance must be an integer: %w", err)
		}
		c.Server.ViewDistance = value
	case "server.simulation_distance":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("server.simulation_distance must be an integer: %w", err)
		}
		c.Server.SimulationDistance = value
	case "container.restart_policy":
		c.Container.RestartPolicy = parseStringValue(rawValue)
	case "container.port_publish":
		c.Container.PortPublish = parseStringValue(rawValue)
	case "container.uid":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("container.uid must be an integer: %w", err)
		}
		c.Container.UID = value
	case "container.gid":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("container.gid must be an integer: %w", err)
		}
		c.Container.GID = value
	case "backup.enabled":
		value, err := parseBoolValue(rawValue)
		if err != nil {
			return fmt.Errorf("backup.enabled must be a boolean: %w", err)
		}
		c.Backup.Enabled = value
	case "backup.directory":
		c.Backup.Directory = parseStringValue(rawValue)
	case "backup.retention_count":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("backup.retention_count must be an integer: %w", err)
		}
		c.Backup.RetentionCount = value
	case "backup.retention_days":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("backup.retention_days must be an integer: %w", err)
		}
		c.Backup.RetentionDays = value
	case "backup.retention_max_bytes":
		value, err := parseInt64Value(rawValue)
		if err != nil {
			return fmt.Errorf("backup.retention_max_bytes must be an integer: %w", err)
		}
		c.Backup.RetentionMaxBytes = value
	case "backup.daily_time":
		c.Backup.DailyTime = parseStringValue(rawValue)
	case "backup.timezone":
		c.Backup.Timezone = parseStringValue(rawValue)
	case "backup.quiesce_timeout_seconds":
		value, err := parseIntValue(rawValue)
		if err != nil {
			return fmt.Errorf("backup.quiesce_timeout_seconds must be an integer: %w", err)
		}
		c.Backup.QuiesceTimeoutSeconds = value
	case "backup.include_mc_server_toml":
		value, err := parseBoolValue(rawValue)
		if err != nil {
			return fmt.Errorf("backup.include_mc_server_toml must be a boolean: %w", err)
		}
		c.Backup.IncludeMCServerTOML = value
	default:
		return ValidateOwnedKey(path)
	}
	return nil
}

func parseIntValue(raw string) (int, error) {
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) {
		return 0, fmt.Errorf("quoted value %q is not an integer", raw)
	}
	return strconv.Atoi(raw)
}

func parseInt64Value(raw string) (int64, error) {
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) {
		return 0, fmt.Errorf("quoted value %q is not an integer", raw)
	}
	return strconv.ParseInt(raw, 10, 64)
}

func parseBoolValue(raw string) (bool, error) {
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) {
		return false, fmt.Errorf("quoted value %q is not a boolean", raw)
	}
	return strconv.ParseBool(raw)
}

func parseStringValue(raw string) string {
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			return unquoted
		}
	}
	return raw
}

func quote(value string) string {
	return strconv.Quote(value)
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validPortPublish(value string) bool {
	host, container, ok := strings.Cut(value, ":")
	if !ok || host == "" || container == "" || strings.Contains(container, ":") {
		return false
	}
	hostPort, err := strconv.Atoi(host)
	if err != nil || hostPort <= 0 || hostPort > 65535 {
		return false
	}
	containerPort, err := strconv.Atoi(container)
	return err == nil && containerPort > 0 && containerPort <= 65535
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create mc-server config directory: %w", err)
	}
	tempFile, err := os.CreateTemp(dir, ".mc-server-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("create mc-server config temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write mc-server config temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync mc-server config temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close mc-server config temp file: %w", err)
	}
	if err := atomicfile.Replace(tempPath, path); err != nil {
		return fmt.Errorf("replace mc-server config: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func (c Config) ComposeEnvironment() map[string]string {
	return map[string]string{
		"VERSION":                  c.Server.Version,
		"TYPE":                     c.Server.Type,
		"DIFFICULTY":               c.Server.Difficulty,
		"MEMORY":                   c.Server.Memory,
		"INIT_MEMORY":              c.Server.InitMemory,
		"MOTD":                     c.Server.MOTD,
		"VIEW_DISTANCE":            strconv.Itoa(c.Server.ViewDistance),
		"SIMULATION_DISTANCE":      strconv.Itoa(c.Server.SimulationDistance),
		"MC_SERVER_RESTART_POLICY": c.Container.RestartPolicy,
		"MC_SERVER_PORT_PUBLISH":   c.Container.PortPublish,
		"UID":                      strconv.Itoa(c.Container.UID),
		"GID":                      strconv.Itoa(c.Container.GID),
	}
}
