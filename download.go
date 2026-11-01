package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// download performs one sequential transfer. It does not retry or resume yet.
// Partial output is retained on failure, but is not proof of resumability.
func download(ctx context.Context, client *http.Client, rawURL, destination string) (int64, error) {
	if ctx == nil || client == nil {
		return 0, errors.New("a context and HTTP client are required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return 0, errors.New("provide a valid HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return 0, errors.New("URLs containing credentials are not supported")
	}
	if strings.TrimSpace(destination) == "" {
		return 0, errors.New("a destination file is required")
	}
	partialPath := destination + ".part"
	for _, path := range []string{destination, partialPath} {
		if err := requireAbsent(path); err != nil {
			return 0, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("prepare request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(req)
	if err != nil {
		// Avoid repeating the request URL in errors; query strings can be sensitive.
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return 0, fmt.Errorf("request file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server returned HTTP %d; expected 200", response.StatusCode)
	}
	encoding := response.Header.Get("Content-Encoding")
	if encoding != "" && !strings.EqualFold(encoding, "identity") {
		return 0, errors.New("server returned unsupported content encoding")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Exclusive creation also protects an existing partial file from truncation.
	partial, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create partial file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			partial.Close()
		}
	}()

	// Copy streams through bounded buffering rather than retaining the whole file.
	written, err := io.Copy(partial, response.Body)
	if err != nil {
		return written, fmt.Errorf("transfer failed; partial data kept at %q: %w", partialPath, err)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return written, fmt.Errorf("incomplete response; partial data kept at %q: %w", partialPath, io.ErrUnexpectedEOF)
	}
	if err := ctx.Err(); err != nil {
		return written, fmt.Errorf("download canceled; partial data kept at %q: %w", partialPath, err)
	}
	if err := partial.Sync(); err != nil {
		return written, fmt.Errorf("sync partial file %q: %w", partialPath, err)
	}
	err = partial.Close()
	closed = true
	if err != nil {
		return written, fmt.Errorf("close partial file %q: %w", partialPath, err)
	}
	if err := ctx.Err(); err != nil {
		return written, fmt.Errorf("download canceled; partial data kept at %q: %w", partialPath, err)
	}

	// Link publishes the completed data without replacing a destination that
	// appeared during the transfer. Both names are in the same directory.
	// Unsupported filesystems fail safely, leaving the partial file intact.
	if err := os.Link(partialPath, destination); err != nil {
		return written, fmt.Errorf("publish file; partial data kept at %q: %w", partialPath, err)
	}
	if err := os.Remove(partialPath); err != nil {
		return written, fmt.Errorf("file completed at %q, but partial-file cleanup failed: %w", destination, err)
	}
	return written, nil
}

func requireAbsent(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return fmt.Errorf("%q already exists: %w", path, os.ErrExist)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	return nil
}
