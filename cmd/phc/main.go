package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gantoine/phc/internal/posthogcloud"
)

var version = "dev"

const usage = `phc manages PostHog Cloud runs from cmux.

Usage:
  phc                      open the run manager (meant for the cmux Dock)
  phc connect              save a personal API key
  phc claude-token         save a Claude token for runs on your Claude plan
  phc status               show the saved account
  phc disconnect           forget the saved account
  phc new --repo owner/name [--agent claude|codex] [--claude-plan] "prompt"
  phc attach TASK RUN      follow one run in this terminal
  phc dock install         pin the run manager in the cmux Dock
  phc version`

type paths struct {
	dir        string
	config     string
	workspaces string
}

func statePaths() (paths, error) {
	dir := os.Getenv("PHC_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return paths{}, err
		}
		dir = filepath.Join(home, ".config", "phc")
	}
	return paths{dir: dir, config: filepath.Join(dir, "config.json"), workspaces: filepath.Join(dir, "workspaces.json")}, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "phc:", err)
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
		fmt.Println("phc", version)
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
		return config, errors.New("not connected; run `phc connect`")
	}
	return config, nil
}
