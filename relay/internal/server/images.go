package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gantoine/muxer/relay/internal/claude"
	"github.com/gantoine/muxer/relay/internal/cmux"
)

var errImagesUnsupported = unsupported("receive images")

var imageFormats = map[string]bool{"png": true, "jpeg": true, "jpg": true, "webp": true, "gif": true}

func validImages(images []cmux.ChatImage) error {
	if len(images) == 0 || len(images) > maxImages {
		return invalid(fmt.Sprintf("Send 1 to %d images at a time.", maxImages))
	}
	for _, image := range images {
		if !imageFormats[image.Format] {
			return invalid("Images must be PNG, JPEG, WebP or GIF.")
		}
		decoded, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil || len(decoded) == 0 {
			return invalid("An image wasn't valid base64 data.")
		}
		if len(decoded) > maxImageBytes {
			return invalid(fmt.Sprintf("Each image can be up to %d MB.", maxImageBytes>>20))
		}
	}
	return nil
}

func (s *session) sendImages(ctx context.Context, current *attachment, text string, images []cmux.ChatImage) error {
	return guard(s.server, cmux.MethodChatSend, errImagesUnsupported, func() error {
		sessionID, _, err := s.server.claudeSession(ctx, current.workspaceID, current.surfaceID)
		if err != nil {
			return err
		}
		if sessionID == "" {
			return &requestError{Code: "no_agent", Message: "Images can only go to Claude, and Claude isn't running in this terminal."}
		}
		return s.server.cmux.SendChat(ctx, sessionID, text, images)
	})
}

func readAttachedImage(path string) (cmux.ChatImage, error) {
	if filepath.Clean(path) != path || !claude.IsAttachedImage(path) {
		return cmux.ChatImage{}, invalid("That isn't an image sent to Claude.")
	}
	gone := &requestError{Code: "not_found", Message: "That image is no longer on the computer."}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return cmux.ChatImage{}, gone
	}
	if info.Size() > maxImageBytes {
		return cmux.ChatImage{}, invalid(fmt.Sprintf("Images can be up to %d MB.", maxImageBytes>>20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cmux.ChatImage{}, gone
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return cmux.ChatImage{Data: base64.StdEncoding.EncodeToString(data), Format: format}, nil
}
