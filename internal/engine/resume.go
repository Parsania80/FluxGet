package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func jobDirectory() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "fluxget", "jobs"), nil
}

func checkpointLocation(destination string) (string, error) {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	dir, err := jobDirectory()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := sha256.Sum256([]byte(absolute))
	return filepath.Join(dir, hex.EncodeToString(id[:])+".json"), nil
}

func saveInterrupted(path string, partial *os.File, c *checkpoint, written int64) error {
	return saveInterruptedObserved(context.Background(), path, partial, c, written)
}

func saveInterruptedObserved(ctx context.Context, path string, partial *os.File, c *checkpoint, written int64) error {
	if c == nil || written < 0 || (c.Total != nil && written > *c.Total) {
		return nil
	}
	hash := sha256.New()
	if _, err := hashTransferRange(ctx, partial, 0, written, hash); err != nil {
		return fmt.Errorf("hash partial file: %w", err)
	}
	c.Completed = written
	c.PrefixSHA256 = hex.EncodeToString(hash.Sum(nil))
	c.Version = 2
	c.State = "interrupted"
	if err := saveTransferCheckpoint(ctx, path, partial, *c); err != nil {
		return fmt.Errorf("save recovery checkpoint: %w", err)
	}
	return nil
}

func checkpointProgress(path string, partial *os.File, c *checkpoint, digest hash.Hash, last int64) func(int64) error {
	return checkpointProgressObserved(context.Background(), path, partial, c, digest, last)
}

func checkpointProgressObserved(ctx context.Context, path string, partial *os.File, c *checkpoint, digest hash.Hash, last int64) func(int64) error {
	return func(written int64) error {
		if written-last < 1<<20 || written >= *c.Total {
			return nil
		}
		updated := *c
		updated.Completed = written
		updated.PrefixSHA256 = hex.EncodeToString(digest.Sum(nil))
		if err := saveTransferCheckpoint(ctx, path, partial, updated); err != nil {
			return fmt.Errorf("save recovery checkpoint: %w", err)
		}
		last = written
		return nil
	}
}

func saveReadyCheckpoint(path string, partial *os.File, c *checkpoint, written int64, digest hash.Hash) error {
	return saveReadyCheckpointObserved(context.Background(), path, partial, c, written, digest)
}

func saveReadyCheckpointObserved(ctx context.Context, path string, partial *os.File, c *checkpoint, written int64, digest hash.Hash) error {
	ready := *c
	ready.Version = 2
	ready.Chunks = nil
	ready.State = "ready"
	ready.Total = &written
	ready.Completed = written
	ready.PrefixSHA256 = hex.EncodeToString(digest.Sum(nil))
	if err := saveTransferCheckpoint(ctx, path, partial, ready); err != nil {
		return fmt.Errorf("save final publication checkpoint; partial file kept: %w", err)
	}
	clearPartialToken(partial, c.PartialToken)
	return nil
}

func resumeDownload(ctx context.Context, client *http.Client, destination, expectedSHA256 string, progress io.Writer) (int64, error) {
	return resumeDownloadWithLimit(ctx, client, destination, expectedSHA256, progress, 4)
}

func resumeDownloadWithLimit(ctx context.Context, client *http.Client, destination, expectedSHA256 string, progress io.Writer, connections int) (int64, error) {
	if ctx == nil || client == nil {
		return 0, errors.New("a context and HTTP client are required")
	}
	destinationErr := requireAbsent(destination)
	if destinationErr != nil && !errors.Is(destinationErr, os.ErrExist) {
		return 0, destinationErr
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		return 0, fmt.Errorf("locate checkpoint: %w", err)
	}
	c, err := loadCheckpoint(path)
	if err != nil {
		if destinationErr != nil {
			return 0, destinationErr
		}
		return 0, err
	}
	absolute, err := filepath.Abs(destination)
	if err != nil || c.Destination != absolute {
		return 0, errors.New("checkpoint destination does not match")
	}
	if expectedSHA256 == "" {
		expectedSHA256 = c.ExpectedSHA256
	} else if c.ExpectedSHA256 != "" && !strings.EqualFold(expectedSHA256, c.ExpectedSHA256) {
		return 0, errors.New("checksum differs from saved download")
	}
	if expectedSHA256 != "" {
		if sum, err := hex.DecodeString(expectedSHA256); err != nil || len(sum) != sha256.Size {
			return 0, errors.New("SHA-256 must be 64 hexadecimal characters")
		}
	}
	if c.State == "ready" {
		return reconcilePublication(ctx, path, c, expectedSHA256)
	}
	if destinationErr != nil {
		return 0, destinationErr
	}
	info, err := os.Lstat(c.Partial)
	if err != nil || !info.Mode().IsRegular() {
		return 0, errors.New("saved partial file is missing or is not a regular file")
	}
	partial, err := openPartial(c.Partial, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open partial file: %w", err)
	}
	defer partial.Close()
	opened, err := partial.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() < c.Completed {
		return 0, errors.New("saved partial file has changed")
	}
	lock, err := lockJob(path+".lock", partial)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	// Another owner may have updated the checkpoint before this lock was acquired.
	current, err := loadCheckpoint(path)
	if err != nil || !reflect.DeepEqual(current, c) {
		return 0, errors.New("checkpoint changed while acquiring ownership")
	}
	if c.PartialIdentity != nil {
		identity, _, err := identifyPartial(partial)
		if err != nil || !reflect.DeepEqual(identity, c.PartialIdentity) {
			return 0, errors.New("partial file identity differs from checkpoint")
		}
	}
	if c.parallel() {
		return resumeChunks(ctx, client, path, partial, c, expectedSHA256, progress, connections)
	}
	hash := sha256.New()
	if _, err := hashTransferRange(ctx, partial, 0, c.Completed, hash); err != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), c.PrefixSHA256) {
		return 0, errors.New("saved partial file does not match its checkpoint")
	}
	c.ExpectedSHA256 = expectedSHA256
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("prepare resume request: %w", err)
	}
	validatedRange := c.Total != nil && strongETag(c.ETag) && c.ResolvedURL != "" && c.Completed > 0 && c.Completed < *c.Total
	if c.Completed <= 0 {
		return 0, errors.New("no saved data is available to resume")
	}
	if validatedRange {
		if err := validateRemote(ctx, client, c); err != nil {
			return 0, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", c.Completed))
		req.Header.Set("If-Range", c.ETag)
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return 0, fmt.Errorf("request remaining data: %w", err)
	}
	defer response.Body.Close()
	if err := transientStatus(response); err != nil {
		return 0, err
	}
	if validatedRange && (response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != c.ResolvedURL ||
		response.StatusCode != http.StatusPartialContent || len(response.Header.Values("ETag")) != 1 || response.Header.Get("ETag") != c.ETag ||
		len(response.Header.Values("Content-Range")) != 1 || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", c.Completed, *c.Total-1, *c.Total) ||
		len(response.Header.Values("Content-Encoding")) > 1 || (response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) ||
		(response.ContentLength >= 0 && response.ContentLength != *c.Total-c.Completed)) {
		return 0, errors.New("resume response does not match saved checkpoint")
	}
	if !validatedRange {
		encoding := response.Header.Get("Content-Encoding")
		if response.StatusCode != http.StatusOK || response.Request == nil || response.Request.URL == nil || response.ContentLength < c.Completed || len(response.Header.Values("Content-Encoding")) > 1 || (encoding != "" && !strings.EqualFold(encoding, "identity")) {
			return 0, errors.New("cannot verify saved data against the current response")
		}
		if progress != nil {
			fmt.Fprintf(progress, "Verifying %d saved bytes against the server…\n", c.Completed)
		}
		remoteHash := sha256.New()
		if _, err := copyTransferN(ctx, remoteHash, response.Body, c.Completed, true); err != nil {
			return 0, fmt.Errorf("verify saved data: %w", err)
		}
		if !strings.EqualFold(hex.EncodeToString(remoteHash.Sum(nil)), c.PrefixSHA256) {
			return 0, errors.New("server data differs from saved partial; all files kept")
		}
		total := response.ContentLength
		c.Total = &total
		c.ResolvedURL = response.Request.URL.String()
	}
	c.Version = 2
	c.State = "downloading"
	if err := saveTransferCheckpoint(ctx, path, partial, c); err != nil {
		return 0, fmt.Errorf("save active job state: %w", err)
	}
	if err := partial.Truncate(c.Completed); err != nil {
		return 0, fmt.Errorf("discard uncheckpointed data: %w", err)
	}
	if _, err := partial.Seek(c.Completed, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek partial file: %w", err)
	}
	tracker := &progressWriter{writer: io.MultiWriter(measuredFileWriter{partial, measurementsFrom(ctx)}, hash), output: progress, total: *c.Total, written: c.Completed, startWritten: c.Completed, start: time.Now()}
	segments := newSegmentSession(progress, "sequential", 1, []transferSegment{{Start: 0, End: *c.Total, Downloaded: c.Completed, State: "downloading"}})
	complete := false
	defer func() { segments.finish(ctx, complete) }()
	tracker.segment, tracker.segments = segments.part(0), segments
	tracker.checkpoint = checkpointProgressObserved(ctx, path, partial, &c, hash, c.Completed)
	tracker.report()
	written, err := copyTransfer(ctx, tracker, io.LimitReader(response.Body, *c.Total-c.Completed+1), false)
	tracker.report()
	totalWritten := c.Completed + written
	if err != nil || totalWritten != *c.Total {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return totalWritten, errors.Join(fmt.Errorf("resume failed; partial data kept: %w", err), saveInterruptedObserved(ctx, path, partial, &c, totalWritten))
	}
	if err := ctx.Err(); err != nil {
		return totalWritten, fmt.Errorf("download canceled; partial data kept: %w", err)
	}
	if expectedSHA256 != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedSHA256) {
		return totalWritten, errors.New("SHA-256 mismatch; partial data kept")
	}
	tracker.segment.setState(segmentVerifying)
	segments.publish()
	if err := syncTransferFile(ctx, partial); err != nil {
		return totalWritten, fmt.Errorf("sync partial file: %w", err)
	}
	if err := saveReadyCheckpointObserved(ctx, path, partial, &c, totalWritten, hash); err != nil {
		return totalWritten, err
	}
	if err := partial.Close(); err != nil {
		return totalWritten, fmt.Errorf("close partial file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return totalWritten, fmt.Errorf("download canceled; partial data kept: %w", err)
	}
	if err := publishFile(c.Partial, c.Destination, nil); err != nil {
		return totalWritten, err
	}
	if err := os.Remove(path); err != nil {
		return totalWritten, fmt.Errorf("file completed, but checkpoint cleanup failed: %w", err)
	}
	complete = true
	return totalWritten, nil
}
