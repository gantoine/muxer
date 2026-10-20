package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
	"github.com/gantoine/muxer/relay/internal/server"
)

const (
	postHogAPIKeyEnv     = "POSTHOG_PERSONAL_API_KEY"
	claudeTokenEnv       = "CLAUDE_CODE_OAUTH_TOKEN"
	maxClaudeTokenLength = 4096
)

func runPostHogCloud(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: muxer posthog-cloud connect|disconnect|status|claude-token|new|attach")
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
		if config.ClaudeToken != "" {
			fmt.Println("A Claude token is saved for runs on your Claude plan.")
		}
		return nil
	case "claude-token":
		return saveClaudeToken(paths, args[1:])
	case "attach":
		return attachPostHogCloudRun(paths, args[1:])
	case "new":
		return startPostHogCloudRun(paths, args[1:])
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
	if previous, ok, _ := posthogcloud.LoadConfig(paths.postHogCloud); ok && previous.UserID == config.UserID {
		config.ClaudeToken = previous.ClaudeToken
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

func startPostHogCloudRun(paths paths, args []string) error {
	flags := flag.NewFlagSet("posthog-cloud new", flag.ContinueOnError)
	repository := flags.String("repo", "", "the GitHub repository to work in, as owner/name")
	agent := flags.String("agent", posthogcloud.AgentClaude, "claude or codex")
	claudePlan := flags.Bool("claude-plan", false, "bill Claude to your own Claude plan instead of PostHog (needs `muxer posthog-cloud claude-token`)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *repository == "" || prompt == "" {
		return errors.New(`usage: muxer posthog-cloud new --repo owner/name [--agent claude|codex] [--claude-plan] "what to do"`)
	}
	config, ok, err := posthogcloud.LoadConfig(paths.postHogCloud)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("connect PostHog Cloud first with `muxer posthog-cloud connect`")
	}
	if *claudePlan && config.ClaudeToken == "" {
		return errors.New("save a Claude token first with `muxer posthog-cloud claude-token`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := posthogcloud.NewClient(config, nil)
	wanted := strings.ToLower(strings.TrimSpace(*repository))
	_, short, _ := strings.Cut(wanted, "/")
	found, err := client.Repositories(ctx, cmp.Or(short, wanted))
	if err != nil {
		return fmt.Errorf("list your repositories: %w", err)
	}
	var chosen *posthogcloud.Repository
	for i := range found {
		if found[i].Name == wanted {
			chosen = &found[i]
		}
	}
	if chosen == nil {
		return fmt.Errorf("PostHog can't reach %s through your GitHub integrations", wanted)
	}
	task, run, err := client.StartRun(ctx, posthogcloud.StartRequest{Repository: *chosen, Agent: *agent, ClaudePlan: *claudePlan, Prompt: prompt})
	if err != nil {
		return err
	}
	fmt.Printf("Started %s.\n", strings.TrimSuffix(config.Host, "/")+"/project/"+strconv.Itoa(config.ProjectID)+"/tasks/"+task.ID)
	if _, err := server.CallAdmin(paths.admin, server.AdminRequest{Command: "posthog_cloud_sync"}); err != nil {
		fmt.Println("The relay isn't running, so no cmux workspace opened. Watch it with:")
		fmt.Printf("  muxer posthog-cloud attach --task %s --run %s\n", task.ID, run.ID)
		return nil
	}
	fmt.Println("Its workspace is open in cmux.")
	return nil
}

func saveClaudeToken(paths paths, args []string) error {
	flags := flag.NewFlagSet("posthog-cloud claude-token", flag.ContinueOnError)
	forget := flags.Bool("clear", false, "forget the saved Claude token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, ok, err := posthogcloud.LoadConfig(paths.postHogCloud)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("connect PostHog Cloud first with `muxer posthog-cloud connect`")
	}
	if *forget {
		config.ClaudeToken = ""
		if err := posthogcloud.SaveConfig(paths.postHogCloud, config); err != nil {
			return err
		}
		fmt.Println("Forgot the Claude token. Runs on your Claude plan need another client to start them now.")
		return nil
	}
	token := strings.TrimSpace(os.Getenv(claudeTokenEnv))
	if token == "" {
		fmt.Println("Run `claude setup-token` in another terminal and copy the token it prints.")
		token, err = readSecret("Paste it here: ")
		if err != nil {
			return err
		}
	}
	if len(token) > maxClaudeTokenLength {
		return errors.New("that token is longer than PostHog accepts")
	}
	config.ClaudeToken = token
	if err := posthogcloud.SaveConfig(paths.postHogCloud, config); err != nil {
		return err
	}
	fmt.Println("Saved. The relay hands it to cloud runs you start on your Claude plan when they ask for it.")
	return nil
}

func readAPIKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv(postHogAPIKeyEnv)); key != "" {
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
		if errors.Is(err, os.ErrClosed) || (err != nil && line != "") {
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
