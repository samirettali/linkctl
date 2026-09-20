//go:build unix

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run the deterministic precheck/open substitution in a child: regressing to a
// blocking open must fail this test, not hang the entire test process on a FIFO.
func TestUploadInputReplacement(t *testing.T) {
	for _, replacement := range []string{"fifo", "symlink-same-inode", "different-regular", "directory", "missing", "unchanged"} {
		t.Run(replacement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadInputReplacementProcess$")
			cmd.Env = append(os.Environ(), "LINKCTL_TEST_INPUT_REPLACEMENT="+replacement)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("safe open blocked after %s substitution: %v\n%s", replacement, ctx.Err(), out)
			}
			if err != nil {
				t.Fatalf("replacement regression: %v\n%s", err, out)
			}
		})
	}
}

func TestUploadInputReplacementProcess(t *testing.T) {
	replacement := os.Getenv("LINKCTL_TEST_INPUT_REPLACEMENT")
	if replacement == "" {
		return
	}
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	input := filepath.Join(dir, "input")
	if err := os.WriteFile(original, []byte("original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, input); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(input)
	if err != nil {
		t.Fatal(err)
	}
	if replacement != "unchanged" {
		if err := os.Remove(input); err != nil {
			t.Fatal(err)
		}
	}
	switch replacement {
	case "fifo":
		err = syscall.Mkfifo(input, 0600)
	case "symlink-same-inode":
		err = os.Symlink(original, input)
	case "different-regular":
		err = os.WriteFile(input, []byte("replacement"), 0600)
	case "directory":
		err = os.Mkdir(input, 0700)
	}
	if err != nil {
		t.Fatal(err)
	}
	file, err := openUploadInput(input, info)
	if replacement == "unchanged" {
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		got, err := io.ReadAll(file)
		if err != nil || string(got) != "original bytes" {
			t.Fatalf("regular read: %q %v", got, err)
		}
		return
	}
	if err == nil {
		file.Close()
		t.Fatalf("accepted %s substituted after regular-file precheck", replacement)
	}
	if (replacement == "fifo" || replacement == "different-regular" || replacement == "directory") && !strings.Contains(err.Error(), "input changed") {
		t.Errorf("descriptor validation missing: %v", err)
	}
}
