package envfile

import "fmt"

var ownedKeys = map[string]struct{}{
	"DISCORD_TOKEN":                    {},
	"MCBOT_TRUSTED_GUILD_ID":           {},
	"EMBED_CHANNEL_ID":                 {},
	"MCBOT_ROLE_NAME":                  {},
	"READY_TIMEOUT_SECONDS":            {},
	"STOP_TIMEOUT_SECONDS":             {},
	"SERVER_OPERATION_TIMEOUT_SECONDS": {},
	"EMBED_UPDATE_TIMEOUT_SECONDS":     {},
	"AUTO_RECOVER_ENABLED":             {},
	"AUTO_RECOVER_INTERVAL_SECONDS":    {},
	"MAX_AUTO_RECOVER_ATTEMPTS":        {},
	"CRASH_DETECTION_INTERVAL_SECONDS": {},
	"MAX_INSPECT_FAILURE_ATTEMPTS":     {},
	"MCBOT_DEBUG":                      {},
	"MC_JOIN_LOG_PATTERN":              {},
	"MC_LEAVE_LOG_PATTERN":             {},
	"MC_CONTAINER_NAME":                {},
	"RCON_HOST":                        {},
	"RCON_PORT":                        {},
	"RCON_PASSWORD":                    {},
	"RCON_TIMEOUT_SECONDS":             {},
	"ENABLE_RCON":                      {},
	"RCON_CMDS_STARTUP":                {},
}

// IsOwned reports whether key belongs to the .env domain in the v1 operational contract.
func IsOwned(key string) bool {
	_, ok := ownedKeys[key]
	return ok
}

// ValidateOwnedKey rejects keys that env commands are not allowed to manage.
func ValidateOwnedKey(key string) error {
	if IsOwned(key) {
		return nil
	}
	return fmt.Errorf("%s is not owned by .env", key)
}

func OwnedKeys() []string {
	keys := make([]string, 0, len(ownedKeys))
	for key := range ownedKeys {
		keys = append(keys, key)
	}
	return keys
}
