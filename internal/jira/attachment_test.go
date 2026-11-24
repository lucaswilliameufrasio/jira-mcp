package jira

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDownloadAttachmentBoundsActualBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		_, _ = io.WriteString(w, "%PDF")
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, BearerToken: "synthetic-test-token"})
	attachment := Attachment{ID: "42"}
	data, contentType, err := client.DownloadAttachment(attachment, 4)
	if err != nil || string(data) != "%PDF" || contentType != "application/pdf" {
		t.Fatalf("download at limit: data = %q, type = %q, err = %v", data, contentType, err)
	}
	data, _, err = client.DownloadAttachment(attachment, 3)
	if !errors.Is(err, ErrAttachmentTooLarge) || data != nil {
		t.Fatalf("download over limit: data = %q, err = %v", data, err)
	}
}

func TestDownloadAttachmentKeepsDataCenterCredentialsOnConfiguredOrigin(t *testing.T) {
	var externalRequests atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		externalRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, external.URL, http.StatusFound)
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentServer, PersonalAccessToken: "synthetic-test-token"})
	for _, contentURL := range []string{external.URL, server.URL + "/secure/attachment/42/test.pdf"} {
		if _, _, err := client.DownloadAttachment(Attachment{ID: "42", Content: contentURL}, 4); err == nil {
			t.Error("expected download to reject external origin")
		}
	}
	if requests := externalRequests.Load(); requests != 0 {
		t.Errorf("made %d requests to external origin", requests)
	}
}
