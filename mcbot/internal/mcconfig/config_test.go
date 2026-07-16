package mcconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesCanonicalDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")

	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	assertFileContent(t, path, Render(Defaults()))
}

func TestSetValidKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := Set(path, "server.memory", "8G"); err != nil {
		t.Fatalf("Set server.memory failed: %v", err)
	}
	if err := Set(path, "server.view_distance", "12"); err != nil {
		t.Fatalf("Set server.view_distance failed: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Memory != "8G" || cfg.Server.ViewDistance != 12 {
		t.Fatalf("updated config = %+v, want memory 8G and view distance 12", cfg.Server)
	}
	assertFileContent(t, path, Render(cfg))
}

func TestGetShowValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	before := readFile(t, path)

	cfg, err := Show(path)
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if cfg.Server.Type != "FORGE" {
		t.Fatalf("server.type = %q, want FORGE", cfg.Server.Type)
	}
	value, err := Get(path, "container.uid")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if value != 1000 {
		t.Fatalf("container.uid = %v, want 1000", value)
	}
	if err := ValidateFile(path); err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("ValidateFile changed file:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestSetRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	before := readFile(t, path)

	if err := Set(path, "server.seed", "12345"); err == nil {
		t.Fatal("Set unknown key succeeded, want error")
	}
	assertFileContent(t, path, before)
}

func TestSetRejectsInvalidType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	before := readFile(t, path)

	for _, tc := range []struct {
		key   string
		value string
	}{
		{key: "server.memory", value: "fourteen-gigabytes"},
		{key: "container.uid", value: "\"1001\""},
		{key: "server.difficulty", value: "brutal"},
		{key: "container.port_publish", value: "25565"},
	} {
		if err := Set(path, tc.key, tc.value); err == nil {
			t.Fatalf("Set(%s, %s) succeeded, want error", tc.key, tc.value)
		}
		assertFileContent(t, path, before)
	}
}

func TestValidateLeavesFileUnchangedOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	contents := strings.Join([]string{
		"[server]",
		`version = "1.20.1"`,
		`type = "FORGE"`,
		`difficulty = "impossible"`,
		`memory = "14G"`,
		`init_memory = "14G"`,
		`motd = "SNOWY'S SERVER"`,
		"view_distance = 8",
		"simulation_distance = 8",
		"",
		"[container]",
		`restart_policy = "no"`,
		`port_publish = "25565:25565"`,
		"uid = 1000",
		"gid = 1000",
	}, "\n") + "\n"
	writeFile(t, path, contents)

	if err := ValidateFile(path); err == nil {
		t.Fatal("ValidateFile succeeded, want validation error")
	}
	assertFileContent(t, path, contents)
}

func TestLoadAcceptsValidTOMLInlineCommentsAndLiteralStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	contents := strings.Join([]string{
		"[server]",
		`version = '1.20.1' # literal string with comment`,
		`type = 'FORGE'`,
		`difficulty = "easy" # basic string with comment`,
		`memory = "14G" # JVM memory`,
		`init_memory = '12G'`,
		`motd = "SNOWY'S SERVER"`,
		"view_distance = 10 # integer with comment",
		"simulation_distance = 12",
		"",
		"[container]",
		`restart_policy = 'unless-stopped'`,
		`port_publish = '25565:25565' # publish mapping`,
		"uid = 1002",
		"gid = 1003",
	}, "\n") + "\n"
	writeFile(t, path, contents)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Type != "FORGE" || cfg.Server.Memory != "14G" || cfg.Server.InitMemory != "12G" {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.Server.MOTD != "SNOWY'S SERVER" || cfg.Server.ViewDistance != 10 || cfg.Server.SimulationDistance != 12 {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.Container.RestartPolicy != "unless-stopped" || cfg.Container.PortPublish != "25565:25565" {
		t.Fatalf("unexpected container config: %+v", cfg.Container)
	}
	if cfg.Container.UID != 1002 || cfg.Container.GID != 1003 {
		t.Fatalf("unexpected container config: %+v", cfg.Container)
	}
}

func TestLoadAppliesDefaultsForOmittedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	writeFile(t, path, "[server]\nmotd = 'HELLO'\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	defaults := Defaults()
	if cfg.Server.MOTD != "HELLO" {
		t.Fatalf("server.motd = %q, want HELLO", cfg.Server.MOTD)
	}
	if cfg.Server.Type != defaults.Server.Type || cfg.Container.PortPublish != defaults.Container.PortPublish {
		t.Fatalf("defaults not preserved: cfg=%+v defaults=%+v", cfg, defaults)
	}
	if cfg.Backup.Directory != defaults.Backup.Directory || cfg.Backup.RetentionCount != defaults.Backup.RetentionCount {
		t.Fatalf("backup defaults not preserved: cfg=%+v defaults=%+v", cfg.Backup, defaults.Backup)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	writeFile(t, path, "[server]\nseed = 12345\n")

	err := ValidateFile(path)
	if err == nil {
		t.Fatal("ValidateFile succeeded, want error")
	}
	if !strings.Contains(err.Error(), `unknown mc-server.toml key "server.seed"`) {
		t.Fatalf("ValidateFile error = %v, want unknown key", err)
	}
}

func TestBackupConfigKeysRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	updates := map[string]string{
		"backup.enabled":                 "false",
		"backup.directory":               "server-backups",
		"backup.retention_count":         "7",
		"backup.retention_days":          "30",
		"backup.retention_max_bytes":     "1073741824",
		"backup.daily_time":              "23:15",
		"backup.timezone":                "UTC",
		"backup.quiesce_timeout_seconds": "45",
		"backup.include_mc_server_toml":  "false",
	}
	for key, value := range updates {
		if err := Set(path, key, value); err != nil {
			t.Fatalf("Set(%s, %s) failed: %v", key, value, err)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Backup.Enabled || cfg.Backup.Directory != "server-backups" || cfg.Backup.RetentionCount != 7 ||
		cfg.Backup.RetentionDays != 30 || cfg.Backup.RetentionMaxBytes != 1073741824 ||
		cfg.Backup.DailyTime != "23:15" || cfg.Backup.Timezone != "UTC" ||
		cfg.Backup.QuiesceTimeoutSeconds != 45 || cfg.Backup.IncludeMCServerTOML {
		t.Fatalf("unexpected backup config: %+v", cfg.Backup)
	}
	value, err := Get(path, "backup.retention_count")
	if err != nil {
		t.Fatalf("Get backup.retention_count failed: %v", err)
	}
	if value != 7 {
		t.Fatalf("backup.retention_count = %v, want 7", value)
	}
	assertFileContent(t, path, Render(cfg))
}

func TestBackupConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "absolute directory", edit: func(c *Config) { c.Backup.Directory = "/tmp/backups" }, want: "backup.directory"},
		{name: "unclean directory", edit: func(c *Config) { c.Backup.Directory = "backups/../backups" }, want: "clean relative"},
		{name: "data root", edit: func(c *Config) { c.Backup.Directory = "data" }, want: "data root"},
		{name: "inside data subtree", edit: func(c *Config) { c.Backup.Directory = "data/backups" }, want: "protected path data"},
		{name: "inside game data", edit: func(c *Config) { c.Backup.Directory = "data/minecraft/backups" }, want: "protected path data/minecraft"},
		{name: "inside bot runtime", edit: func(c *Config) { c.Backup.Directory = "data/mcbot/backups" }, want: "protected path data/mcbot"},
		{name: "inside git metadata", edit: func(c *Config) { c.Backup.Directory = ".git/backups" }, want: "protected path .git"},
		{name: "inside omx metadata", edit: func(c *Config) { c.Backup.Directory = ".omx/backups" }, want: "protected path .omx"},
		{name: "zero retention", edit: func(c *Config) { c.Backup.RetentionCount = 0 }, want: "backup.retention_count"},
		{name: "negative retention days", edit: func(c *Config) { c.Backup.RetentionDays = -1 }, want: "backup.retention_days"},
		{name: "negative bytes", edit: func(c *Config) { c.Backup.RetentionMaxBytes = -1 }, want: "backup.retention_max_bytes"},
		{name: "bad daily time", edit: func(c *Config) { c.Backup.DailyTime = "4:00" }, want: "backup.daily_time"},
		{name: "bad timezone", edit: func(c *Config) { c.Backup.Timezone = "Not/AZone" }, want: "backup.timezone"},
		{name: "low quiesce timeout", edit: func(c *Config) { c.Backup.QuiesceTimeoutSeconds = 4 }, want: "backup.quiesce_timeout_seconds"},
		{name: "high quiesce timeout", edit: func(c *Config) { c.Backup.QuiesceTimeoutSeconds = 301 }, want: "backup.quiesce_timeout_seconds"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.edit(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	if got := readFile(t, path); got != want {
		t.Fatalf("%s =\n%s\nwant=\n%s", path, got, want)
	}
}

func TestWriteCanReplaceExistingFileRepeatedlyPreservesLatestContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	first := Defaults()
	first.Server.MOTD = "FIRST"
	second := Defaults()
	second.Server.MOTD = "SECOND"
	second.Container.UID = 1234

	if err := Write(path, first); err != nil {
		t.Fatalf("Write(first) failed: %v", err)
	}
	if err := Write(path, second); err != nil {
		t.Fatalf("Write(second) failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Server.MOTD != second.Server.MOTD || loaded.Container.UID != second.Container.UID {
		t.Fatalf("loaded config = %+v, want %+v", loaded, second)
	}
	got := readFile(t, path)
	if got != Render(second) {
		t.Fatalf("config file =\n%s\nwant=\n%s", got, Render(second))
	}
	if strings.Contains(got, "FIRST") {
		t.Fatalf("config file still contains stale MOTD: %q", got)
	}
	assertNoMatches(t, filepath.Join(filepath.Dir(path), ".mc-server-*.toml.tmp"))
}

func assertNoMatches(t *testing.T, pattern string) {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Glob(%q) failed: %v", pattern, err)
	}
	if len(matches) != 0 {
		t.Fatalf("unexpected matches for %q: %v", pattern, matches)
	}
}
