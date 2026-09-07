package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	TradebotLocalStorageRoot  string `env:"TRADEBOT_LOCAL_STORAGE_ROOT" default:"$HOME/tradebot"`
	TradebotCredentialsDBPath string `env:"TRADEBOT_CREDENTIALS_SQLITE_PATH" default:"$HOME/tradebot/credentials.sqlite3"`
}

// Load returns the local-storage configuration used by the command-line tools.
// Environment variables override the per-user defaults. When only the storage
// root is overridden, the credentials database follows that root.
func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	settings := viper.New()
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.SetDefault("tradebot.local_storage_root", structDefault("TradebotLocalStorageRoot"))
	settings.SetDefault("tradebot.credentials_sqlite_path", structDefault("TradebotCredentialsDBPath"))
	if err := settings.BindEnv("tradebot.local_storage_root", "TRADEBOT_LOCAL_STORAGE_ROOT"); err != nil {
		return Config{}, err
	}
	if err := settings.BindEnv("tradebot.credentials_sqlite_path", "TRADEBOT_CREDENTIALS_SQLITE_PATH"); err != nil {
		return Config{}, err
	}

	root := expandHome(settings.GetString("tradebot.local_storage_root"), home)
	rootOverridden := false
	if value := strings.TrimSpace(os.Getenv("TRADEBOT_LOCAL_STORAGE_ROOT")); value != "" {
		root = expandHome(value, home)
		rootOverridden = true
	}

	databasePath := expandHome(settings.GetString("tradebot.credentials_sqlite_path"), home)
	if value := strings.TrimSpace(os.Getenv("TRADEBOT_CREDENTIALS_SQLITE_PATH")); value != "" {
		databasePath = expandHome(value, home)
	} else if rootOverridden {
		databasePath = filepath.Join(root, "credentials.sqlite3")
	}

	return Config{
		TradebotLocalStorageRoot:  filepath.Clean(root),
		TradebotCredentialsDBPath: filepath.Clean(databasePath),
	}, nil
}

func structDefault(fieldName string) string {
	field, ok := reflect.TypeOf(Config{}).FieldByName(fieldName)
	if !ok {
		return ""
	}
	return field.Tag.Get("default")
}

func expandHome(value, home string) string {
	if value == "$HOME" {
		return home
	}
	if strings.HasPrefix(value, "$HOME/") {
		return filepath.Join(home, strings.TrimPrefix(value, "$HOME/"))
	}
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	return value
}
