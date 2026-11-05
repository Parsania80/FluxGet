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
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const minParallelSize = 1 << 20

type representation struct {
	url, etag string
	size      int64
}

// A probe is only an optimization; unsupported servers use the sequential path.
func probeParallel(ctx context.Context, client *http.Client, rawURL string) (representation, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return representation{}, false, err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
	if err != nil {
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return representation{}, false, fmt.Errorf("probe ranges: %w", err)
	}
	defer response.Body.Close()
	if err := transientStatus(response); err != nil {
		return representation{}, false, err
	}
	if response.StatusCode != http.StatusPartialContent || response.Request == nil || response.Request.URL == nil || len(response.Header.Values("ETag")) != 1 || !strongETag(response.Header.Get("ETag")) || len(response.Header.Values("Content-Range")) != 1 || len(response.Header.Values("Content-Encoding")) > 1 || (response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) {
		return representation{}, false, nil
	}
	value := response.Header.Get("Content-Range")
	sizeText, ok := strings.CutPrefix(value, "bytes 0-0/")
	if !ok {
		return representation{}, false, nil
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < minParallelSize || value != fmt.Sprintf("bytes 0-0/%d", size) || (response.ContentLength >= 0 && response.ContentLength != 1) {
		return representation{}, false, nil
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 2))
	if err != nil {
		return representation{}, false, fmt.Errorf("read range probe: %w", err)
	}
	if len(b) != 1 {
		return representation{}, false, nil
	}
	return representation{url: response.Request.URL.String(), etag: response.Header.Get("ETag"), size: size}, true, nil
}

func downloadFresh(ctx context.Context, client *http.Client, rawURL, destination, checksum string, progress io.Writer, connections int) (int64, error) {
	if connections <= 1 || runtime.GOOS != "linux" {
		return download(ctx, client, rawURL, destination, checksum, progress)
	}
	if ctx == nil || client == nil {
		return 0, errors.New("a context and HTTP client are required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !validQueueURL(rawURL) {
		return 0, errors.New("provide a valid HTTP or HTTPS URL without credentials")
	}
	if strings.TrimSpace(destination) == "" {
		return 0, errors.New("a destination file is required")
	}
	if checksum != "" {
		sum, err := hex.DecodeString(checksum)
		if err != nil || len(sum) != sha256.Size {
			return 0, errors.New("SHA-256 must be 64 hexadecimal characters")
		}
	}
	for _, path := range []string{destination, destination + ".part"} {
		if err := requireAbsent(path); err != nil {
			return 0, err
		}
	}
	rep, ok, err := probeParallel(ctx, client, rawURL)
	if err != nil {
		return 0, err
	}
	if !ok {
		return download(ctx, client, rawURL, destination, checksum, progress)
	}
	return parallelDownload(ctx, client, rawURL, destination, checksum, progress, connections, rep)
}

type rangeResult struct {
	index int
	err   error
}

func parallelDownload(ctx context.Context, client *http.Client, rawURL, destination, checksum string, progress io.Writer, workers int, rep representation) (int64, error) {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return 0, err
	}
	cp, err := checkpointLocation(absolute)
	if err != nil {
		return 0, err
	}
	partial, err := os.OpenFile(absolute+".part", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return 0, fmt.Errorf("create partial file: %w", err)
	}
	defer partial.Close()
	lock, err := lockJob(cp+".lock", partial)
	if err != nil {
		return 0, fmt.Errorf("lock download; partial file kept: %w", err)
	}
	defer lock.Close()
	if err := requireAbsent(cp); err != nil {
		return 0, fmt.Errorf("existing recovery metadata; partial file kept: %w", err)
	}
	identity, _, err := identifyPartial(partial)
	if err != nil {
		return 0, fmt.Errorf("identify partial file: %w", err)
	}
	c := checkpoint{Version: 3, URL: rawURL, ResolvedURL: rep.url, Destination: absolute, Partial: absolute + ".part", ETag: rep.etag, Total: &rep.size, ExpectedSHA256: checksum, State: "downloading", PartialIdentity: identity, PartialToken: createPartialToken(partial)}
	size := rep.size / int64(workers)
	for i := 0; i < workers; i++ {
		start := int64(i) * size
		end := start + size
		if i == workers-1 {
			end = rep.size
		}
		c.Chunks = append(c.Chunks, savedChunk{Start: start, End: end})
	}
	if err := saveCheckpoint(cp, partial, c); err != nil {
		return 0, fmt.Errorf("save active job state; partial file kept: %w", err)
	}
	return runChunks(ctx, client, cp, partial, c, checksum, progress, workers)
}

func chunkDigest(partial *os.File, chunk savedChunk) (string, error) {
	hash := sha256.New()
	if _, err := io.CopyN(hash, io.NewSectionReader(partial, chunk.Start, chunk.End-chunk.Start), chunk.End-chunk.Start); err != nil {
		return "", fmt.Errorf("hash chunk: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// runChunks owns checkpoint changes; workers only write their assigned disjoint ranges.
func runChunks(ctx context.Context, client *http.Client, path string, partial *os.File, c checkpoint, checksum string, progress io.Writer, connections int) (int64, error) {
	if connections < 1 || connections > 16 {
		return c.Completed, errors.New("connections must be between 1 and 16")
	}
	rep := representation{url: c.ResolvedURL, etag: c.ETag, size: *c.Total}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan rangeResult, len(c.Chunks))
	var count atomic.Int64
	count.Store(c.Completed)
	pending := 0
	slots := make(chan struct{}, connections)
	rawURL := c.URL
	for i, chunk := range c.Chunks {
		if chunk.SHA256 != "" {
			continue
		}
		pending++
		i, chunk := i, chunk
		go func() {
			var err error
			select {
			case slots <- struct{}{}:
				_, err = fetchRange(workerCtx, client, rawURL, partial, chunk.Start, chunk.End-1, rep, &count)
				<-slots
			case <-workerCtx.Done():
				err = workerCtx.Err()
			}
			if err != nil {
				cancel()
			}
			results <- rangeResult{index: i, err: err}
		}()
	}
	done := make(chan struct{})
	progressDone := make(chan struct{})
	if progress != nil {
		go func() {
			defer close(progressDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					n := count.Load()
					fmt.Fprintf(progress, "Downloaded %d/%d bytes (%.1f%%)\n", n, rep.size, float64(n)*100/float64(rep.size))
				case <-done:
					return
				}
			}
		}()
	}
	var firstErr, checkpointErr error
	for i := 0; i < pending; i++ {
		result := <-results
		if result.err != nil {
			if firstErr == nil || (errors.Is(firstErr, context.Canceled) && !errors.Is(result.err, context.Canceled)) {
				firstErr = result.err
			}
			cancel()
			continue
		}
		if checkpointErr != nil {
			continue
		}
		digest, err := chunkDigest(partial, c.Chunks[result.index])
		if err == nil {
			updated := c
			updated.Chunks = append([]savedChunk(nil), c.Chunks...)
			updated.Chunks[result.index].SHA256 = digest
			updated.Completed += updated.Chunks[result.index].End - updated.Chunks[result.index].Start
			err = saveCheckpoint(path, partial, updated)
			if err == nil {
				c = updated
			}
		}
		if err != nil {
			checkpointErr = fmt.Errorf("save completed chunk: %w", err)
			cancel()
		}
	}
	close(done)
	if progress != nil {
		<-progressDone
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	if firstErr != nil || checkpointErr != nil {
		if checkpointErr == nil {
			c.State = "interrupted"
			if err := saveCheckpoint(path, partial, c); err != nil {
				checkpointErr = fmt.Errorf("save interrupted state: %w", err)
			}
		}
		return c.Completed, errors.Join(firstErr, checkpointErr)
	}
	if err := partial.Truncate(rep.size); err != nil {
		return c.Completed, fmt.Errorf("truncate completed partial: %w", err)
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, io.NewSectionReader(partial, 0, rep.size), rep.size); err != nil {
		return c.Completed, fmt.Errorf("hash downloaded file: %w", err)
	}
	if checksum != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), checksum) {
		return c.Completed, errors.New("SHA-256 mismatch; partial data kept")
	}
	if err := saveReadyCheckpoint(path, partial, &c, rep.size, hash); err != nil {
		return c.Completed, err
	}
	if err := partial.Close(); err != nil {
		return c.Completed, err
	}
	if err := ctx.Err(); err != nil {
		return c.Completed, err
	}
	if err := publishFile(c.Partial, c.Destination, nil); err != nil {
		return c.Completed, err
	}
	if err := os.Remove(path); err != nil {
		return c.Completed, fmt.Errorf("file completed, but checkpoint cleanup failed: %w", err)
	}
	if progress != nil {
		fmt.Fprintf(progress, "Downloaded %d/%d bytes (100.0%%)\n", rep.size, rep.size)
	}
	return rep.size, nil
}

func resumeChunks(ctx context.Context, client *http.Client, path string, partial *os.File, c checkpoint, checksum string, progress io.Writer, connections int) (int64, error) {
	for _, chunk := range c.Chunks {
		if err := ctx.Err(); err != nil {
			return c.Completed, err
		}
		if chunk.SHA256 == "" {
			continue
		}
		digest, err := chunkDigest(partial, chunk)
		if err != nil || !strings.EqualFold(digest, chunk.SHA256) {
			return c.Completed, errors.New("saved chunk does not match checkpoint")
		}
	}
	if c.Completed < *c.Total {
		rep, ok, err := probeParallel(ctx, client, c.URL)
		if err != nil {
			return c.Completed, err
		}
		if !ok || rep.url != c.ResolvedURL || rep.etag != c.ETag || rep.size != *c.Total {
			return c.Completed, errors.New("remote file does not match saved chunks or does not support safe ranges")
		}
	}
	c.State = "downloading"
	c.ExpectedSHA256 = checksum
	if err := saveCheckpoint(path, partial, c); err != nil {
		return c.Completed, fmt.Errorf("save active job state: %w", err)
	}
	return runChunks(ctx, client, path, partial, c, checksum, progress, connections)
}

func fetchRange(ctx context.Context, client *http.Client, rawURL string, partial *os.File, start, end int64, rep representation, count *atomic.Int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("If-Range", rep.etag)
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
	if err != nil {
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return 0, fmt.Errorf("request range: %w", err)
	}
	defer response.Body.Close()
	if err := transientStatus(response); err != nil {
		return 0, err
	}
	length := end - start + 1
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != rep.url || response.StatusCode != http.StatusPartialContent || len(response.Header.Values("ETag")) != 1 || response.Header.Get("ETag") != rep.etag || len(response.Header.Values("Content-Range")) != 1 || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, rep.size) || len(response.Header.Values("Content-Encoding")) > 1 || (response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) || (response.ContentLength >= 0 && response.ContentLength != length) {
		return 0, errors.New("range response does not match selected representation")
	}
	writer := &rangeWriter{out: io.NewOffsetWriter(partial, start), count: count}
	n, err := io.CopyN(writer, response.Body, length)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return n, fmt.Errorf("transfer range: %w", err)
	}
	var extra [1]byte
	m, err := io.ReadFull(response.Body, extra[:])
	if m != 0 || err == nil {
		return n, errors.New("range response exceeded its declared length")
	}
	if !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("finish range: %w", err)
	}
	return n, nil
}

type rangeWriter struct {
	out   io.Writer
	count *atomic.Int64
}

func (w *rangeWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.count.Add(int64(n))
	return n, err
}
