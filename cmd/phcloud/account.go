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

	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const (
	apiKeyEnv            = "POSTHOG_PERSONAL_API_KEY"
	claudeTokenEnv       = "CLAUDE_CODE_OAUTH_TOKEN"
	maxClaudeTokenLength = 4096
)

func connect(paths paths, args []string) error {
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	host := flags.String("host", posthogcloud.DefaultHost, "your PostHog address, such as https://eu.posthog.com")
	project := flags.Int("project", 0, "the project whose tasks to show (default: the key owner's current project)")
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
	user, err := posthogcloud.NewClient(config, nil).Me(ctx)
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
	if previous, ok, _ := posthogcloud.LoadConfig(paths.config); ok && previous.UserID == config.UserID {
		config.ClaudeToken = previous.ClaudeToken
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		return err
	}
	if err := posthogcloud.SaveConfig(paths.config, config); err != nil {
		return err
	}
	fmt.Printf("Connected as %s, project %d.\n", config.Email, config.ProjectID)
	return nil
}

func status(paths paths) error {
	config, ok, err := posthogcloud.LoadConfig(paths.config)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Not connected. Run `phcloud connect`.")
		return nil
	}
	fmt.Printf("Connected to %s, project %d, as %s.\n", config.Host, config.ProjectID, config.Email)
	if config.ClaudeToken != "" {
		fmt.Println("A Claude token is saved for runs on your Claude plan.")
	}
	return nil
}

func saveClaudeToken(paths paths, args []string) error {
	flags := flag.NewFlagSet("claude-token", flag.ContinueOnError)
	forget := flags.Bool("clear", false, "forget the saved Claude token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := loadConfig(paths)
	if err != nil {
		return err
	}
	if *forget {
		config.ClaudeToken = ""
		if err := posthogcloud.SaveConfig(paths.config, config); err != nil {
			return err
		}
		fmt.Println("Forgot the Claude token.")
		return nil
	}
	token := strings.TrimSpace(os.Getenv(claudeTokenEnv))
	if token == "" {
		fmt.Println("Run `claude setup-token` in another terminal and copy the token it prints.")
		if token, err = readSecret("Paste it here: "); err != nil {
			return err
		}
	}
	if len(token) > maxClaudeTokenLength {
		return errors.New("that token is longer than PostHog accepts")
	}
	config.ClaudeToken = token
	if err := posthogcloud.SaveConfig(paths.config, config); err != nil {
		return err
	}
	fmt.Println("Saved. The run manager hands it to runs on your Claude plan when they ask for it.")
	return nil
}

func readAPIKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv(apiKeyEnv)); key != "" {
		return key, nil
	}
	fmt.Println("Create a personal API key in PostHog under Settings › Personal API keys,")
	fmt.Println("with the task:read, task:write, user:read and integration:read scopes.")
	return readSecret("Paste it here: ")
}

func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	var raw []byte
	var err error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
	} else {
		var line string
		line, err = bufio.NewReader(os.Stdin).ReadString('\n')
		raw = []byte(line)
		if line != "" {
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", errors.New("nothing was pasted")
	}
	return secret, nil
}
