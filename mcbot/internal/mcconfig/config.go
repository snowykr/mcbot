package mcconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/snowy/mcbot/internal/atomicfile"
)

const DefaultPath = "mc-server.toml"

var memoryPattern = regexp.MustCompile(`^[1-9][0-9]*[MG]$`)

type Config struct {
	Server    ServerConfig    `json:"server" toml:"server"`
	Container ContainerConfig `json:"container" toml:"container"`
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
	return nil
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
