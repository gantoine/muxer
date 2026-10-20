package posthogcloud

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Config struct {
	Host      string `json:"host"`
	ProjectID int    `json:"project_id"`
	APIKey    string `json:"api_key"`
	UserID    int    `json:"user_id"`
	Email     string `json:"email,omitempty"`
}

func LoadConfig(path string) (Config, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return Config{}, false, err
	}
	if config.APIKey == "" || config.ProjectID == 0 {
		return Config{}, false, nil
	}
	if config.Host == "" {
		config.Host = DefaultHost
	}
	return config, true, nil
}

func SaveConfig(path string, config Config) error {
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".posthog-cloud-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func RemoveConfig(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
