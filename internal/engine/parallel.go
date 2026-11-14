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
	header    http.Header
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
	return representation{url: response.Request.URL.String(), etag: response.Header.Get("ETag"), size: size, header: response.Header}, true, nil
}

func downloadFresh(ctx context.Context, client *http.Client, rawURL, destination, checksum string, progress io.Writer, connections int) (int64, error) {
	return downloadFreshNamed(ctx, client, rawURL, &destination, checksum, progress, connections, nil)
}

func downloadFreshNamed(ctx context.Context, client *http.Client, rawURL string, destinationPtr *string, checksum string, progress io.Writer, connections int, resolve func(http.Header) error) (int64, error) {
	destination := *destinationPtr
	if connections <= 1 || (runtime.GOOS != "linux" && runtime.GOOS != "windows") {
		return downloadNamed(ctx, client, rawURL, destinationPtr, checksum, progress, resolve)
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
	if resolve == nil {
		announceDestination(progress, destination)
		for _, path := range []string{destination, destination + ".part"} {
			if err := requireAbsent(path); err != nil {
				return 0, err
			}
		}
	}
	rep, ok, err := probeParallel(ctx, client, rawURL)
	if err != nil {
		return 0, err
	}
	if !ok {
		return downloadNamed(ctx, client, rawURL, destinationPtr, checksum, progress, resolve)
	}
	if resolve != nil {
		if err := resolve(rep.header); err != nil {
			return 0, err
		}
		destination = *destinationPtr
		announceDestination(progress, destination)
		for _, path := range []string{destination, destination + ".part"} {
			if err := requireAbsent(path); err != nil {
				return 0, err
			}
		}
	}
	return parallelDownload(ctx, client, rawURL, destination, checksum, progress, connections, rep)
}

type rangeResult struct {
	index      int
	downloaded int64
	digest     string
	err        error
}

type rangeCheckpoint struct {
	index      int
	downloaded int64
	digest     string
	done       chan error
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
	partial, err := openPartial(absolute+".part", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
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
	c := checkpoint{Version: 4, URL: rawURL, ResolvedURL: rep.url, Destination: absolute, Partial: absolute + ".part", ETag: rep.etag, Total: &rep.size, ExpectedSHA256: checksum, State: "downloading", PartialIdentity: identity, PartialToken: createPartialToken(partial)}
	if control := workerControlFrom(ctx); control != nil {
		workers = control.settings.Load().Maximum
		if budget, ok := ctx.Value(budgetContextKey{}).(*requestBudget); ok {
			policy, _ := budget.snapshot()
			if policy.Policy.Mode == "automatic" {
				workers = min(workers, max(1, int(rep.size/(256<<10))))
			}
		}
	}
	size := rep.size / int64(workers)
	for i := 0; i < workers; i++ {
		start := int64(i) * size
		end := start + size
		if i == workers-1 {
			end = rep.size
		}
		c.Chunks = append(c.Chunks, savedChunk{Start: start, End: end})
	}
	if err := saveTransferCheckpoint(ctx, cp, partial, c); err != nil {
		return 0, fmt.Errorf("save active job state; partial file kept: %w", err)
	}
	return runChunks(ctx, client, cp, partial, c, checksum, progress, workers)
}

func chunkDigest(partial *os.File, chunk savedChunk) (string, error) {
	chunk.Downloaded = chunk.End - chunk.Start
	return chunkPrefixDigest(partial, chunk)
}

func chunkPrefixDigest(partial *os.File, chunk savedChunk) (string, error) {
	return chunkPrefixDigestObserved(context.Background(), partial, chunk)
}
func chunkPrefixDigestObserved(ctx context.Context, partial *os.File, chunk savedChunk) (string, error) {
	hash := sha256.New()
	if _, err := hashTransferRange(ctx, partial, chunk.Start, chunk.savedLength(), hash); err != nil {
		return "", fmt.Errorf("hash chunk: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// runChunks owns checkpoint changes; workers only write their assigned disjoint ranges.
func runChunks(ctx context.Context, client *http.Client, path string, partial *os.File, c checkpoint, checksum string, progress io.Writer, connections int) (int64, error) {
	if connections < 1 || connections > 16 {
		return c.Completed, errors.New("connections must be between 1 and 16")
	}
	// Older checkpoints only contain whole chunks. Upgrade their lengths while
	// leaving unrecorded bytes outside the reusable prefix.
	c.Version = 4
	c.Chunks = append([]savedChunk(nil), c.Chunks...)
	for i := range c.Chunks {
		c.Chunks[i].Downloaded = c.Chunks[i].savedLength()
	}
	rep := representation{url: c.ResolvedURL, etag: c.ETag, size: *c.Total}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan rangeResult, len(c.Chunks))
	checkpoints := make(chan rangeCheckpoint)
	var count atomic.Int64
	count.Store(c.Completed)
	ranges := make([]transferSegment, 0, len(c.Chunks))
	for _, chunk := range c.Chunks {
		part := transferSegment{Start: chunk.Start, End: chunk.End, Downloaded: chunk.savedLength(), State: "waiting"}
		if chunk.SHA256 != "" {
			part.State, part.Downloaded = "completed", chunk.End-chunk.Start
		}
		ranges = append(ranges, part)
	}
	segments := newSegmentSession(progress, "parallel", connections, ranges)
	complete := false
	defer func() { segments.finish(ctx, complete) }()
	if progress != nil {
		fmt.Fprintf(progress, "Downloaded %d/%d bytes (%.1f%%)\n", c.Completed, rep.size, float64(c.Completed)*100/float64(rep.size))
	}
	control := workerControlFrom(ctx)
	if control == nil {
		control = newWorkerControl(connections, connections)
	}
	type rangeWorker struct {
		cancel   context.CancelFunc
		retiring bool
	}
	active := make(map[int]*rangeWorker)
	rawURL := c.URL
	buffers, _ := ctx.Value(bufferBudgetKey{}).(*bufferBudget)
	launch := func(i int) bool {
		var reserved *transferBuffers
		if buffers != nil {
			reserved, _ = buffers.reserve()
			if reserved == nil {
				return false
			}
		}

		chunk := c.Chunks[i]
		part := segments.part(i)
		part.setReceived(chunk.savedLength())
		part.setState(segmentConnecting)
		partCtx, stop := context.WithCancel(workerCtx)
		if reserved != nil {
			partCtx = context.WithValue(partCtx, transferBufferKey{}, reserved)
		}
		active[i] = &rangeWorker{cancel: stop}
		if m := measurementsFrom(ctx); m != nil {
			m.workerChanges.Add(1)
		}
		go func() {
			defer reserved.release()
			report := func(n int64, sum string) error {
				update := rangeCheckpoint{index: i, downloaded: n, digest: sum, done: make(chan error, 1)}
				// Keep draining reports after cancellation to save final prefixes.
				checkpoints <- update
				return <-update.done
			}
			n, digest, err := fetchRange(partCtx, client, rawURL, partial, chunk, rep, &count, part, report)
			if err == nil {
				part.setState(segmentVerifying)
			}
			stop()
			results <- rangeResult{index: i, downloaded: n, digest: digest, err: err}
		}()
		return true
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
					segments.publish()
					n := count.Load()
					fmt.Fprintf(progress, "Downloaded %d/%d bytes (%.1f%%)\n", n, rep.size, float64(n)*100/float64(rep.size))
				case <-done:
					return
				}
			}
		}()
	}
	var firstErr, checkpointErr error
	persistRange := func(index int, n int64, digest string, complete bool) error {
		if checkpointErr != nil {
			return checkpointErr
		}
		old := c.Chunks[index]
		if n <= old.savedLength() && !complete {
			return nil
		}
		updated := c
		updated.Chunks = append([]savedChunk(nil), c.Chunks...)
		part := &updated.Chunks[index]
		part.Downloaded = n
		part.PrefixSHA256 = digest
		if complete {
			part.SHA256, part.PrefixSHA256 = digest, ""
		}
		updated.Completed += n - old.savedLength()
		if err := saveTransferCheckpoint(ctx, path, partial, updated); err != nil {
			checkpointErr = fmt.Errorf("save chunk progress: %w", err)
			cancel()
			return checkpointErr
		}
		c = updated
		return nil
	}
	ctxDone := ctx.Done()
	for {
		var bufferChanged <-chan struct{}
		if buffers != nil {
			bufferChanged = buffers.signal()
		}
		settings := *control.settings.Load()
		requestedTarget := settings.Target
		target := min(requestedTarget, len(c.Chunks))
		settings.Target = target
		if target < requestedTarget {
			settings.Reason = "Limited by saved part layout"
		}
		if workerCtx.Err() == nil {
			running := 0
			for _, worker := range active {
				if !worker.retiring {
					running++
				}
			}
			for i := len(c.Chunks) - 1; running > target && i >= 0; i-- {
				if worker := active[i]; worker != nil && !worker.retiring {
					worker.retiring = true
					if part := segments.part(i); part != nil {
						part.retiring.Store(true)
					}
					if m := measurementsFrom(ctx); m != nil {
						m.workerChanges.Add(1)
					}
					worker.cancel()
					running--
				}
			}
			for i, chunk := range c.Chunks {
				if len(active) >= target {
					break
				}
				if chunk.SHA256 == "" && active[i] == nil {
					if !launch(i) {
						settings.Reason = "Waiting for transfer buffer capacity"
						break
					}
				}
			}
		}
		for _, worker := range active {
			if worker.retiring {
				settings.Reason = "Reducing active connections…"
			}
		}
		if segments != nil {
			segments.workers.Store(&settings)
			segments.publish()
		}
		if len(active) == 0 {
			unfinished := false
			for _, chunk := range c.Chunks {
				if chunk.SHA256 == "" {
					unfinished = true
					break
				}
			}
			if !unfinished || workerCtx.Err() != nil {
				break
			}
		}
		select {
		case <-ctxDone:
			ctxDone = nil
			cancel()
		case <-bufferChanged:
		case <-control.changed:
		case update := <-checkpoints:
			update.done <- persistRange(update.index, update.downloaded, update.digest, false)
		case result := <-results:
			worker := active[result.index]
			delete(active, result.index)
			if part := segments.part(result.index); part != nil {
				part.retiring.Store(false)
			}
			if result.err != nil {
				if worker.retiring && onlyCancellation(result.err) {
					// The writer has stopped and acknowledged its last durable prefix.
					count.Add(c.Chunks[result.index].savedLength() - result.downloaded)
					segments.part(result.index).setReceived(c.Chunks[result.index].savedLength())
					segments.part(result.index).setState(segmentWaiting)
					if m := measurementsFrom(ctx); m != nil {
						m.workerChanges.Add(1)
					}
					continue
				}
				if firstErr == nil || (errors.Is(firstErr, context.Canceled) && !errors.Is(result.err, context.Canceled)) {
					firstErr = result.err
				}
				segments.part(result.index).setState(segmentFailed)
				cancel()
				continue
			}
			if err := persistRange(result.index, result.downloaded, result.digest, true); err == nil {
				segments.part(result.index).setState(segmentComplete)
				segments.publish()
			}
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
			if err := saveTransferCheckpoint(ctx, path, partial, c); err != nil {
				checkpointErr = fmt.Errorf("save interrupted state: %w", err)
			}
		}
		return c.Completed, errors.Join(firstErr, checkpointErr)
	}
	if err := partial.Truncate(rep.size); err != nil {
		return c.Completed, fmt.Errorf("truncate completed partial: %w", err)
	}
	hash := sha256.New()
	if _, err := hashTransferRange(ctx, partial, 0, rep.size, hash); err != nil {
		return c.Completed, fmt.Errorf("hash downloaded file: %w", err)
	}
	if checksum != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), checksum) {
		return c.Completed, errors.New("SHA-256 mismatch; partial data kept")
	}
	if err := saveReadyCheckpointObserved(ctx, path, partial, &c, rep.size, hash); err != nil {
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
	complete = true
	return rep.size, nil
}

func resumeChunks(ctx context.Context, client *http.Client, path string, partial *os.File, c checkpoint, checksum string, progress io.Writer, connections int) (int64, error) {
	for _, chunk := range c.Chunks {
		if err := ctx.Err(); err != nil {
			return c.Completed, err
		}
		if chunk.savedLength() == 0 {
			continue
		}
		digest, err := chunkPrefixDigestObserved(ctx, partial, chunk)
		if err != nil || !strings.EqualFold(digest, chunk.savedDigest()) {
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
	if err := saveTransferCheckpoint(ctx, path, partial, c); err != nil {
		return c.Completed, fmt.Errorf("save active job state: %w", err)
	}
	return runChunks(ctx, client, path, partial, c, checksum, progress, connections)
}

func fetchRange(ctx context.Context, client *http.Client, rawURL string, partial *os.File, chunk savedChunk, rep representation, count *atomic.Int64, part *segmentPart, report func(int64, string) error) (int64, string, error) {
	digest := sha256.New()
	saved := chunk.savedLength()
	if _, err := hashTransferRange(ctx, partial, chunk.Start, saved, digest); err != nil {
		return saved, "", err
	}
	if saved > 0 && !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), chunk.savedDigest()) {
		return saved, "", errors.New("saved chunk prefix changed before worker restart")
	}
	start, end := chunk.Start+saved, chunk.End-1
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return saved, "", err
	}
	req = req.WithContext(context.WithValue(req.Context(), requestSegmentKey{}, part))
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("If-Range", rep.etag)
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
	if err != nil {
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return saved, "", fmt.Errorf("request range: %w", err)
	}
	defer response.Body.Close()
	if err := transientStatus(response); err != nil {
		return saved, "", err
	}
	length := end - start + 1
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != rep.url || response.StatusCode != http.StatusPartialContent || len(response.Header.Values("ETag")) != 1 || response.Header.Get("ETag") != rep.etag || len(response.Header.Values("Content-Range")) != 1 || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, rep.size) || len(response.Header.Values("Content-Encoding")) > 1 || (response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) || (response.ContentLength >= 0 && response.ContentLength != length) {
		return saved, "", errors.New("range response does not match selected representation")
	}
	part.setState(segmentDownloading)
	writer := &rangeWriter{out: measuredFileWriter{io.NewOffsetWriter(partial, start), measurementsFrom(ctx)}, count: count, part: part, digest: digest, written: saved, saved: saved, total: chunk.End - chunk.Start, last: time.Now(), report: report}
	_, err = copyTransferN(ctx, writer, response.Body, length, false)
	if saveErr := writer.savePrefix(); saveErr != nil {
		err = errors.Join(err, saveErr)
	}
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return writer.written, hex.EncodeToString(digest.Sum(nil)), fmt.Errorf("transfer range: %w", err)
	}
	var extra [1]byte
	m, err := io.ReadFull(response.Body, extra[:])
	if m != 0 || err == nil {
		return writer.written, hex.EncodeToString(digest.Sum(nil)), errors.New("range response exceeded its declared length")
	}
	if !errors.Is(err, io.EOF) {
		return writer.written, hex.EncodeToString(digest.Sum(nil)), fmt.Errorf("finish range: %w", err)
	}
	return writer.written, hex.EncodeToString(digest.Sum(nil)), nil
}

type rangeWriter struct {
	out                   io.Writer
	count                 *atomic.Int64
	part                  *segmentPart
	digest                hash.Hash
	written, saved, total int64
	last                  time.Time
	report                func(int64, string) error
}

func (w *rangeWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.count.Add(int64(n))
	w.part.add(int64(n))
	w.written += int64(n)
	_, _ = w.digest.Write(p[:n])
	if err == nil && (w.written-w.saved >= 1<<20 || time.Since(w.last) >= 2*time.Second) {
		err = w.savePrefix()
	}
	return n, err
}

func (w *rangeWriter) savePrefix() error {
	// Full ranges become reusable only after response EOF is validated.
	if w.written <= w.saved || w.written >= w.total {
		return nil
	}
	if err := w.report(w.written, hex.EncodeToString(w.digest.Sum(nil))); err != nil {
		return err
	}
	w.saved, w.last = w.written, time.Now()
	return nil
}
