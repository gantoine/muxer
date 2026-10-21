package posthogcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestPromptAttachmentsBecomeImageRefs(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript, notification("1", "session/prompt", `{"prompt":[
		{"type":"text","text":"what is wrong here?"},
		{"type":"resource_link","uri":"file:///tmp/workspace/repos/acme/app/.posthog/attachments/r1/a1/screen.png","name":"screen.png","mimeType":"image/png"},
		{"type":"resource_link","uri":"file:///tmp/workspace/repos/acme/app/.posthog/attachments/r1/a2/notes.txt","name":"notes.txt","mimeType":"text/plain"}
	]}`))
	entries := transcript.Entries()
	if len(entries) != 1 || entries[0].Text != "what is wrong here?" || len(entries[0].Images) != 1 || entries[0].Images[0] != "phcloud-image:r1/a1" {
		t.Fatalf("entries = %+v", entries)
	}
	runID, artifactID, ok := ParseImageRef(entries[0].Images[0])
	if !ok || runID != "r1" || artifactID != "a1" {
		t.Fatalf("ParseImageRef = %s %s %v", runID, artifactID, ok)
	}
	if _, _, ok := ParseImageRef("/var/clipboard.png"); ok {
		t.Fatal("a local path isn't a cloud image")
	}
}

func TestAttachmentsUploadThroughPresignedPosts(t *testing.T) {
	var mu sync.Mutex
	var stored map[string]string
	var finalized []any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects/2/tasks/t1/runs/r1/artifacts/prepare_upload/":
			var body struct {
				Artifacts []map[string]any `json:"artifacts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Artifacts) != 1 || body.Artifacts[0]["type"] != "user_attachment" || body.Artifacts[0]["size"] != float64(4) || body.Artifacts[0]["content_type"] != "image/png" {
				t.Errorf("prepare = %v", body)
			}
			_, _ = w.Write([]byte(`{"artifacts":[{"id":"tmp1","name":"image-1.png","type":"user_attachment","source":"posthog_code","content_type":"image/png","storage_path":"runs/r1/image-1.png","presigned_post":{"url":"` + server.URL + `/s3","fields":{"key":"runs/r1/image-1.png","policy":"p"}}}]}`))
		case "/s3":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("file: %v", err)
			}
			data, _ := io.ReadAll(file)
			mu.Lock()
			stored = map[string]string{"key": r.FormValue("key"), "policy": r.FormValue("policy"), "data": string(data), "type": header.Header.Get("Content-Type")}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/api/projects/2/tasks/t1/runs/r1/artifacts/finalize_upload/":
			var body struct {
				Artifacts []any `json:"artifacts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			finalized = body.Artifacts
			mu.Unlock()
			_, _ = w.Write([]byte(`{"artifacts":[{"id":"art-9","storage_path":"runs/r1/image-1.png"}]}`))
		case "/api/projects/2/tasks/t1/runs/r1/":
			_, _ = w.Write([]byte(`{"id":"r1","status":"in_progress","artifacts":[{"id":"art-9","storage_path":"runs/r1/image-1.png","content_type":"image/png"}]}`))
		case "/api/projects/2/tasks/t1/runs/r1/artifacts/presign/":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["storage_path"] != "runs/r1/image-1.png" {
				t.Errorf("presign = %v", body)
			}
			_, _ = w.Write([]byte(`{"url":"` + server.URL + `/download"}`))
		case "/download":
			if r.Header.Get("Authorization") != "" {
				t.Error("a presigned download must not carry the API key")
			}
			_, _ = w.Write([]byte("\x89PNG"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{Host: server.URL, ProjectID: 2, APIKey: "k"}, server.Client())

	ids, err := client.UploadAttachments(context.Background(), "t1", "r1", []Upload{{Name: "image-1.png", ContentType: "image/png", Data: []byte("\x89PNG")}})
	if err != nil || len(ids) != 1 || ids[0] != "art-9" {
		t.Fatalf("ids = %v err = %v", ids, err)
	}
	mu.Lock()
	if stored["key"] != "runs/r1/image-1.png" || stored["policy"] != "p" || stored["data"] != "\x89PNG" || stored["type"] != "image/png" {
		t.Errorf("stored = %v", stored)
	}
	if len(finalized) != 1 || finalized[0].(map[string]any)["storage_path"] != "runs/r1/image-1.png" {
		t.Errorf("finalized = %v", finalized)
	}
	mu.Unlock()

	data, contentType, err := client.DownloadArtifact(context.Background(), "t1", "r1", "art-9")
	if err != nil || string(data) != "\x89PNG" || contentType != "image/png" {
		t.Fatalf("download = %q %s %v", data, contentType, err)
	}
	if _, _, err := client.DownloadArtifact(context.Background(), "t1", "r1", "gone"); err != ErrArtifactMissing {
		t.Fatalf("a missing artifact = %v", err)
	}
}
