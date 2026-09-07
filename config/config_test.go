package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesTradebotHomeDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRADEBOT_LOCAL_STORAGE_ROOT", "")
	t.Setenv("TRADEBOT_CREDENTIALS_SQLITE_PATH", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	wantRoot := filepath.Join(home, "tradebot")
	if got.TradebotLocalStorageRoot != wantRoot {
		t.Fatalf("unexpected storage root: got %q want %q", got.TradebotLocalStorageRoot, wantRoot)
	}
	if got.TradebotCredentialsDBPath != filepath.Join(wantRoot, "credentials.sqlite3") {
		t.Fatalf("unexpected database path: got %q", got.TradebotCredentialsDBPath)
	}
}

func TestLoadEnvironmentOverridesExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRADEBOT_LOCAL_STORAGE_ROOT", "$HOME/custom")
	t.Setenv("TRADEBOT_CREDENTIALS_SQLITE_PATH", "~/state/users.sqlite3")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if got.TradebotLocalStorageRoot != filepath.Join(home, "custom") {
		t.Fatalf("unexpected overridden root: %q", got.TradebotLocalStorageRoot)
	}
	if got.TradebotCredentialsDBPath != filepath.Join(home, "state", "users.sqlite3") {
		t.Fatalf("unexpected overridden database path: %q", got.TradebotCredentialsDBPath)
	}
	if _, err := os.Stat(got.TradebotCredentialsDBPath); !os.IsNotExist(err) {
		t.Fatalf("config unexpectedly created the database: %v", err)
	}
}

func TestLoadRootOverrideDerivesDatabasePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRADEBOT_LOCAL_STORAGE_ROOT", filepath.Join(home, "custom"))
	t.Setenv("TRADEBOT_CREDENTIALS_SQLITE_PATH", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	want := filepath.Join(home, "custom", "credentials.sqlite3")
	if got.TradebotCredentialsDBPath != want {
		t.Fatalf("unexpected derived database path: got %q want %q", got.TradebotCredentialsDBPath, want)
	}
}
