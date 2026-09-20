//go:build unix

package main

import (
	"os"
	"syscall"
)

// The open itself must not block on a FIFO substituted after Lstat, and must
// reject a substituted final symlink even when it points to the same inode.
// The caller validates the opened descriptor before reading any bytes.
func openUploadFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
