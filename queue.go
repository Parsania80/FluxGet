package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type queueProgress struct {
	id     int
	text   string
	source *queueProgressWriter
}
type queueProgressWriter struct {
	id       int
	updates  chan<- queueProgress
	segments atomic.Pointer[segmentSnapshot]
}

func (w *queueProgressWriter) ReportSegments(snapshot segmentSnapshot) { w.segments.Store(&snapshot) }

func (w *queueProgressWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if len(msg) > 1024 {
		msg = msg[:1024]
	}
	select {
	case w.updates <- queueProgress{id: w.id, text: msg, source: w}:
	default:
	}
	return len(p), nil
}

type queueResult struct {
	id  int
	err error
}

type queueNameRequest struct {
	id          int
	destination string
	done        chan error
}
type queueNameKey struct{}

// runQueue owns all queue state; workers only report their results through done.
func runQueue(ctx context.Context, input io.Reader, output io.Writer, maxActive, connections int) error {
	path, err := queuePath()
	if err != nil {
		return err
	}
	lock, err := lockQueue(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	q, err := loadQueue(path)
	if err != nil {
		return fmt.Errorf("load queue: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		if err := backupQueue(path); err != nil {
			return fmt.Errorf("back up queue: %w", err)
		}
	}
	for i := range q.Jobs {
		if q.Jobs[i].State == "running" {
			q.Jobs[i].State = "paused"
			q.Jobs[i].Progress, q.Jobs[i].SavedProgress = "", nil
			if cp, e := checkpointLocation(q.Jobs[i].Destination); e == nil {
				if saved, e := loadCheckpoint(cp); e == nil && saved.URL == q.Jobs[i].URL && saved.Destination == q.Jobs[i].Destination && saved.Total != nil {
					q.Jobs[i].Progress = fmt.Sprintf("Downloaded %d/%d bytes", saved.Completed, *saved.Total)
				}
			}
		}
	}
	if err := saveQueue(path, q); err != nil {
		return fmt.Errorf("save queue: %w", err)
	}
	lines := make(chan string)
	scanStop := make(chan struct{})
	defer close(scanStop)
	inputDone := make(chan error, 1)
	scanLines, scanDone := lines, inputDone
	go func() {
		s := bufio.NewScanner(input)
		for s.Scan() {
			select {
			case scanLines <- s.Text():
			case <-ctx.Done():
				return
			case <-scanStop:
				return
			}
		}
		scanDone <- s.Err()
	}()
	done := make(chan queueResult, maxActive)
	updates := make(chan queueProgress, maxActive*2)
	names := make(chan queueNameRequest)
	progressWriters := make(map[int]*queueProgressWriter)
	speeds := make(map[int]*queueSpeed)
	layouts := make(map[int]segmentSnapshot)
	for _, job := range q.Jobs {
		if cp, e := checkpointLocation(job.Destination); e == nil {
			if c, e := loadCheckpoint(cp); e == nil && c.URL == job.URL && c.Destination == job.Destination {
				layouts[job.ID] = segmentsFromCheckpoint(c, connections)
			}
		}
	}
	active := make(map[int]context.CancelFunc)
	requested := make(map[int]string)
	started, stopping, desktopInitialized := false, false, false
	ctxDone := ctx.Done()
	var stopErr error
	var desktopRevision uint64
	fmt.Fprintln(output, "Queue ready. Type help for commands; start begins queued downloads.")
	persist := func() error {
		if err := saveQueue(path, q); err != nil {
			return fmt.Errorf("save queue: %w", err)
		}
		return nil
	}
	find := func(id int) int {
		for i := range q.Jobs {
			if q.Jobs[i].ID == id {
				return i
			}
		}
		return -1
	}
	shutdown := func(err error) {
		if !stopping {
			stopping = true
			lines = nil
			inputDone = nil
			ctxDone = nil
			stopErr = err
			for id, cancel := range active {
				if requested[id] == "" {
					requested[id] = "paused"
				}
				cancel()
			}
		}
	}
	defer func() {
		for _, cancel := range active {
			cancel()
		}
	}()
	for {
		if started && !stopping {
			for i := range q.Jobs {
				if len(active) >= maxActive {
					break
				}
				if q.Jobs[i].State != "queued" {
					continue
				}
				q.Jobs[i].State = "running"
				if err := persist(); err != nil {
					shutdown(err)
					break
				}
				job := q.Jobs[i]
				workerCtx, cancel := context.WithCancel(ctx)
				active[job.ID] = cancel
				writer := &queueProgressWriter{id: job.ID, updates: updates}
				progressWriters[job.ID] = writer
				delete(layouts, job.ID)
				baseline := int64(0)
				if cp, err := checkpointLocation(job.Destination); err == nil {
					if saved, err := loadCheckpoint(cp); err == nil && saved.URL == job.URL && saved.Destination == job.Destination {
						baseline = saved.Completed
						layouts[job.ID] = segmentsFromCheckpoint(saved, connections)
					}
				}
				speeds[job.ID] = &queueSpeed{bytes: baseline, sampled: time.Now()}
				workerCtx = context.WithValue(workerCtx, queueNameKey{}, names)
				go func(connectionLimit int) {
					done <- queueResult{job.ID, runQueueDownloadWithProgress(workerCtx, job, connectionLimit, writer)}
				}(connections)
			}
		}
		if stopping && len(active) == 0 {
			return stopErr
		}
		select {
		case <-ctxDone:
			shutdown(nil)
		case err := <-inputDone:
			shutdown(err)
		case update := <-updates:
			if progressWriters[update.id] == update.source && update.text != "" {
				if speed := speeds[update.id]; speed != nil {
					speed.update(update.text, time.Now())
				}
				if i := find(update.id); i >= 0 && strings.Contains(update.text, "Downloaded ") {
					q.Jobs[i].Progress = update.text
				}
				fmt.Fprintf(output, "Job %d: %s\n", update.id, update.text)
			}
		case request := <-names:
			i := find(request.id)
			var nameErr error
			if i < 0 || stopping || requested[request.id] != "" {
				nameErr = context.Canceled
			} else if nameErr = queueDestinationAvailable(q, request.destination, request.id); nameErr != nil {
				q.Jobs[i].Conflict = request.destination
			} else {
				q.Jobs[i].Destination = request.destination
				q.Jobs[i].AutomaticName = false
				q.Jobs[i].Conflict = ""
				if nameErr = persist(); nameErr != nil {
					shutdown(nameErr)
				}
			}
			request.done <- nameErr
		case result := <-done:
			if writer := progressWriters[result.id]; writer != nil {
				if snapshot := writer.segments.Load(); snapshot != nil {
					layouts[result.id] = *snapshot
				}
			}
			active[result.id]()
			delete(active, result.id)
			delete(progressWriters, result.id)
			delete(speeds, result.id)
			i := find(result.id)
			if i < 0 {
				shutdown(errors.New("active job disappeared"))
				continue
			}
			state := requested[result.id]
			delete(requested, result.id)
			if state == "" {
				if result.err != nil {
					state = "failed"
				} else {
					state = "completed"
				}
			}
			// A transfer that published successfully is complete even if pause arrived too late.
			if result.err == nil {
				state = "completed"
			} else if state != "" && !onlyCancellation(result.err) {
				state = "failed"
			}
			q.Jobs[i].State = state
			if layout, ok := layouts[result.id]; ok && state == "completed" {
				layout.Parts = append([]transferSegment(nil), layout.Parts...)
				for i := range layout.Parts {
					layout.Parts[i].State = "completed"
					if layout.Parts[i].End >= layout.Parts[i].Start {
						layout.Parts[i].Downloaded = layout.Parts[i].End - layout.Parts[i].Start
					}
				}
				layouts[result.id] = layout
			}
			if layout, ok := layouts[result.id]; ok && (state == "paused" || state == "canceled") {
				layout.Parts = append([]transferSegment(nil), layout.Parts...)
				for i := range layout.Parts {
					if layout.Parts[i].State != "completed" && layout.Parts[i].State != "saved" {
						layout.Parts[i].State = state
					}
				}
				layouts[result.id] = layout
			}
			q.Jobs[i].Message = ""
			if result.err != nil && state == "failed" {
				q.Jobs[i].Message = result.err.Error()
				if len(q.Jobs[i].Message) > 4096 {
					q.Jobs[i].Message = q.Jobs[i].Message[:4096]
				}
			}
			if err := persist(); err != nil {
				shutdown(err)
				continue
			}
			if result.err != nil && state == "failed" {
				fmt.Fprintf(output, "Job %d failed: %v\n", result.id, result.err)
			} else {
				fmt.Fprintf(output, "Job %d: %s\n", result.id, state)
			}
		case line := <-lines:
			command, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
			arg = strings.TrimSpace(arg)
			switch command {
			case "":
			case "desktop-json":
				req, requestErr := decodeDesktopRequest(arg)
				desktopRevision++
				response := desktopResponse{ID: req.ID, Revision: desktopRevision}
				changed := false
				if requestErr == nil {
					switch req.Operation {
					case "initialize":
						if len(active) != 0 {
							requestErr = errors.New("cannot initialize a desktop session while downloads are active")
						} else if !desktopInitialized {
							for i := range q.Jobs {
								if q.Jobs[i].State == "queued" {
									q.Jobs[i].State = "paused"
								}
							}
							changed = true
							started = true
							desktopInitialized = true
						}
					case "snapshot":
					case "configure":
						if req.MaxActive < 1 || req.MaxActive > 64 || req.Connections < 1 || req.Connections > 16 {
							requestErr = errors.New("simultaneous downloads must be 1–64 and connections 1–16")
						} else {
							maxActive, connections = req.MaxActive, req.Connections
						}
					case "add", "adopt":
						response.JobID, requestErr = desktopAdd(&q, req.Job, req.Operation == "adopt")
						changed = requestErr == nil
					case "published":
						i := find(req.Job.ID)
						info, e := os.Lstat(req.Job.OutputPath)
						if i < 0 || q.Jobs[i].State != "completed" || !filepath.IsAbs(req.Job.OutputPath) || e != nil || !info.Mode().IsRegular() {
							requestErr = errors.New("completed replacement output is unavailable")
						} else {
							q.Jobs[i].OutputPath = filepath.Clean(req.Job.OutputPath)
							changed = true
						}
					case "retarget":
						i := find(req.Job.ID)
						if i < 0 || q.Jobs[i].State == "running" || q.Jobs[i].State == "queued" || q.Jobs[i].State == "resetting" {
							requestErr = errors.New("pause the download before choosing a new filename")
						} else if requestErr = queueDestinationAvailable(q, req.Job.Destination, req.Job.ID); requestErr == nil {
							old := q.Jobs[i]
							cp, e := checkpointLocation(old.Destination)
							if e != nil {
								requestErr = e
							} else if _, e = os.Lstat(cp); !errors.Is(e, os.ErrNotExist) {
								requestErr = errors.New("saved data exists; keep it and add a separate download instead")
							} else if _, e = os.Lstat(old.Destination + ".part"); !errors.Is(e, os.ErrNotExist) {
								requestErr = errors.New("partial data exists; keep it and add a separate download instead")
							} else {
								q.Jobs[i].Destination, q.Jobs[i].AutomaticName, q.Jobs[i].Conflict = req.Job.Destination, false, ""
								q.Jobs[i].Message, q.Jobs[i].Progress, q.Jobs[i].State = "", "", "paused"
								q.Jobs[i].OutputPath, q.Jobs[i].SavedProgress = "", nil
								delete(layouts, req.Job.ID)
								changed = true
							}
						}
					default:
						requestErr = errors.New("unknown desktop operation")
					}
				}
				if changed {
					if e := persist(); e != nil {
						requestErr = e
						shutdown(e)
					}
				}
				if requestErr != nil {
					response.Error = requestErr.Error()
				}
				response.Jobs = desktopEntries(q.Jobs, speeds, liveSegmentLayouts(layouts, progressWriters), time.Now())
				data, _ := json.Marshal(response)
				fmt.Fprintf(output, "DESKTOP_JSON %s\n", data)
			case "help":
				fmt.Fprintln(output, "add URL OUTPUT | import OUTPUT | list | inspect ID | start | pause ID | resume ID | cancel ID | retry ID | restart ID | discard ID | remove ID | quit")
			case "list":
				for _, j := range q.Jobs {
					fmt.Fprintf(output, "%d\t%s\t%q\n", j.ID, j.State, j.Destination)
				}
			case "snapshot":
				type entry struct {
					ID          int    `json:"id"`
					State       string `json:"state"`
					Destination string `json:"destination"`
				}
				entries := make([]entry, 0, len(q.Jobs))
				for _, j := range q.Jobs {
					entries = append(entries, entry{j.ID, j.State, j.Destination})
				}
				data, e := json.Marshal(entries)
				if e != nil {
					fmt.Fprintln(output, "Cannot encode queue snapshot.")
				} else {
					fmt.Fprintf(output, "QUEUE_JSON %s\n", data)
				}
			case "inspect":
				id, e := strconv.Atoi(arg)
				i := find(id)
				if e != nil || i < 0 {
					fmt.Fprintln(output, "Unknown job ID.")
					continue
				}
				inspectQueueJob(output, q.Jobs[i])
			case "start":
				started = true
			case "quit":
				shutdown(nil)
			case "add", "import", "add-json", "import-json":
				raw, dest := "", arg
				if command == "add-json" || command == "import-json" {
					var input struct {
						URL         string `json:"url"`
						Destination string `json:"destination"`
					}
					if e := json.Unmarshal([]byte(arg), &input); e != nil {
						fmt.Fprintln(output, "Invalid queue input.")
						continue
					}
					raw, dest = input.URL, input.Destination
				} else if command == "add" {
					raw, dest, _ = strings.Cut(arg, " ")
					dest = strings.TrimSpace(dest)
				}
				if strings.TrimSpace(dest) == "" {
					fmt.Fprintln(output, "Invalid URL or destination.")
					continue
				}
				dest, err = filepath.Abs(dest)
				isImport := command == "import" || command == "import-json"
				if err != nil || arg == "" || (!isImport && (!validQueueURL(raw) || strings.TrimSpace(dest) == "")) {
					fmt.Fprintln(output, "Invalid URL or destination.")
					continue
				}
				duplicate := false
				for _, j := range q.Jobs {
					if j.Destination == dest {
						duplicate = true
					}
				}
				if duplicate {
					fmt.Fprintln(output, "Destination is already in the queue.")
					continue
				}
				cp, e := checkpointLocation(dest)
				if e != nil {
					fmt.Fprintf(output, "Cannot locate job: %v\n", e)
					continue
				}
				state := "queued"
				if isImport {
					c, e := loadCheckpoint(cp)
					if e != nil || c.Destination != dest {
						fmt.Fprintln(output, "No valid saved job for this destination.")
						continue
					}
					busy, e := jobActive(cp + ".lock")
					if e != nil || busy {
						fmt.Fprintln(output, "Saved job is active or cannot be inspected.")
						continue
					}
					raw, state = c.URL, "paused"
				} else {
					if _, e := os.Lstat(dest); !errors.Is(e, os.ErrNotExist) {
						fmt.Fprintln(output, "Destination already exists or cannot be inspected.")
						continue
					}
					if _, e := os.Lstat(dest + ".part"); !errors.Is(e, os.ErrNotExist) {
						fmt.Fprintln(output, "Partial file already exists or cannot be inspected; import a saved job instead.")
						continue
					}
					if _, e := os.Lstat(cp); !errors.Is(e, os.ErrNotExist) {
						fmt.Fprintln(output, "Saved job already exists or cannot be inspected; import it instead.")
						continue
					}
				}
				j := queueJob{ID: q.NextID, URL: raw, Destination: dest, State: state}
				q.NextID++
				q.Jobs = append(q.Jobs, j)
				if err := persist(); err != nil {
					shutdown(err)
					continue
				}
				fmt.Fprintf(output, "Job %d: %s\n", j.ID, state)
			case "pause", "resume", "cancel", "retry", "restart", "discard", "remove":
				id, e := strconv.Atoi(arg)
				i := find(id)
				if e != nil || i < 0 {
					fmt.Fprintln(output, "Unknown job ID.")
					continue
				}
				if command == "restart" || command == "discard" {
					if q.Jobs[i].State != "paused" && q.Jobs[i].State != "failed" && q.Jobs[i].State != "canceled" && q.Jobs[i].State != "resetting" {
						fmt.Fprintln(output, "Pause or cancel the job before cleanup.")
						continue
					}
					fatal, err := cleanupQueueJob(&q, i, path, command)
					if err != nil {
						if fatal {
							shutdown(err)
						} else {
							fmt.Fprintf(output, "Cleanup failed: %v\n", err)
						}
						continue
					}
					fmt.Fprintf(output, "Job %d: %s\n", id, q.Jobs[i].State)
					continue
				}
				j := &q.Jobs[i]
				if requested[id] != "" {
					fmt.Fprintln(output, "Job is already stopping.")
					continue
				}
				next := ""
				switch command {
				case "pause":
					if j.State == "queued" || j.State == "running" {
						next = "paused"
					}
				case "resume":
					if j.State == "paused" {
						next = "queued"
					}
				case "cancel":
					if j.State == "queued" || j.State == "running" || j.State == "paused" || j.State == "failed" {
						next = "canceled"
					}
				case "retry":
					if j.State == "failed" || j.State == "canceled" {
						next = "queued"
					}
				case "remove":
					if j.State != "running" && j.State != "queued" && j.State != "resetting" {
						q.Jobs = append(q.Jobs[:i], q.Jobs[i+1:]...)
						delete(layouts, id)
						if err := persist(); err != nil {
							shutdown(err)
						} else {
							fmt.Fprintf(output, "Job %d removed from queue; files retained.\n", id)
						}
						continue
					}
				}
				if next == "" {
					fmt.Fprintln(output, "Command is not valid for this job state.")
					continue
				}
				if cancel, ok := active[id]; ok {
					requested[id] = next
					cancel()
					fmt.Fprintf(output, "Stopping job %d...\n", id)
					continue
				}
				j.State = next
				j.Message = ""
				if err := persist(); err != nil {
					shutdown(err)
					continue
				}
				fmt.Fprintf(output, "Job %d: %s\n", id, next)
			default:
				fmt.Fprintln(output, "Unknown command; type help.")
			}
		}
	}
}

func runQueueDownload(ctx context.Context, j queueJob, connections int) error {
	return runQueueDownloadWithProgress(ctx, j, connections, io.Discard)
}

func runQueueDownloadWithProgress(ctx context.Context, j queueJob, connections int, progress io.Writer) error {
	client := defaultDownloadClient()
	if limiter, ok := ctx.Value(limiterContextKey{}).(*speedLimiter); ok {
		limitClient(client, limiter)
	}
	if path, ok := ctx.Value(liveSpeedKey{}).(string); ok {
		limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: fmt.Sprintf("queue:%d", j.ID)}})
	}
	defer client.CloseIdleConnections()
	return withRetries(ctx, time.Second, func() error {
		if j.AutomaticName {
			resolve := func(header http.Header) error {
				destination := j.Destination
				if name := serverFilename(header); name != "" {
					destination = filepath.Join(filepath.Dir(destination), name)
				}
				requests, ok := ctx.Value(queueNameKey{}).(chan queueNameRequest)
				if !ok {
					return errors.New("automatic filenames require the scheduler owner")
				}
				request := queueNameRequest{id: j.ID, destination: destination, done: make(chan error, 1)}
				select {
				case requests <- request:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case err := <-request.done:
					if err != nil {
						return err
					}
				case <-ctx.Done():
					return ctx.Err()
				}
				j.Destination, j.AutomaticName = destination, false
				return nil
			}
			_, err := downloadFreshNamed(ctx, client, j.URL, &j.Destination, j.ExpectedSHA256, progress, connections, resolve)
			return err
		}
		_, err := runDownloadAttempt(ctx, client, j, j.ExpectedSHA256, progress, connections)
		return err
	})
}

func runDownloadAttempt(ctx context.Context, client *http.Client, j queueJob, checksum string, progress io.Writer, connections int) (int64, error) {
	absolute, err := filepath.Abs(j.Destination)
	if err != nil {
		return 0, err
	}
	j.Destination = absolute
	cp, err := checkpointLocation(j.Destination)
	if err != nil {
		return 0, err
	}
	_, err = os.Lstat(cp)
	if err == nil {
		c, err := loadCheckpoint(cp)
		if err != nil {
			return 0, err
		}
		if c.URL != j.URL || c.Destination != j.Destination {
			return 0, errors.New("saved job does not match queue entry")
		}
		return resumeDownloadWithLimit(ctx, client, j.Destination, checksum, progress, connections)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if _, err = os.Lstat(j.Destination + ".part"); err == nil {
		return 0, errors.New("partial file has no checkpoint; manual investigation required")
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return downloadFresh(ctx, client, j.URL, j.Destination, checksum, progress, connections)
}

// A joined checkpoint error must not be mistaken for a successful pause.
func onlyCancellation(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !onlyCancellation(child) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, context.Canceled)
}
