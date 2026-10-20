package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrImageNotFound = errors.New("image not found in the session")
	imageRef         = regexp.MustCompile(`^([A-Za-z0-9-]{1,64}):(\d{1,4})$`)
)

type Image struct {
	Data      string
	MediaType string
}

func ReadImage(sessionPath, ref string) (Image, error) {
	match := imageRef.FindStringSubmatch(ref)
	if match == nil {
		return Image{}, ErrImageNotFound
	}
	position, _ := strconv.Atoi(match[2])
	file, err := os.Open(sessionPath)
	if err != nil {
		return Image{}, err
	}
	defer file.Close()
	marker := []byte(`"id":"` + match[1] + `"`)
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		raw, err := reader.ReadBytes('\n')
		if bytes.Contains(raw, marker) {
			if image, ok := imageInLine(raw, match[1], position); ok {
				return image, nil
			}
		}
		if err == io.EOF {
			return Image{}, ErrImageNotFound
		}
		if err != nil {
			return Image{}, err
		}
	}
}

func imageInLine(raw []byte, id string, position int) (Image, bool) {
	var entry struct {
		ID      string `json:"id"`
		Message struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &entry) != nil || entry.ID != id || entry.Message.Role != "user" || position >= len(entry.Message.Content) {
		return Image{}, false
	}
	var image struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	}
	if json.Unmarshal(entry.Message.Content[position], &image) != nil || image.Type != "image" || image.Data == "" {
		return Image{}, false
	}
	if !strings.HasPrefix(image.MimeType, "image/") {
		return Image{}, false
	}
	return Image{Data: image.Data, MediaType: image.MimeType}, true
}
