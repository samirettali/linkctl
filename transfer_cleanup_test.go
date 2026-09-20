package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type trackedTransferBody struct {
	io.Reader
	closed bool
}

func (b *trackedTransferBody) Close() error { b.closed = true; return nil }

func TestTransfersCloseBodiesAndSpoolFiles(t *testing.T) {
	input := writeTestFile(t, "source.txt", []byte("unchanged"))
	for _, action := range []string{"upload", "download"} {
		for _, status := range []int{200, 401, 502} {
			t.Run(action+http.StatusText(status), func(t *testing.T) {
				spool := t.TempDir()
				t.Setenv("TMPDIR", spool)
				payload := `{"id":1}`
				if status != 200 {
					payload = strings.Repeat("x", maxErrorResponseBytes*4)
				}
				body := &trackedTransferBody{Reader: strings.NewReader(payload)}
				var requestBody io.ReadCloser
				c := &linkdingClient{baseURL: "http://local.invalid", token: "fake", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					requestBody = r.Body
					if r.Body != nil {
						io.Copy(io.Discard, r.Body)
						r.Body.Close()
					}
					return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
				})}}
				var err error
				if action == "upload" {
					_, err = c.upload("/upload/", input, nil)
				} else {
					_, err = c.download("/download/", filepath.Join(t.TempDir(), "out"))
				}
				if (err == nil) != (status == 200) || !body.closed {
					t.Fatalf("err=%v closed=%t", err, body.closed)
				}
				if action == "upload" {
					if _, readErr := requestBody.Read(make([]byte, 1)); readErr == nil {
						t.Error("upload body remains open")
					}
				}
				files, _ := os.ReadDir(spool)
				if len(files) != 0 {
					t.Errorf("spool leak %v", files)
				}
			})
		}
	}
	got, _ := os.ReadFile(input)
	if string(got) != "unchanged" {
		t.Fatal("source changed")
	}
}

func TestTransfersCleanupOnTransportFailure(t *testing.T) {
	input := writeTestFile(t, "source", []byte("data"))
	spool := t.TempDir()
	t.Setenv("TMPDIR", spool)
	c := &linkdingClient{baseURL: "http://local.invalid", token: "fake", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, errors.New("offline")
	})}}
	if _, err := c.upload("/upload/", input, nil); err == nil {
		t.Fatal("upload succeeded")
	}
	outputDir := t.TempDir()
	if _, err := c.download("/download/", filepath.Join(outputDir, "out")); err == nil {
		t.Fatal("download succeeded")
	}
	for _, dir := range []string{spool, outputDir} {
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("leaked files %v", entries)
		}
	}
}

func TestUploadSpoolUnavailable(t *testing.T) {
	input := writeTestFile(t, "source", []byte("data"))
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("upload made request without spool") })
	if _, err := c.upload("/upload/", input, nil); err == nil {
		t.Fatal("expected temp storage failure")
	}
}
