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
