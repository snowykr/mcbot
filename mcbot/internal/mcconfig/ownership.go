package mcconfig

import "fmt"

var ownedKeys = map[string]struct{}{
	"server.version":                 {},
	"server.type":                    {},
	"server.difficulty":              {},
	"server.memory":                  {},
	"server.init_memory":             {},
	"server.motd":                    {},
	"server.view_distance":           {},
	"server.simulation_distance":     {},
	"container.restart_policy":       {},
	"container.port_publish":         {},
	"container.uid":                  {},
	"container.gid":                  {},
	"backup.enabled":                 {},
	"backup.directory":               {},
	"backup.retention_count":         {},
	"backup.retention_days":          {},
	"backup.retention_max_bytes":     {},
	"backup.daily_time":              {},
	"backup.timezone":                {},
	"backup.quiesce_timeout_seconds": {},
	"backup.include_mc_server_toml":  {},
}

// IsOwned reports whether path belongs to mc-server.toml in the v1 operational contract.
func IsOwned(path string) bool {
	_, ok := ownedKeys[path]
	return ok
}

// ValidateOwnedKey rejects .env-owned keys, secrets, and any unknown TOML path.
func ValidateOwnedKey(path string) error {
	if IsOwned(path) {
		return nil
	}
	return fmt.Errorf("%s is not owned by mc-server.toml", path)
}

func OwnedKeys() []string {
	keys := make([]string, 0, len(ownedKeys))
	for key := range ownedKeys {
		keys = append(keys, key)
	}
	return keys
}
