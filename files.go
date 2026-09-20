package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
)

func runAsset(args []string) error {
	if len(args) == 0 {
		return errors.New("bookmark asset: subcommand required")
	}
	if isHelp(args[0]) {
		return errHelp
	}
	action := args[0]
	switch action {
	case "list", "get", "upload", "download", "delete":
	default:
		return fmt.Errorf("bookmark asset: unknown subcommand %q", action)
	}
	fs := newFlagSet("bookmark asset " + action)
	var input, output string
	limit, offset := 100, 0
	if action == "upload" {
		fs.StringVar(&input, "input", "", "file to upload")
	}
	if action == "download" {
		fs.StringVar(&output, "output", "", "new destination file (never overwritten)")
	}
	if action == "list" {
		fs.IntVar(&limit, "limit", 100, "page size, 0 for everything")
		fs.IntVar(&offset, "offset", 0, "page offset")
	}
	if action == "list" || action == "get" || action == "upload" {
		fs.Bool("full", false, "return complete objects (default)")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	ids, err := parseIDs(fs.Name(), fs.Args())
	if err != nil {
		return err
	}
	want := 1
	if action == "get" || action == "download" {
		want = 2
	}
	if action == "delete" && len(ids) < 2 {
		return fmt.Errorf("%s: expected a bookmark ID followed by one or more asset IDs", fs.Name())
	}
	if action != "delete" && len(ids) != want {
		if want == 1 {
			return fmt.Errorf("%s: exactly one bookmark ID is required", fs.Name())
		}
		return fmt.Errorf("%s: exactly one bookmark ID and one asset ID are required", fs.Name())
	}
	if limit < 0 || offset < 0 {
		return errors.New("bookmark asset list: --limit and --offset must be >= 0")
	}
	if action == "upload" && input == "" {
		return errors.New("bookmark asset upload: --input is required")
	}
	if action == "download" && output == "" {
		return errors.New("bookmark asset download: --output is required")
	}
	c, err := newLinkdingClient()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/bookmarks/%d/assets/", ids[0])
	switch action {
	case "list":
		p, err := listPage(c, path, nil, limit, offset)
		if err != nil {
			return err
		}
		for _, raw := range p.Results {
			if err := validateResource(raw, "asset"); err != nil {
				return err
			}
		}
		return writeJSON(p)
	case "delete":
		result, err := deleteResources(c, path, ids[1:])
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "get":
		data, err := c.request("GET", path+strconv.FormatInt(ids[1], 10)+"/", nil, nil)
		if err != nil {
			return err
		}
		return writeResource(data, "asset")
	case "upload":
		data, err := c.upload(path+"upload/", input, nil)
		if err != nil {
			return err
		}
		return writeResource(data, "asset")
	default:
		n, err := c.download(path+strconv.FormatInt(ids[1], 10)+"/download/", output)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"bookmark": ids[0], "id": ids[1], "output": output, "bytes": n})
	}
}

func runSinglefile(args []string) error {
	fs := newFlagSet("bookmark singlefile")
	input := fs.String("input", "", "SingleFile HTML file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *input == "" {
		return errors.New("bookmark singlefile: exactly one URL and --input are required")
	}
	c, err := newLinkdingClient()
	if err != nil {
		return err
	}
	data, err := c.upload("/bookmarks/singlefile/", *input, map[string]string{"url": fs.Arg(0)})
	if err != nil {
		return err
	}
	var result struct {
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Message == nil {
		return errors.New("decoding SingleFile response: expected message")
	}
	return writeJSON(data)
}

// Spool multipart to a private temporary file: bounded memory, known length, no
// pipe goroutine to leak on early HTTP failure. Mutations are never replayed.
func (c *linkdingClient) upload(path, input string, fields map[string]string) (json.RawMessage, error) {
	info, err := os.Lstat(input)
	if err != nil {
		return nil, fmt.Errorf("opening input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("input must be a regular file, not a symlink or device")
	}
	source, err := openUploadInput(input, info)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	spool, err := os.CreateTemp("", "linkctl-upload-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	writer := multipart.NewWriter(spool)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return nil, err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filepath.Base(input)}))
	contentType := mime.TypeByExtension(filepath.Ext(input))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, source); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	size, err := spool.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.baseURL+"/api"+path, spool)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readJSONResponse(resp)
}

// openUploadInput checks the actual descriptor, not just the earlier pathname
// metadata. openUploadFile enforces nonblocking/no-follow opening on Unix and
// fails closed on platforms without that implementation.
func openUploadInput(path string, expected os.FileInfo) (*os.File, error) {
	file, err := openUploadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening input safely: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		file.Close()
		return nil, errors.New("input changed while opening")
	}
	return file, nil
}

// A private temporary sibling is published with an atomic no-clobber hard link.
// Neither server filenames nor Content-Disposition choose any local path.
func (c *linkdingClient) download(path, output string) (int64, error) {
	if _, err := os.Lstat(output); err == nil {
		return 0, errors.New("output already exists; choose a new path")
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".linkctl-download-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	req, err := http.NewRequest("GET", c.baseURL+"/api"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: expected status 200, got %d", resp.StatusCode)
	}
	n, err := io.Copy(file, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("downloading asset: %w", err)
	}
	if err = file.Close(); err != nil {
		return 0, err
	}
	if err = os.Link(file.Name(), output); err != nil {
		return 0, fmt.Errorf("publishing download without overwriting: %w", err)
	}
	return n, nil
}
