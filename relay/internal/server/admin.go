package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"github.com/gantoine/muxer/relay/internal/devices"
)

type AdminRequest struct {
	Command  string `json:"command"`
	DeviceID string `json:"device_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
}

type AdminResponse struct {
	Error     string           `json:"error,omitempty"`
	Code      string           `json:"code,omitempty"`
	ExpiresAt time.Time        `json:"expires_at,omitzero"`
	Devices   []devices.Device `json:"devices,omitempty"`
}

type AdminHooks struct {
	SyncPostHogCloud   func(context.Context)
	AttachPostHogCloud func(ctx context.Context, conn net.Conn, taskID, runID string)
}

func ServeAdmin(ctx context.Context, path string, store *devices.Store, hooks AdminHooks) error {
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	context.AfterFunc(ctx, func() { _ = listener.Close() })
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleAdmin(ctx, conn, store, hooks)
	}
}

func handleAdmin(ctx context.Context, conn net.Conn, store *devices.Store, hooks AdminHooks) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	var request AdminRequest
	if err != nil || json.Unmarshal(line, &request) != nil {
		return
	}
	if request.Command == "posthog_cloud_attach" && hooks.AttachPostHogCloud != nil && request.RunID != "" {
		_ = conn.SetDeadline(time.Time{})
		hooks.AttachPostHogCloud(ctx, &bufferedConn{Conn: conn, reader: reader}, request.TaskID, request.RunID)
		return
	}
	var response AdminResponse
	switch request.Command {
	case "pair":
		code, expiresAt, err := store.NewPairingCode()
		if err != nil {
			response.Error = err.Error()
		}
		response.Code, response.ExpiresAt = code, expiresAt
	case "devices":
		response.Devices = store.List()
		for index := range response.Devices {
			response.Devices[index].TokenHash = ""
		}
	case "posthog_cloud_sync":
		if hooks.SyncPostHogCloud == nil {
			response.Error = "this relay can't open PostHog Cloud workspaces"
			break
		}
		syncCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		hooks.SyncPostHogCloud(syncCtx)
		cancel()
	case "revoke":
		if err := store.Revoke(request.DeviceID); err != nil {
			response.Error = err.Error()
		}
	default:
		response.Error = "unknown command"
	}
	_ = json.NewEncoder(conn).Encode(response)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func DialAdmin(path string, request AdminRequest) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return nil, errors.New("the relay is not running")
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func CallAdmin(path string, request AdminRequest) (AdminResponse, error) {
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return AdminResponse{}, errors.New("the relay is not running; start it with `muxer install` or `muxer serve`")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return AdminResponse{}, err
	}
	var response AdminResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return AdminResponse{}, err
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}
