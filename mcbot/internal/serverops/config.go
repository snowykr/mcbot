package serverops

import (
	"fmt"
	"maps"

	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
)

type Sources struct {
	EnvFile    string
	ConfigFile string
}

type OperationalConfig struct {
	Env      envfile.File
	MCConfig mcconfig.Config
}

func LoadSources(sources Sources) (OperationalConfig, error) {
	env, err := envfile.Load(sources.EnvFile)
	if err != nil {
		return OperationalConfig{}, err
	}
	mcCfg, err := mcconfig.Load(sources.ConfigFile)
	if err != nil {
		return OperationalConfig{}, err
	}
	return OperationalConfig{Env: env, MCConfig: mcCfg}, nil
}

func ComposeEnvironment(cfg OperationalConfig) map[string]string {
	env := make(map[string]string, len(cfg.Env.Values)+12)
	maps.Copy(env, cfg.Env.Values)
	maps.Copy(env, cfg.MCConfig.ComposeEnvironment())
	return env
}

func ValidateEnvKey(key string) error {
	if mcconfig.IsOwned(key) {
		return fmt.Errorf("%s is owned by mc-server.toml", key)
	}
	return envfile.ValidateOwnedKey(key)
}

func ValidateConfigKey(key string) error {
	if envfile.IsOwned(key) {
		return fmt.Errorf("%s is owned by .env", key)
	}
	return mcconfig.ValidateOwnedKey(key)
}

// ServerCommandsRequireDiscordToken is deliberately false: server operations read files and Docker state only.
func ServerCommandsRequireDiscordToken() bool {
	return false
}
