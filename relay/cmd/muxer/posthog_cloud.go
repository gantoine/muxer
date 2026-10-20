package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const postHogAPIKeyEnv = "POSTHOG_PERSONAL_API_KEY"

func runPostHogCloud(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: muxer posthog-cloud connect|disconnect|status")
	}
	paths, err := statePaths()
	if err != nil {
		return err
	}
	switch args[0] {
	case "connect":
		return connectPostHogCloud(paths, args[1:])
	case "disconnect":
		if err := posthogcloud.RemoveConfig(paths.postHogCloud); err != nil {
			return err
		}
		fmt.Println("Disconnected. The app no longer shows PostHog Cloud runs from this computer.")
		return nil
	case "status":
		config, ok, err := posthogcloud.LoadConfig(paths.postHogCloud)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Not connected. Run `muxer posthog-cloud connect`.")
			return nil
		}
		fmt.Printf("Connected to %s, project %d, as %s.\n", config.Host, config.ProjectID, config.Email)
		return nil
	}
	return fmt.Errorf("unknown posthog-cloud command %q", args[0])
}

func connectPostHogCloud(paths paths, args []string) error {
	flags := flag.NewFlagSet("posthog-cloud connect", flag.ContinueOnError)
	host := flags.String("host", posthogcloud.DefaultHost, "your PostHog address, such as https://eu.posthog.com")
	project := flags.Int("project", 0, "the project ID whose tasks to show (default: the key owner's current project)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	key, err := readAPIKey()
	if err != nil {
		return err
	}
	config := posthogcloud.Config{Host: strings.TrimSuffix(*host, "/"), ProjectID: *project, APIKey: key}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := posthogcloud.NewClient(config, nil)
	user, err := client.Me(ctx)
	if err != nil {
		return fmt.Errorf("check the key: %w", err)
	}
	config.UserID, config.Email = user.ID, user.Email
	if config.ProjectID == 0 {
		config.ProjectID = user.Team.ID
	}
	if config.ProjectID == 0 {
		return errors.New("the key's owner has no current project; pass --project")
	}
	if _, err := posthogcloud.NewClient(config, nil).Tasks(ctx, 1); err != nil {
		return fmt.Errorf("read tasks in project %d: %w", config.ProjectID, err)
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		return err
	}
	if err := posthogcloud.SaveConfig(paths.postHogCloud, config); err != nil {
		return err
	}
	fmt.Printf("Connected as %s, project %d. Open Devices in the app to see your runs.\n", config.Email, config.ProjectID)
	return nil
}

func readAPIKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv(postHogAPIKeyEnv)); key != "" {
		return key, nil
	}
	fmt.Println("Create a personal API key in PostHog under Settings › Personal API keys,")
	fmt.Println("with the task:read, task:write and user:read scopes.")
	fmt.Print("Paste it here: ")
	var raw []byte
	var err error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
	} else {
		var line string
		line, err = bufio.NewReader(os.Stdin).ReadString('\n')
		raw = []byte(line)
		if errors.Is(err, os.ErrClosed) || (err != nil && line != "") {
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", errors.New("no API key given")
	}
	return key, nil
}
