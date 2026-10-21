package posthogcloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

const (
	ImageRefPrefix   = "phc-image:"
	maxArtifactBytes = 30 << 20
	attachmentsDir   = "/.posthog/attachments/"
)

var ErrArtifactMissing = errors.New("that image is no longer on the run")

type Artifact struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	StoragePath string `json:"storage_path"`
	ContentType string `json:"content_type"`
}

type Upload struct {
	Name        string
	ContentType string
	Data        []byte
}

func ImageRef(runID, artifactID string) string {
	return ImageRefPrefix + runID + "/" + artifactID
}

func ParseImageRef(ref string) (runID, artifactID string, ok bool) {
	rest, found := strings.CutPrefix(ref, ImageRefPrefix)
	if !found {
		return "", "", false
	}
	runID, artifactID, ok = strings.Cut(rest, "/")
	return runID, artifactID, ok && runID != "" && artifactID != ""
}

func attachmentFromURI(uri string) (runID, artifactID string, ok bool) {
	_, rest, found := strings.Cut(uri, attachmentsDir)
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (c *Client) DownloadArtifact(ctx context.Context, taskID, runID, artifactID string) ([]byte, string, error) {
	run, err := c.Run(ctx, taskID, runID)
	if err != nil {
		return nil, "", err
	}
	var artifact *Artifact
	for i := range run.Artifacts {
		if run.Artifacts[i].ID == artifactID {
			artifact = &run.Artifacts[i]
		}
	}
	if artifact == nil || artifact.StoragePath == "" {
		return nil, "", ErrArtifactMissing
	}
	var presigned struct {
		URL string `json:"url"`
	}
	if err := c.postJSON(ctx, c.runPath(taskID, runID, "artifacts/presign/"), map[string]any{"storage_path": artifact.StoragePath}, &presigned); err != nil {
		return nil, "", err
	}
	if presigned.URL == "" {
		return nil, "", ErrArtifactMissing
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, presigned.URL, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("the image download answered %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxArtifactBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxArtifactBytes {
		return nil, "", errors.New("the image is larger than 30 MB")
	}
	contentType := artifact.ContentType
	if contentType == "" {
		contentType = response.Header.Get("Content-Type")
	}
	return data, contentType, nil
}

type preparedArtifact struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Source        string `json:"source"`
	ContentType   string `json:"content_type"`
	StoragePath   string `json:"storage_path"`
	PresignedPost struct {
		URL    string            `json:"url"`
		Fields map[string]string `json:"fields"`
	} `json:"presigned_post"`
}

func (c *Client) UploadAttachments(ctx context.Context, taskID, runID string, uploads []Upload) ([]string, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	items := make([]map[string]any, len(uploads))
	for i, upload := range uploads {
		if len(upload.Data) == 0 || len(upload.Data) > maxArtifactBytes {
			return nil, fmt.Errorf("%s must be between 1 byte and 30 MB", upload.Name)
		}
		items[i] = map[string]any{"name": upload.Name, "type": "user_attachment", "source": "posthog_code", "size": len(upload.Data), "content_type": upload.ContentType}
	}
	var prepared struct {
		Artifacts []preparedArtifact `json:"artifacts"`
	}
	if err := c.postJSON(ctx, c.runPath(taskID, runID, "artifacts/prepare_upload/"), map[string]any{"artifacts": items}, &prepared); err != nil {
		return nil, fmt.Errorf("prepare the upload: %w", err)
	}
	if len(prepared.Artifacts) != len(uploads) {
		return nil, errors.New("PostHog prepared a different number of uploads")
	}
	finalize := make([]map[string]any, len(uploads))
	for i, artifact := range prepared.Artifacts {
		if err := c.postForm(ctx, artifact.PresignedPost.URL, artifact.PresignedPost.Fields, uploads[i]); err != nil {
			return nil, fmt.Errorf("upload %s: %w", uploads[i].Name, err)
		}
		finalize[i] = map[string]any{"id": artifact.ID, "name": artifact.Name, "type": artifact.Type, "source": artifact.Source, "content_type": artifact.ContentType, "storage_path": artifact.StoragePath}
	}
	var finalized struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	if err := c.postJSON(ctx, c.runPath(taskID, runID, "artifacts/finalize_upload/"), map[string]any{"artifacts": finalize}, &finalized); err != nil {
		return nil, fmt.Errorf("finish the upload: %w", err)
	}
	if len(finalized.Artifacts) != len(uploads) {
		return nil, errors.New("PostHog didn't confirm every upload")
	}
	ids := make([]string, len(finalized.Artifacts))
	for i, artifact := range finalized.Artifacts {
		ids[i] = artifact.ID
	}
	return ids, nil
}

func (c *Client) postForm(ctx context.Context, target string, fields map[string]string, upload Upload) error {
	if target == "" {
		return errors.New("PostHog gave no upload address")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, upload.Name))
	header.Set("Content-Type", upload.ContentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(upload.Data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("the storage answered %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}
