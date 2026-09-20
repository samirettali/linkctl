//go:build !unix

package main

import (
	"errors"
	"os"
)

func openUploadFile(string) (*os.File, error) {
	return nil, errors.New("safe file uploads are supported only on Unix platforms (including macOS and Linux)")
}
