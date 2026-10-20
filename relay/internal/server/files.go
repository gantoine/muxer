package server

import "context"

const maxFileQueryBytes = 256

func (s *session) chatFiles(ctx context.Context, current *attachment, query string) ([]string, error) {
	agent, err := s.server.chatAgent(ctx, current.workspaceID, current.surfaceID)
	if err != nil {
		return nil, err
	}
	if agent.Cwd == "" {
		return nil, &requestError{Code: "no_agent", Message: "Files can only be listed while Claude, Codex or Pi runs in this terminal."}
	}
	found, err := s.server.files.Search(ctx, agent.Cwd, query)
	if err != nil {
		return nil, &requestError{Code: "not_found", Message: "The computer couldn't list the files in the agent's folder."}
	}
	return found, nil
}
