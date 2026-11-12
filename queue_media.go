package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Parsania80/FluxGet/browserbridge"
	"github.com/Parsania80/FluxGet/internal/platformcmd"
	"github.com/Parsania80/FluxGet/internal/platformfile"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

type mediaUpdate struct {
	Phase      string  `json:"phase"`
	Downloaded int64   `json:"downloaded_bytes"`
	Total      int64   `json:"total_bytes"`
	Speed      float64 `json:"bytes_per_second"`
}

type mediaControl struct{ others atomic.Int64 }
type mediaControlKey struct{}

type mediaWorkerRequest struct {
	Job   queueJob
	Limit int64
}
type mediaWorkerResult struct {
	Error    string
	Canceled bool
}

func validateMediaQueueJob(j queueJob) error {
	if j.Media == nil {
		return nil
	}
	if err := j.Media.Validate(); err != nil {
		return err
	}
	url, err := media.NormalizeVideoURL(j.URL)
	if err != nil || !filepath.IsAbs(j.Destination) || filepath.Clean(j.Destination) != j.Destination || url != j.URL || url != "https://www.youtube.com/watch?v="+j.Media.Video.ID || j.AutomaticName || j.IntakeID != "" || j.ExpectedSHA256 != "" || !safeFilename(filepath.Base(j.Destination)) || !safeWindowsFilename(filepath.Base(j.Destination)) || !strings.HasSuffix(j.Destination, "."+j.Media.Selection.Container) {
		return errors.New("invalid YouTube download selection or destination")
	}
	return nil
}

func mediaPhase(phase string) string {
	switch phase {
	case "download":
		return "Downloading media"
	case "processing":
		return "Merging / converting"
	case "saving":
		return "Saving media progress"
	case "publishing":
		return "Verifying and publishing"
	case "limit":
		return "Applying speed limit"
	case "budget":
		return "Waiting for other downloads to stop"
	default:
		return "Analyzing media source"
	}
}

func (w *queueProgressWriter) ReportMedia(update mediaUpdate) {
	w.media.Store(&update)
	w.mediaAt.Store(time.Now().UnixNano())
	text := mediaPhase(update.Phase)
	if update.Total > 0 {
		text = fmt.Sprintf("Downloaded %d/%d bytes", update.Downloaded, update.Total)
	} else if update.Downloaded > 0 {
		text = fmt.Sprintf("Downloaded %d bytes", update.Downloaded)
	}
	_, _ = w.Write([]byte(text))
}

// The helper watches stdin until EOF. Even SIGKILL of the queue closes this
// pipe, so the helper cancels and reaps yt-dlp/FFmpeg before releasing its lock.
func runMediaWorker(input io.Reader, output io.Writer) int {
	// The queue may die while this helper reports to stdout. Go otherwise exits
	// on SIGPIPE for descriptor 1 before Fprintf can return its error. Keep the
	// helper alive long enough to cancel the tool tree and save verified state.
	signal.Ignore(syscall.SIGPIPE)
	defer signal.Reset(syscall.SIGPIPE)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	if !scanner.Scan() {
		return 2
	}
	var request mediaWorkerRequest
	if json.Unmarshal(scanner.Bytes(), &request) != nil || request.Limit < 0 || request.Limit > 1<<40 || request.Job.Media == nil || validateMediaQueueJob(request.Job) != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { scanner.Scan(); cancel() }()
	err := runMediaJob(ctx, request.Job, request.Limit, func(update mediaUpdate) {
		data, _ := json.Marshal(update)
		if _, e := fmt.Fprintf(output, "MEDIA_PROGRESS %s\n", data); e != nil {
			cancel()
		}
	})
	result := mediaWorkerResult{}
	if err != nil {
		result.Error = browserbridge.Diagnostic(err.Error())
		result.Canceled = onlyCancellation(err)
		if len(result.Error) > 4096 {
			result.Error = result.Error[:4096]
		}
	}
	data, _ := json.Marshal(result)
	fmt.Fprintf(output, "MEDIA_DONE %s\n", data)
	return 0
}

func mediaAggregate(state mediaRecovery, phase string, speed float64) mediaUpdate {
	update := mediaUpdate{Phase: phase, Speed: speed}
	known := len(state.Counters) > 0
	for _, counter := range state.Counters {
		if counter.Downloaded > math.MaxInt64-update.Downloaded || counter.Total > math.MaxInt64-update.Total {
			return mediaUpdate{Phase: phase}
		}
		update.Downloaded += counter.Downloaded
		update.Total += counter.Total
		known = known && counter.Total > 0
	}
	if !known {
		update.Total = 0
	}
	if phase != "download" {
		update.Speed = 0
	}
	return update
}

func runMediaJob(ctx context.Context, j queueJob, limit int64, report func(mediaUpdate)) error {
	if err := validateMediaQueueJob(j); err != nil {
		return err
	}
	path, err := mediaRecordPath(j.Destination)
	if err != nil {
		return err
	}
	lock, err := lockJobRecord(strings.TrimSuffix(path, ".media") + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := loadMediaRecord(path, j)
	existing := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if existing {
		if !state.Clean {
			return errors.New("interrupted media workspace needs Restart; saved files have been kept")
		}
		if err := verifyMediaFiles(ctx, state, j, false); err != nil {
			return err
		}
		if state.Ready != "" {
			report(mediaAggregate(state, "publishing", 0))
			return publishMedia(ctx, state, j)
		}
	}
	if err := requireAbsent(j.Destination); err != nil {
		return errors.New("destination exists; choose another filename")
	}
	report(mediaUpdate{Phase: "checking"})
	tools, err := media.LoadTools(j.Media.Manifest)
	if err != nil {
		return err
	}
	route := networkproxy.Config{}
	if j.Proxy != nil {
		route = *j.Proxy
	}
	client, err := media.NewWithProxy(tools, route)
	if err != nil {
		return err
	}
	versions, err := client.Probe(ctx)
	if err != nil {
		return err
	}
	if existing && !reflect.DeepEqual(versions, state.Tools) {
		return errors.New("media tool versions changed; use Restart before continuing saved data")
	}
	current, err := client.Analyze(ctx, j.URL)
	if err != nil {
		return err
	}
	if !j.Media.Matches(current) {
		return errors.New("selected media source changed; add the video again with an available format")
	}
	if !existing {
		state, err = createMediaRecord(j, versions)
		if err != nil {
			return err
		}
	}
	if state.Counters == nil {
		state.Counters = map[string]mediaCounter{}
	}
	for _, format := range j.Media.Video.Formats {
		if _, ok := state.Counters[format.ID]; !ok {
			state.Counters[format.ID] = mediaCounter{Total: format.Size}
		}
	}
	state.Clean = false
	if err := saveMediaRecord(path, state); err != nil {
		return err
	}
	report(mediaAggregate(state, "download", 0))
	events := make(chan media.Progress, 64)
	type outcome struct {
		result media.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := client.Download(ctx, j.Media.Video, j.Media.Selection, media.DownloadOptions{Directory: filepath.Dir(j.Destination), Workspace: state.Workspace, LimitBytesPerSecond: limit, Events: events})
		done <- outcome{result, err}
	}()
	consume := func(event media.Progress) {
		if event.FormatID != "" {
			if _, ok := state.Counters[event.FormatID]; ok {
				state.Counters[event.FormatID] = mediaCounter{Downloaded: event.Downloaded, Total: event.Total}
			}
		}
		report(mediaAggregate(state, event.Phase, event.Speed))
	}
	var result outcome
wait:
	for {
		select {
		case event := <-events:
			consume(event)
		case result = <-done:
			break wait
		}
	}
	for draining := true; draining; {
		select {
		case event := <-events:
			consume(event)
		default:
			draining = false
		}
	}
	report(mediaAggregate(state, "saving", 0))
	// A canceled operation may still save verified local state, with a deadline.
	saveCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := snapshotMediaFiles(saveCtx, &state, j); err != nil {
		return errors.Join(result.err, err)
	}
	state.Clean = true
	if result.err == nil {
		state.Ready = "asset." + j.Media.Selection.Container
	}
	if err := saveMediaRecord(path, state); err != nil {
		return errors.Join(result.err, err)
	}
	if result.err != nil {
		return result.err
	}
	report(mediaAggregate(state, "publishing", 0))
	return publishMedia(ctx, state, j)
}

func mediaLimits(ctx context.Context, id int) (limit, overall int64, err error) {
	if limiter, _ := ctx.Value(limiterContextKey{}).(*speedLimiter); limiter != nil && limiter.live == nil {
		overall = limiter.bytesPerSecond
	}
	path, _ := ctx.Value(liveSpeedKey{}).(string)
	if path != "" {
		data, e := platformfile.ReadFile(path)
		var c liveSpeedConfig
		if e != nil || len(data) > 1<<20 || json.Unmarshal(data, &c) != nil || c.Default < 0 || c.Default > 1<<30 || c.Overall < 0 || c.Overall > 1<<30 {
			return 0, 0, errors.New("cannot read valid speed settings for media download")
		}
		if c.Enabled {
			limit = c.Default * 1024
		}
		for _, v := range c.Overrides {
			if v < 0 || v > 1<<30 {
				return 0, 0, errors.New("invalid media speed settings")
			}
		}
		if v := c.Overrides[fmt.Sprintf("queue:%d", id)]; v > 0 {
			limit = minMediaRate(limit, v*1024)
		}
		overall = minMediaRate(overall, c.Overall*1024)
	}
	return minMediaRate(limit, overall), overall, nil
}

func minMediaRate(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return min(a, b)
}

func runQueueMedia(ctx context.Context, j queueJob, progress io.Writer) error {
	control, _ := ctx.Value(mediaControlKey{}).(*mediaControl)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		limit, overall, err := mediaLimits(ctx, j.ID)
		if err != nil {
			return err
		}
		if control != nil && overall > 0 && control.others.Load() > 0 {
			if reporter, ok := progress.(interface{ ReportMedia(mediaUpdate) }); ok {
				reporter.ReportMedia(mediaUpdate{Phase: "budget"})
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		childCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- runMediaCompanion(childCtx, j, limit, progress) }()
		ticker := time.NewTicker(250 * time.Millisecond)
		changed := false
		var runErr error
	waitChild:
		for {
			select {
			case runErr = <-done:
				break waitChild
			case <-ctx.Done():
				cancel()
				runErr = <-done
				break waitChild
			case <-ticker.C:
				next, nextOverall, e := mediaLimits(ctx, j.ID)
				if e != nil || next != limit || control != nil && nextOverall > 0 && control.others.Load() > 0 {
					changed = true
					cancel()
					runErr = <-done
					if e != nil {
						err = e
					}
					break waitChild
				}
			}
		}
		ticker.Stop()
		cancel()
		if runErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			if !onlyCancellation(runErr) {
				return errors.Join(ctx.Err(), runErr)
			}
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if !changed || runErr == nil {
			return runErr
		}
		if !onlyCancellation(runErr) {
			return runErr
		}
		if reporter, ok := progress.(interface{ ReportMedia(mediaUpdate) }); ok {
			reporter.ReportMedia(mediaUpdate{Phase: "limit"})
		}
	}
}

func runMediaCompanion(ctx context.Context, j queueJob, limit int64, progress io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := platformcmd.CommandContext(ctx, executable, "-media-worker")
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	cmd.Cancel = input.Close
	cmd.WaitDelay = 25 * time.Second
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cannot start the media companion")
	}
	data, _ := json.Marshal(mediaWorkerRequest{Job: j, Limit: limit})
	if _, err = input.Write(append(data, '\n')); err != nil {
		input.Close()
		cmd.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cannot send media download to the companion")
	}
	var result *mediaWorkerResult
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MEDIA_PROGRESS ") {
			var update mediaUpdate
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "MEDIA_PROGRESS ")), &update) == nil && update.Downloaded >= 0 && update.Total >= 0 && update.Speed >= 0 && !math.IsNaN(update.Speed) && !math.IsInf(update.Speed, 0) {
				if reporter, ok := progress.(interface{ ReportMedia(mediaUpdate) }); ok {
					reporter.ReportMedia(update)
				}
			}
		} else if strings.HasPrefix(line, "MEDIA_DONE ") {
			var value mediaWorkerResult
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "MEDIA_DONE ")), &value) == nil {
				result = &value
			}
		}
	}
	if scanner.Err() != nil {
		input.Close()
	}
	waitErr := cmd.Wait()
	if result != nil && result.Error == "" && waitErr == nil {
		return nil
	}
	if result != nil && result.Canceled {
		return context.Canceled
	}
	if result != nil && result.Error != "" {
		return errors.New(browserbridge.Diagnostic(result.Error))
	}
	return errors.New("media companion stopped unexpectedly; saved data kept")
}
