package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/server"
	"github.com/gantoine/muxer/relay/internal/tailnet"
)

const (
	defaultListenAddress = "127.0.0.1:8765"
	launchdLabel         = "com.gantoine.muxer.relay"
	legacyLaunchdLabel   = "dev.cmuxandroid.relay"
	legacyStateDirName   = "cmux-android-relay"
)

type paths struct {
	dir          string
	devices      string
	exposedPorts string
	admin        string
	plist        string
	log          string
	postHogCloud string
	cloudMirror  string
}

func statePaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	dir := os.Getenv("MUXER_STATE_DIR")
	if dir == "" {
		dir = filepath.Join(home, "Library", "Application Support", "muxer")
	}
	admin, err := adminSocketPath(dir)
	if err != nil {
		return paths{}, err
	}
	return paths{
		dir:          dir,
		devices:      filepath.Join(dir, "devices.json"),
		exposedPorts: filepath.Join(dir, "exposed-ports.json"),
		postHogCloud: filepath.Join(dir, "posthog-cloud.json"),
		cloudMirror:  filepath.Join(dir, "posthog-cloud-workspaces.json"),
		admin:        admin,
		plist:        filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		log:          filepath.Join(home, "Library", "Logs", "muxer.log"),
	}, nil
}

const maxUnixSocketPath = 100

func adminSocketPath(stateDir string) (string, error) {
	preferred := filepath.Join(stateDir, "admin.sock")
	if len(preferred) <= maxUnixSocketPath {
		return preferred, nil
	}
	privateDir := filepath.Join("/tmp", "muxer-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(privateDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(privateDir)
	if err != nil {
		return "", err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || int(owner.Uid) != os.Getuid() {
		return "", fmt.Errorf("%s must be a directory owned by you with mode 0700", privateDir)
	}
	digest := sha256.Sum256([]byte(stateDir))
	return filepath.Join(privateDir, hex.EncodeToString(digest[:4])+".sock"), nil
}

func defaultMacName() string {
	if output, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
		if name := strings.TrimSpace(string(output)); name != "" {
			return fixPossessive(name)
		}
	}
	hostname, _ := os.Hostname()
	return strings.TrimSuffix(hostname, ".local")
}

func fixPossessive(name string) string {
	for _, apostrophe := range []string{"'", "\u2019"} {
		name = strings.ReplaceAll(name, "s"+apostrophe+"s ", "s"+apostrophe+" ")
	}
	return name
}

func listenPort(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "8765"
	}
	return port
}

func tailscaleRelayURL(listen string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	self, err := tailnet.CLI{}.Self(ctx)
	if errors.Is(err, tailnet.ErrNotRunning) {
		return "", errors.New("could not read `tailscale status`; is Tailscale running and logged in?")
	}
	if err != nil {
		return "", err
	}
	return "https://" + self.DNSName + ":" + listenPort(listen), nil
}

func relayPort(listen string) int {
	port, _ := strconv.Atoi(listenPort(listen))
	return port
}

func tailscaleServeCommand(listen string) string {
	port := listenPort(listen)
	return fmt.Sprintf("tailscale serve --bg --https=%s http://127.0.0.1:%s", port, port)
}

func runInstall(args []string) error {
	listen, macName, err := serveFlags("install", args)
	if err != nil {
		return err
	}
	p, err := statePaths()
	if err != nil {
		return err
	}
	if err := retireLegacyRelay(p); err != nil {
		return err
	}
	executable, err := stableExecutablePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.plist), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.log), 0o755); err != nil {
		return err
	}
	command := []string{executable, "serve", "--listen", listen}
	if macName != "" {
		command = append(command, "--name", macName)
	}
	plist := launchdPlist(command, p.log)
	if err := os.WriteFile(p.plist, []byte(plist), 0o644); err != nil {
		return err
	}
	bootout(launchdLabel)
	if err := bootstrap(p.plist); err != nil {
		return err
	}
	fmt.Println("The relay now runs in the background and starts when you log in.")
	fmt.Println("Log file:", p.log)
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. Let the phone reach it over HTTPS, for example with Tailscale:")
	fmt.Println("       " + tailscaleServeCommand(listen))
	fmt.Println("     For a VPN, your local network, or Cloudflare Tunnel, see the README.")
	fmt.Println("  2. Check the setup:  muxer doctor")
	fmt.Println("  3. Pair your phone:  muxer pair  (add --url when you don't use Tailscale)")
	return nil
}

func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func bootout(label string) {
	_ = exec.Command("launchctl", "bootout", launchdDomain()+"/"+label).Run()
}

const (
	bootstrapAttempts = 10
	bootstrapRetry    = time.Second
)

func bootstrap(plist string) error {
	var output []byte
	var err error
	for attempt := 1; attempt <= bootstrapAttempts; attempt++ {
		if output, err = exec.Command("launchctl", "bootstrap", launchdDomain(), plist).CombinedOutput(); err == nil {
			return nil
		}
		if attempt < bootstrapAttempts {
			time.Sleep(bootstrapRetry)
		}
	}
	return fmt.Errorf("launchctl bootstrap failed: %s", strings.TrimSpace(string(output)))
}

func retireLegacyRelay(p paths) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyPlist := filepath.Join(home, "Library", "LaunchAgents", legacyLaunchdLabel+".plist")
	if _, err := os.Stat(legacyPlist); err == nil {
		bootout(legacyLaunchdLabel)
		if err := os.Remove(legacyPlist); err != nil {
			return err
		}
		fmt.Println("Stopped the old cmux-relay background relay and removed it from login items.")
	}
	if os.Getenv("MUXER_STATE_DIR") != "" {
		return nil
	}
	return adoptLegacyStateDir(filepath.Join(home, "Library", "Application Support", legacyStateDirName), p.dir)
}

func adoptLegacyStateDir(legacyDir, dir string) error {
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := os.Stat(legacyDir); err != nil {
		return nil
	}
	if err := os.Rename(legacyDir, dir); err != nil {
		return err
	}
	fmt.Println("Moved paired phones from", legacyDir, "to", dir)
	return nil
}

func stableExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	onPath, _ := exec.LookPath("muxer")
	return preferStableLink(executable, onPath), nil
}

func preferStableLink(executable, onPath string) string {
	running, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return executable
	}
	if onPath == "" {
		return running
	}
	if target, err := filepath.EvalSymlinks(onPath); err == nil && target == running {
		if absolute, err := filepath.Abs(onPath); err == nil {
			return absolute
		}
	}
	return running
}

func runUninstall() error {
	p, err := statePaths()
	if err != nil {
		return err
	}
	bootout(launchdLabel)
	if err := os.Remove(p.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("The relay is stopped and removed from login items. Paired phones are kept in", p.devices)
	return nil
}

func launchdPlist(arguments []string, logPath string) string {
	var programArguments strings.Builder
	for _, argument := range arguments {
		programArguments.WriteString("\t\t<string>" + xmlEscape(argument) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + programArguments.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
</dict>
</plist>
`
}

func xmlEscape(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func runDoctor(args []string) error {
	listen, _, err := serveFlags("doctor", args)
	if err != nil {
		return err
	}
	p, err := statePaths()
	if err != nil {
		return err
	}
	problems := 0
	check := func(ok bool, good, bad string) {
		if ok {
			fmt.Println("  ✓ " + good)
			return
		}
		problems++
		fmt.Println("  ✗ " + bad)
	}
	note := func(ok bool, good, other string) {
		if ok {
			fmt.Println("  ✓ " + good)
			return
		}
		fmt.Println("  · " + other)
	}

	fmt.Println("cmux")
	socketPath := cmux.DefaultSocketPath()
	_, statErr := os.Stat(socketPath)
	check(statErr == nil, "socket found at "+socketPath, "no cmux socket at "+socketPath+"; is cmux running?")

	client := cmux.NewClient(socketPath, cmux.DefaultPasswordSource())
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identity, err := client.Identify(ctx)
	check(err == nil, "connected to cmux "+identity.Version, fmt.Sprintf("could not talk to cmux: %v", err))

	if err == nil {
		capabilities, _ := client.Capabilities(ctx)
		mode := capabilities.AccessMode
		launchdAllowed := mode == "password" || mode == "automation" || mode == "allowAll"
		check(launchdAllowed,
			"socket access mode is "+mode+", which lets the background relay connect",
			"socket access mode is "+mode+"; the background relay will be refused.\n"+
				"      In cmux, open Settings > Automation and set socket access to Password,\n"+
				"      or set \"automation\": {\"socketControlMode\": \"password\"} in ~/.config/cmux/cmux.json")
	}

	fmt.Println("Relay")
	_, adminErr := server.CallAdmin(p.admin, server.AdminRequest{Command: "devices"})
	check(adminErr == nil, "relay is running", "relay is not running; run `muxer install`")

	fmt.Println("Tailscale (optional)")
	binary, tailscaleErr := tailnet.Binary()
	note(tailscaleErr == nil, "tailscale found",
		"tailscale is not installed; make sure the phone reaches the relay over HTTPS another way")
	if tailscaleErr == nil {
		output, err := exec.Command(binary, "serve", "status", "--json").Output()
		target := "127.0.0.1:" + listenPort(listen)
		note(err == nil && bytes.Contains(output, []byte(target)),
			"tailscale serve forwards to "+target,
			"tailscale serve is not forwarding to the relay; to use Tailscale, run: "+tailscaleServeCommand(listen))
	}

	fmt.Println()
	if problems > 0 {
		return fmt.Errorf("%d problem(s) found", problems)
	}
	fmt.Println("Everything looks good. Run `muxer pair` to add a phone.")
	return nil
}
