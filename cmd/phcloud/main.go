package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gantoine/phcloud/internal/posthogcloud"
)

var version = "dev"

const usage = `phcloud manages PostHog Cloud runs from cmux.

Usage:
  phcloud                  open the run manager (meant for the cmux Dock)
  phcloud connect          save a personal API key
  phcloud claude-token     save a Claude token for runs on your Claude plan
  phcloud status           show the saved account
  phcloud disconnect       forget the saved account
  phcloud new --repo owner/name [--agent claude|codex] [--claude-plan] "prompt"
  phcloud attach TASK RUN  follow one run in this terminal
  phcloud dock install     pin the run manager in the cmux Dock
  phcloud version`

type paths struct {
	dir        string
	config     string
	workspaces string
	prefs      string
}

func statePaths() (paths, error) {
	dir := os.Getenv("PHCLOUD_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return paths{}, err
		}
		dir = filepath.Join(home, ".config", "phcloud")
	}
	return paths{dir: dir, config: filepath.Join(dir, "config.json"), workspaces: filepath.Join(dir, "workspaces.json"), prefs: filepath.Join(dir, "prefs.json")}, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "phcloud:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	paths, err := statePaths()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return runManager(paths)
	}
	switch args[0] {
	case "connect":
		return connect(paths, args[1:])
	case "disconnect":
		if err := posthogcloud.RemoveConfig(paths.config); err != nil {
			return err
		}
		fmt.Println("Disconnected.")
		return nil
	case "status":
		return status(paths)
	case "claude-token":
		return saveClaudeToken(paths, args[1:])
	case "new":
		return startRunFromCLI(paths, args[1:])
	case "attach":
		return attach(paths, args[1:])
	case "dock":
		return dock(args[1:])
	case "version", "--version":
		fmt.Println("phcloud", version)
		return nil
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	}
	return errors.New("unknown command " + args[0] + "\n\n" + usage)
}

func loadConfig(paths paths) (posthogcloud.Config, error) {
	config, ok, err := posthogcloud.LoadConfig(paths.config)
	if err != nil {
		return config, err
	}
	if !ok {
		return config, errors.New("not connected; run `phcloud connect`")
	}
	return config, nil
}
