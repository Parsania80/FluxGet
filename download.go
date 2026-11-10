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
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// download performs one sequential transfer and retains partial output on failure.
func download(ctx context.Context, client *http.Client, rawURL, destination, expectedSHA256 string, progress io.Writer) (int64, error) {
	return downloadNamed(ctx, client, rawURL, &destination, expectedSHA256, progress, nil)
}

func downloadNamed(ctx context.Context, client *http.Client, rawURL string, destinationPtr *string, expectedSHA256 string, progress io.Writer, resolve func(http.Header) error) (int64, error) {
	destination := *destinationPtr
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
	if resolve == nil {
		announceDestination(progress, destination)
		for _, path := range []string{destination, partialPath} {
			if err := requireAbsent(path); err != nil {
				return 0, err
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("prepare request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
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
		if err := transientStatus(response); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("server returned HTTP %d; expected 200", response.StatusCode)
	}
	encoding := response.Header.Get("Content-Encoding")
	if len(response.Header.Values("Content-Encoding")) > 1 || (encoding != "" && !strings.EqualFold(encoding, "identity")) {
		return 0, errors.New("server returned unsupported content encoding")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if resolve != nil {
		if err := resolve(response.Header); err != nil {
			return 0, err
		}
		destination = *destinationPtr
		announceDestination(progress, destination)
		partialPath = destination + ".part"
		for _, path := range []string{destination, partialPath} {
			if err := requireAbsent(path); err != nil {
				return 0, err
			}
		}
	}

	// Exclusive creation also protects an existing partial file from truncation.
	partial, err := os.OpenFile(partialPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create partial file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			partial.Close()
		}
	}()
	var saved *checkpoint
	var checkpointPath string
	var ownership *jobLock
	if runtime.GOOS == "linux" && response.Request != nil && response.Request.URL != nil {
		checkpointPath, err = checkpointLocation(destination)
		if err != nil {
			return 0, fmt.Errorf("prepare recovery metadata; partial file kept: %w", err)
		}
		ownership, err = lockJob(checkpointPath+".lock", partial)
		if err != nil {
			return 0, fmt.Errorf("lock download; partial file kept: %w", err)
		}
		defer ownership.Close()
		if err := requireAbsent(checkpointPath); err != nil {
			return 0, fmt.Errorf("existing recovery metadata; partial file kept: %w", err)
		}
		absoluteDestination, err := filepath.Abs(destination)
		if err != nil {
			return 0, err
		}
		identity, _, err := identifyPartial(partial)
		if err != nil {
			return 0, fmt.Errorf("identify partial file: %w", err)
		}
		emptySum := sha256.Sum256(nil)
		saved = &checkpoint{Version: 2, URL: rawURL, ResolvedURL: response.Request.URL.String(), Destination: absoluteDestination, Partial: absoluteDestination + ".part", ExpectedSHA256: expectedSHA256, State: "downloading", PartialIdentity: identity, PartialToken: createPartialToken(partial), PrefixSHA256: hex.EncodeToString(emptySum[:])}
		if response.ContentLength > 0 {
			total := response.ContentLength
			saved.Total = &total
			if len(response.Header.Values("ETag")) == 1 && strongETag(response.Header.Get("ETag")) {
				saved.ETag = response.Header.Get("ETag")
			}
		}
		if err := saveTransferCheckpoint(ctx, checkpointPath, partial, *saved); err != nil {
			return 0, fmt.Errorf("save active job state; partial file kept: %w", err)
		}
	}

	// Copy streams through bounded buffering rather than retaining the whole file.
	hash := sha256.New()
	tracker := &progressWriter{writer: io.MultiWriter(measuredFileWriter{partial, measurementsFrom(ctx)}, hash), output: progress, total: response.ContentLength, start: time.Now()}
	segments := newSegmentSession(progress, "sequential", 1, []transferSegment{{Start: 0, End: response.ContentLength, State: "downloading"}})
	complete := false
	defer func() { segments.finish(ctx, complete) }()
	tracker.segment = segments.part(0)
	tracker.segments = segments
	if saved != nil && saved.Total != nil {
		tracker.checkpoint = checkpointProgressObserved(ctx, checkpointPath, partial, saved, hash, 0)
	}
	tracker.report()
	written, err := copyTransfer(ctx, tracker, response.Body, false)
	tracker.report()
	if err != nil {
		return written, errors.Join(fmt.Errorf("transfer failed; partial data kept at %q: %w", partialPath, err), saveInterruptedObserved(ctx, checkpointPath, partial, saved, written))
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return written, errors.Join(fmt.Errorf("incomplete response; partial data kept at %q: %w", partialPath, io.ErrUnexpectedEOF), saveInterruptedObserved(ctx, checkpointPath, partial, saved, written))
	}
	if err := ctx.Err(); err != nil {
		return written, errors.Join(fmt.Errorf("download canceled; partial data kept at %q: %w", partialPath, err), saveInterruptedObserved(ctx, checkpointPath, partial, saved, written))
	}
	if expected != nil && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedSHA256) {
		return written, fmt.Errorf("SHA-256 mismatch; partial data kept at %q", partialPath)
	}
	tracker.segment.setState(segmentVerifying)
	segments.publish()
	if err := syncTransferFile(ctx, partial); err != nil {
		return written, fmt.Errorf("sync partial file %q: %w", partialPath, err)
	}
	if saved != nil {
		if err := saveReadyCheckpointObserved(ctx, checkpointPath, partial, saved, written, hash); err != nil {
			return written, err
		}
	}
	err = partial.Close()
	closed = true
	if err != nil {
		return written, fmt.Errorf("close partial file %q: %w", partialPath, err)
	}
	if err := ctx.Err(); err != nil {
		return written, fmt.Errorf("download canceled; partial data kept at %q: %w", partialPath, err)
	}

	if err := publishFile(partialPath, destination, nil); err != nil {
		return written, err
	}
	if saved != nil {
		if err := os.Remove(checkpointPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return written, fmt.Errorf("file completed at %q, but checkpoint cleanup failed: %w", destination, err)
		}
	}
	complete = true
	return written, nil
}

func announceDestination(progress io.Writer, destination string) {
	if progress != nil {
		fmt.Fprintf(progress, "Saving to %q\n", destination)
	}
}

// publishFile uses a hard link so an existing destination is never replaced.
func publishFile(partial, destination string, hook func(string)) error {
	if hook != nil {
		hook("before-link")
	}
	if err := os.Link(partial, destination); err != nil {
		return fmt.Errorf("publish file; partial data kept at %q: %w", partial, err)
	}
	if hook != nil {
		hook("after-link")
	}
	if err := os.Remove(partial); err != nil {
		return fmt.Errorf("file completed at %q, but partial-file cleanup failed: %w", destination, err)
	}
	if hook != nil {
		hook("after-partial-removal")
	}
	return nil
}

func safeHTTPClient(client *http.Client) *http.Client {
	redirectClient := *client
	redirectClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if (next.URL.Scheme != "http" && next.URL.Scheme != "https") || next.URL.Hostname() == "" || next.URL.User != nil {
			return errors.New("redirect URL must be HTTP or HTTPS without credentials")
		}
		if via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return errors.New("HTTPS to HTTP redirects are not allowed")
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(next, via)
		}
		return nil
	}
	return &redirectClient
}

type progressWriter struct {
	writer       io.Writer
	output       io.Writer
	total        int64
	written      int64
	startWritten int64
	start        time.Time
	last         time.Time
	checkpoint   func(int64) error
	segment      *segmentPart
	segments     *segmentSession
}

func (p *progressWriter) Write(data []byte) (int, error) {
	n, err := p.writer.Write(data)
	p.written += int64(n)
	p.segment.setReceived(p.written)
	if err == nil && p.checkpoint != nil {
		err = p.checkpoint(p.written)
	}
	if time.Since(p.last) >= time.Second {
		p.report()
	}
	return n, err
}

func (p *progressWriter) report() {
	p.segments.publish()
	if p.output == nil {
		return
	}
	p.last = time.Now()
	speed := float64(p.written-p.startWritten) / p.last.Sub(p.start).Seconds()
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
