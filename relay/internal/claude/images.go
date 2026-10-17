package claude

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
	ErrImageNotFound = errors.New("image not found in the transcript")
	imageRef         = regexp.MustCompile(`^([A-Za-z0-9-]{1,64}):(\d{1,4})(?::(\d{1,4}))?$`)
)

type Image struct {
	Data      string
	MediaType string
}

func IsImageRef(ref string) bool {
	return imageRef.MatchString(ref)
}

func ReadImage(transcriptPath, ref string) (Image, error) {
	match := imageRef.FindStringSubmatch(ref)
	if match == nil {
		return Image{}, ErrImageNotFound
	}
	block, _ := strconv.Atoi(match[2])
	item := -1
	if match[3] != "" {
		item, _ = strconv.Atoi(match[3])
	}
	file, err := os.Open(transcriptPath)
	if err != nil {
		return Image{}, err
	}
	defer file.Close()
	marker := []byte(`"uuid":"` + match[1] + `"`)
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		raw, err := reader.ReadBytes('\n')
		if bytes.Contains(raw, marker) {
			return imageInLine(raw, block, item)
		}
		if err == io.EOF {
			return Image{}, ErrImageNotFound
		}
		if err != nil {
			return Image{}, err
		}
	}
}

func imageInLine(raw []byte, block, item int) (Image, error) {
	var entry struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil || block >= len(entry.Message.Content) {
		return Image{}, ErrImageNotFound
	}
	target := entry.Message.Content[block]
	if item >= 0 {
		var result struct {
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(target, &result); err != nil || item >= len(result.Content) {
			return Image{}, ErrImageNotFound
		}
		target = result.Content[item]
	}
	var image struct {
		Type   string `json:"type"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if err := json.Unmarshal(target, &image); err != nil || image.Type != "image" || image.Source.Type != "base64" || image.Source.Data == "" {
		return Image{}, ErrImageNotFound
	}
	if !strings.HasPrefix(image.Source.MediaType, "image/") {
		return Image{}, ErrImageNotFound
	}
	return Image{Data: image.Source.Data, MediaType: image.Source.MediaType}, nil
}
