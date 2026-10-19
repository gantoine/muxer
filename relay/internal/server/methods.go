package server

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

type methodSupport struct {
	mu      sync.Mutex
	listed  map[string]bool
	missing map[string]bool
}

func (m *methodSupport) load(ctx context.Context, api Cmux) {
	methods, err := api.Methods(ctx)
	if err != nil && !cmux.IsMethodNotFound(err) {
		return
	}
	var listed map[string]bool
	if err == nil && len(methods) > 0 {
		listed = make(map[string]bool, len(methods))
		for _, method := range methods {
			listed[method] = true
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listed = listed
	m.missing = nil
}

func (m *methodSupport) supports(method string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.missing[method] {
		return false
	}
	return m.listed == nil || m.listed[method]
}

func (m *methodSupport) observe(method string, err error) bool {
	if !cmux.IsMethodNotFound(err) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.missing == nil {
		m.missing = map[string]bool{}
	}
	m.missing[method] = true
	return true
}

var errMethodMissing = errors.New("cmux lacks this method")

func unsupported(what string) *requestError {
	return &requestError{Code: "unsupported", Message: "This version of cmux can't " + what + ". Update cmux."}
}

func guarded[T any](s *Server, method string, unsupported error, call func() (T, error)) (T, error) {
	var zero T
	if !s.methods.supports(method) {
		return zero, unsupported
	}
	result, err := call()
	if s.methods.observe(method, err) {
		return zero, unsupported
	}
	return result, err
}

func guard(s *Server, method string, unsupported error, call func() error) error {
	_, err := guarded(s, method, unsupported, func() (struct{}, error) { return struct{}{}, call() })
	return err
}

func probe[T any](s *Server, method string, call func() (T, error)) (T, bool) {
	result, err := guarded(s, method, errMethodMissing, call)
	return result, err == nil
}

var requestMethods = map[string][]string{
	"close_terminal":            {cmux.MethodCloseTerminal},
	"rename_terminal":           {cmux.MethodRenameTerminal},
	"restart_terminal":          {cmux.MethodRestartTerminal},
	"recent_sessions":           {cmux.MethodVaultSessions},
	"resume_session":            {cmux.MethodVaultSessions},
	"keep_awake":                {cmux.MethodKeepAwake},
	"set_keep_awake":            {cmux.MethodSetKeepAwake},
	"top":                       {cmux.MethodTop},
	"vms":                       {cmux.MethodVMList},
	"set_vm_paused":             {cmux.MethodVMPause, cmux.MethodVMResume, cmux.MethodVMStatus},
	"send_images":               {cmux.MethodChatSend},
	"send_agent_message":        {cmux.MethodAgentMessageSend},
	"workspace_action":          {cmux.MethodWorkspaceAction},
	"set_workspace_status":      {cmux.MethodSetLaneStatus},
	"checklist":                 {cmux.MethodChecklist},
	"add_checklist_item":        {cmux.MethodChecklistAdd, cmux.MethodChecklist},
	"edit_checklist_item":       {cmux.MethodChecklistEdit, cmux.MethodChecklist},
	"set_checklist_item_state":  {cmux.MethodChecklistState, cmux.MethodChecklist},
	"remove_checklist_item":     {cmux.MethodChecklistRemove, cmux.MethodChecklist},
	"clear_checklist":           {cmux.MethodChecklistClear, cmux.MethodChecklist},
	"create_group":              {cmux.MethodGroupCreate},
	"rename_group":              {cmux.MethodGroupRename},
	"pin_group":                 {cmux.MethodGroupPin, cmux.MethodGroupUnpin},
	"set_group_color":           {cmux.MethodGroupSetColor},
	"add_to_group":              {cmux.MethodGroupAdd},
	"remove_from_group":         {cmux.MethodGroupRemove},
	"ungroup":                   {cmux.MethodGroupUngroup},
	"create_workspace_in_group": {cmux.MethodGroupNewSpace},
	"reconnect_workspace":       {cmux.MethodRemoteReconnect},
}

var portRequests = []string{"port_link", "expose_port", "unexpose_port", "exposed_ports"}

func (s *Server) unsupportedRequests() []string {
	missing := func(method string) bool { return !s.methods.supports(method) }
	kinds := []string{}
	for kind, methods := range requestMethods {
		if slices.ContainsFunc(methods, missing) {
			kinds = append(kinds, kind)
		}
	}
	if s.options.Approvals == nil {
		kinds = append(kinds, "answer_approval")
	}
	if s.options.Agents == nil && !slices.Contains(kinds, "send_agent_message") {
		kinds = append(kinds, "send_agent_message")
	}
	if s.options.HostUsage == nil {
		kinds = append(kinds, "host_usage")
	}
	if !s.methods.supports(cmux.MethodTerminalGit) && !s.canDiscoverPorts() {
		kinds = append(kinds, "port_link", "expose_port")
	}
	if s.options.Tailnet == nil || s.options.Listeners == nil {
		for _, kind := range portRequests {
			if !slices.Contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	}
	slices.Sort(kinds)
	return kinds
}
