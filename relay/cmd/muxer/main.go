package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/gantoine/muxer/relay/internal/agents"
	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/devices"
	"github.com/gantoine/muxer/relay/internal/events"
	"github.com/gantoine/muxer/relay/internal/host"
	"github.com/gantoine/muxer/relay/internal/notify"
	"github.com/gantoine/muxer/relay/internal/server"
)

var version = "dev"

const usage = `muxer connects the Muxer Android app to cmux on this computer.

Usage:
  muxer install [--listen ADDR] [--name NAME]   Run the relay at login (launchd) and start it now
  muxer uninstall                               Stop the relay and remove it from login items
  muxer pair [--url URL] [--listen ADDR]        Show a QR code and pairing code for a new phone
  muxer devices                                 List paired phones
  muxer revoke DEVICE_ID                        Remove a paired phone
  muxer doctor                                  Check cmux, socket access, and the relay setup
  muxer serve [--listen ADDR] [--name NAME]     Run the relay in the foreground
  muxer version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	command, args := os.Args[1], os.Args[2:]
	var err error
	switch command {
	case "serve":
		err = runServe(args)
	case "pair":
		err = runPair(args)
	case "devices":
		err = runDevices()
	case "revoke":
		err = runRevoke(args)
	case "install":
		err = runInstall(args)
	case "uninstall":
		err = runUninstall()
	case "doctor":
		err = runDoctor(args)
	case "version", "--version":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serveFlags(name string, args []string) (listen, macName string, err error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.StringVar(&listen, "listen", defaultListenAddress, "address to listen on; keep it on loopback behind an HTTPS proxy such as tailscale serve")
	flags.StringVar(&macName, "name", "", "name the phone shows for this computer (default: the name cmux uses for it)")
	err = flags.Parse(args)
	return listen, macName, err
}

func runServe(args []string) error {
	listen, macName, err := serveFlags("serve", args)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	paths, err := statePaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		return err
	}
	store, err := devices.Open(paths.devices)
	if err != nil {
		return fmt.Errorf("read paired devices: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}

	client := cmux.NewDefaultClient(cmux.DefaultPasswordSource())
	defer client.Close()
	hub := notify.NewHub(client, logger)
	approvalTracker := approvals.NewTracker(client, logger)
	go approvalTracker.Run(ctx)
	agentTracker := agents.NewTracker(client, logger)
	go agentTracker.Run(ctx)
	adminErr := make(chan error, 1)
	go func() {
		if err := server.ServeAdmin(ctx, paths.admin, store); err != nil {
			adminErr <- fmt.Errorf("admin socket %s: %w", paths.admin, err)
			stop()
		}
	}()

	relay := server.New(client, store, hub, server.Options{MacName: macName, FallbackMacName: defaultMacName(), RelayVersion: version, Approvals: approvalTracker, Agents: agentTracker, HostUsage: host.Sample}, logger)
	categories := slices.Concat([]string{"notification"}, agents.EventCategories, server.WorkspaceEventCategories)
	go events.NewStream(client, logger, cmux.EventFilter{Categories: categories}, hub, approvalTracker, agentTracker, relay).Run(ctx)
	httpServer := &http.Server{
		Addr:              listen,
		Handler:           relay.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("relay listening", "address", listener.Addr().String(), "cmux_socket", client.SocketPath(), "version", version)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	serveErr := httpServer.Serve(listener)
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	relay.Close(closeCtx)
	cancelClose()
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	select {
	case err := <-adminErr:
		return err
	default:
		return nil
	}
}

func runPair(args []string) error {
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	relayURL := flags.String("url", "", "HTTPS address the phone uses to reach this relay (default: this computer's Tailscale name)")
	listen := flags.String("listen", defaultListenAddress, "address the relay listens on, if you changed it with install or serve")
	if err := flags.Parse(args); err != nil {
		return err
	}
	paths, err := statePaths()
	if err != nil {
		return err
	}
	if *relayURL == "" {
		*relayURL, err = tailscaleRelayURL(*listen)
		if err != nil {
			return fmt.Errorf("%w\nPass the address yourself with --url", err)
		}
	}
	response, err := server.CallAdmin(paths.admin, server.AdminRequest{Command: "pair"})
	if err != nil {
		return err
	}
	link := url.URL{Scheme: "muxer", Host: "pair", RawQuery: url.Values{"url": {*relayURL}, "code": {response.Code}}.Encode()}
	fmt.Println("Scan this with your phone's camera, or open the app and enter the details below.")
	fmt.Println()
	qrterminal.GenerateHalfBlock(link.String(), qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("  Address: %s\n", *relayURL)
	fmt.Printf("  Code:    %s-%s\n", response.Code[:4], response.Code[4:])
	fmt.Printf("  Expires: %s\n", response.ExpiresAt.Local().Format(time.Kitchen))
	return nil
}

func runDevices() error {
	paths, err := statePaths()
	if err != nil {
		return err
	}
	response, err := server.CallAdmin(paths.admin, server.AdminRequest{Command: "devices"})
	if err != nil {
		return err
	}
	if len(response.Devices) == 0 {
		fmt.Println("No paired phones. Run `muxer pair` to add one.")
		return nil
	}
	table := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tPAIRED\tLAST SEEN")
	for _, device := range response.Devices {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", device.ID, device.Name,
			device.PairedAt.Local().Format(time.DateTime), device.LastSeen.Local().Format(time.DateTime))
	}
	return table.Flush()
}

func runRevoke(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: muxer revoke DEVICE_ID")
	}
	paths, err := statePaths()
	if err != nil {
		return err
	}
	if _, err := server.CallAdmin(paths.admin, server.AdminRequest{Command: "revoke", DeviceID: args[0]}); err != nil {
		return err
	}
	fmt.Println("Removed. That phone is disconnected and must pair again.")
	return nil
}
