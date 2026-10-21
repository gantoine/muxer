package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const dockControlID = "cloud-runs"

func dockConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cmux", "dock.json"), nil
}

func dock(args []string) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		return errors.New("usage: phc dock install|uninstall")
	}
	path, err := dockConfigPath()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	install := args[0] == "install"
	changed, err := updateDockConfig(path, self, install)
	if err != nil {
		return err
	}
	if !install {
		if changed {
			fmt.Println("Removed Cloud runs from", path+". Close its Dock tab in cmux if it's still open.")
		} else {
			fmt.Println("Cloud runs wasn't in", path+".")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := newCmux("").run(ctx, "new-surface", "--placement", "dock", "--command", shellQuote(self)); err != nil {
		fmt.Println("Saved to", path+", but cmux couldn't open it now:", err)
		return nil
	}
	fmt.Println("Cloud runs is open in the cmux Dock and saved to", path+".")
	return nil
}

func updateDockConfig(path, executable string, install bool) (bool, error) {
	config := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &config); err != nil {
			return false, fmt.Errorf("%s isn't valid JSON: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	controls, _ := config["controls"].([]any)
	var kept []any
	found := false
	for _, control := range controls {
		if entry, ok := control.(map[string]any); ok && entry["id"] == dockControlID {
			found = true
			continue
		}
		kept = append(kept, control)
	}
	if !install && !found {
		return false, nil
	}
	if install {
		kept = append(kept, map[string]any{"id": dockControlID, "title": "Cloud runs", "command": shellQuote(executable)})
	}
	if kept == nil {
		kept = []any{}
	}
	config["controls"] = kept
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(encoded, '\n'), 0o644)
}
