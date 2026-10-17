package server

import "context"

const maxFileQueryBytes = 256

func (s *session) chatFiles(ctx context.Context, current *attachment, query string) ([]string, error) {
	_, cwd, err := s.server.claudeSession(ctx, current.workspaceID, current.surfaceID)
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		return nil, &requestError{Code: "no_agent", Message: "Files can only be listed while Claude runs in this terminal."}
	}
	found, err := s.server.files.Search(ctx, cwd, query)
	if err != nil {
		return nil, &requestError{Code: "not_found", Message: "The computer couldn't list the files in Claude's folder."}
	}
	return found, nil
}
