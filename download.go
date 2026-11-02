package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// download performs one sequential transfer. It does not retry or resume yet.
// Partial output is retained on failure, but is not proof of resumability.
func download(ctx context.Context, client *http.Client, rawURL, destination, expectedSHA256 string, progress io.Writer) (int64, error) {
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
	var expected []byte
	if expectedSHA256 != "" {
		expected, err = hex.DecodeString(expectedSHA256)
		if err != nil || len(expected) != sha256.Size {
			return 0, errors.New("SHA-256 must be 64 hexadecimal characters")
		}
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
	hash := sha256.New()
	tracker := &progressWriter{writer: io.MultiWriter(partial, hash), output: progress, total: response.ContentLength, start: time.Now()}
	tracker.report()
	written, err := io.Copy(tracker, response.Body)
	tracker.report()
	if err != nil {
		return written, fmt.Errorf("transfer failed; partial data kept at %q: %w", partialPath, err)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return written, fmt.Errorf("incomplete response; partial data kept at %q: %w", partialPath, io.ErrUnexpectedEOF)
	}
	if err := ctx.Err(); err != nil {
		return written, fmt.Errorf("download canceled; partial data kept at %q: %w", partialPath, err)
	}
	if expected != nil && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedSHA256) {
		return written, fmt.Errorf("SHA-256 mismatch; partial data kept at %q", partialPath)
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

type progressWriter struct {
	writer  io.Writer
	output  io.Writer
	total   int64
	written int64
	start   time.Time
	last    time.Time
}

func (p *progressWriter) Write(data []byte) (int, error) {
	n, err := p.writer.Write(data)
	p.written += int64(n)
	if time.Since(p.last) >= time.Second {
		p.report()
	}
	return n, err
}

func (p *progressWriter) report() {
	if p.output == nil {
		return
	}
	p.last = time.Now()
	speed := float64(p.written) / p.last.Sub(p.start).Seconds()
	if p.total > 0 {
		fmt.Fprintf(p.output, "Downloaded %d/%d bytes (%.1f%%), %.0f bytes/s\n", p.written, p.total, float64(p.written)*100/float64(p.total), speed)
	} else {
		fmt.Fprintf(p.output, "Downloaded %d bytes, %.0f bytes/s\n", p.written, speed)
	}
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
