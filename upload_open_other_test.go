//go:build !unix

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestUnsupportedUploadFailsBeforeNetwork(t *testing.T) {
	input := writeTestFile(t, "input.txt", []byte("data"))
	client := testClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("unsupported upload contacted server")
	})
	_, err := client.upload("/bookmarks/1/assets/upload/", input, nil)
	if err == nil || !strings.Contains(err.Error(), "supported only on Unix") {
		t.Fatalf("missing platform remedy: %v", err)
	}
}
