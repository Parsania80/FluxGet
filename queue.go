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

	"github.com/Parsania80/FluxGet/browserbridge"
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
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	return runQueueClock(ctx, input, output, maxActive, connections, time.Now, ticker.C)
}

// A clock boundary lets scheduler integration tests advance observation windows
// while real HTTP transfers and checkpoint owners continue normally.
func runQueueClock(ctx context.Context, input io.Reader, output io.Writer, maxActive, connections int, clock func() time.Time, ticks <-chan time.Time) error {
	buffers := newBufferBudget()
	ctx = context.WithValue(ctx, bufferBudgetKey{}, buffers)
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
	budget := newRequestBudget(requestPolicy{Mode: "manual", Total: 8, PerOrigin: 4})
	measurements := make(map[int]*transferMeasurements)
	samplers := make(map[int]*observationSampler)
	observations := make(map[int]transferObservation)
	generation := uint64(1)
	automatic := newAutomaticController()
	resources := newResourceGuard()
	hardware := startHardwareMonitor()
	defer hardware.close()
	defer budget.close()
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
	workerControls := make(map[int]*workerControl)
	requested := make(map[int]string)
	seenTimeouts := make(map[int]int64)
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
	applyAutomatic := func(update *automaticUpdate) {
		if update == nil {
			return
		}
		if control := workerControls[update.job]; control != nil {
			maximum := control.settings.Load().Maximum
			_ = control.set(maximum, min(maximum, max(1, update.target)))
		}
	}
	automaticJobs := func() []automaticJob {
		jobs := make([]automaticJob, 0, len(active))
		_, requests := budget.snapshot()
		live := liveSegmentLayouts(layouts, progressWriters)
		for _, job := range q.Jobs {
			control := workerControls[job.ID]
			if control == nil {
				continue
			}
			settings := control.settings.Load()
			origin := queueOrigin(job.URL)
			if actual := measurements[job.ID].origin.Load(); actual != nil {
				origin = *actual
			}
			jobs = append(jobs, automaticJob{id: job.ID, origin: origin, layout: live[job.ID], metrics: measurements[job.ID].snapshot(), target: settings.Target, maximum: settings.Maximum, requests: requests[job.ID], stopping: requested[job.ID] != ""})
		}
		return jobs
	}
	for {
		management, _ := budget.snapshot()
		fileMaximum := maxActive
		if management.Policy.Mode == "automatic" {
			fileMaximum = automatic.admissionMaximum(maxActive, automaticJobs(), budget, clock())
			if resources.blocked {
				fileMaximum = len(active)
			}
		}
		if started && !stopping {
			for i := range q.Jobs {
				if len(active) >= fileMaximum {
					break
				}
				if q.Jobs[i].State != "queued" || (management.Policy.Mode == "automatic" && !budget.canStart(queueOrigin(q.Jobs[i].URL), clock())) {
					continue
				}
				q.Jobs[i].State = "running"
				if err := persist(); err != nil {
					shutdown(err)
					break
				}
				job := q.Jobs[i]
				generation++
				automatic.admitted(generation)
				workerCtx, cancel := context.WithCancel(ctx)
				m := &transferMeasurements{}
				measurements[job.ID] = m
				samplers[job.ID] = &observationSampler{}
				samplers[job.ID].sample(clock(), m.snapshot(), generation)
				delete(observations, job.ID)
				workerCtx = context.WithValue(workerCtx, measurementContextKey{}, m)
				workerCtx = context.WithValue(workerCtx, budgetContextKey{}, budget)
				policy, _ := budget.snapshot()
				target := connections
				if policy.Policy.Mode == "automatic" {
					target = 1
				}
				control := newWorkerControl(connections, target)
				workerControls[job.ID] = control
				workerCtx = context.WithValue(workerCtx, workerControlKey{}, control)
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
				speeds[job.ID] = &queueSpeed{bytes: baseline, sampled: clock()}
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
		case now := <-ticks:
			resourceChanged := resources.observe(now, hardware.snapshot(ctx, clock()), automaticJobs(), generation)
			if management.Policy.Mode == "automatic" && !stopping {
				if automatic.readLimits(ctx) {
					generation++
				}
				if resourceChanged {
					// Reset throughput learning, without invalidating the storage
					// trial whose own recovery may have caused this transition.
					applyAutomatic(automatic.discard(generation, now))
				}
				if resources.blocked {
					applyAutomatic(automatic.discard(generation, now))
					for _, update := range resources.adjust(now, automaticJobs(), generation) {
						applyAutomatic(&update)
					}
					automatic.resourceBlocked = true
					automatic.resourceReason = resources.reason
					automatic.files = 1
				} else {
					automatic.resourceBlocked = false
					for _, update := range resources.adjust(now, automaticJobs(), generation) {
						applyAutomatic(&update)
						applyAutomatic(automatic.discard(generation, now))
					}
				}
				jobs := automaticJobs()
				feedback := budget.takeFeedback()
				timedOut := map[int]bool{}
				for _, j := range jobs {
					if j.metrics.Timeouts-seenTimeouts[j.id] >= 2 {
						timedOut[j.id] = true
						seenTimeouts[j.id] = j.metrics.Timeouts
					}
				}
				if len(feedback) > 0 || len(timedOut) > 0 {
					applyAutomatic(automatic.discard(generation, now))
					for origin := range feedback {
						budget.backOffOrigin(origin, now)
					}
					for _, j := range jobs {
						if feedback[j.origin] > 0 || timedOut[j.id] {
							applyAutomatic(&automaticUpdate{j.id, max(1, j.target/2)})
						}
					}
					automatic.status, automatic.reason = "Backing off", "Server requested a cooldown"
					if len(feedback) == 0 {
						automatic.reason = "Repeated timeouts; reducing affected download connections"
					}
				} else {
					if budget.recoverOrigin(now) {
						generation++
					}
					applyAutomatic(automatic.tick(now, generation, maxActive, jobs, q.Jobs, budget))
				}
			}
			for id := range active {
				if observation, ok := samplers[id].sample(now, measurements[id].snapshot(), generation); ok {
					observations[id] = observation
				}
			}
		case <-ctxDone:
			shutdown(nil)
		case err := <-inputDone:
			shutdown(err)
		case update := <-updates:
			if progressWriters[update.id] == update.source && update.text != "" {
				if speed := speeds[update.id]; speed != nil {
					speed.update(update.text, clock())
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
			generation++
			if writer := progressWriters[result.id]; writer != nil {
				if snapshot := writer.segments.Load(); snapshot != nil {
					layouts[result.id] = *snapshot
				}
			}
			active[result.id]()
			delete(active, result.id)
			delete(workerControls, result.id)
			delete(seenTimeouts, result.id)
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
				if q.Jobs[i].IntakeID != "" {
					q.Jobs[i].Message = browserbridge.Diagnostic(q.Jobs[i].Message)
				}
				if len(q.Jobs[i].Message) > 4096 {
					q.Jobs[i].Message = q.Jobs[i].Message[:4096]
				}
			}
			if err := persist(); err != nil {
				shutdown(err)
				continue
			}
			if result.err != nil && state == "failed" {
				message := result.err.Error()
				if q.Jobs[i].IntakeID != "" {
					message = browserbridge.Diagnostic(message)
				}
				fmt.Fprintf(output, "Job %d failed: %s\n", result.id, message)
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
					case "browser-prepare", "browser-status", "browser-abort", "browser-activate":
						response.JobID, changed, requestErr = browserQueueOperation(&q, path, req, clock())
						if receipt, ok := q.BrowserReceipts[req.Job.IntakeID]; ok {
							response.BrowserStage = receipt.Stage
						}
					case "workers":
						i := find(req.Job.ID)
						control := workerControls[req.Job.ID]
						if i < 0 || q.Jobs[i].State != "running" || requested[req.Job.ID] != "" || control == nil {
							requestErr = errors.New("worker targets require an active download")
						} else if req.Connections < 1 || req.Connections > control.settings.Load().Maximum {
							requestErr = errors.New("worker target must be within the configured connection maximum")
						} else {
							applyAutomatic(automatic.discard(generation, clock()))
							requestErr = control.set(control.settings.Load().Maximum, req.Connections)
							if requestErr == nil {
								generation++
							}
						}
					case "action":
						i := find(req.Job.ID)
						if i < 0 {
							requestErr = errors.New("download no longer exists")
							break
						}
						j := &q.Jobs[i]
						if pendingBrowserJob(q, *j) && req.Action != "remove" {
							requestErr = errors.New("browser handoff is pending; complete it in the extension or remove this entry")
							break
						}
						if requested[j.ID] != "" {
							requestErr = errors.New("download is still stopping; try again shortly")
							break
						}
						if req.Action == "remove" || req.Action == "delete" || req.Action == "discard" {
							if j.State == "running" || j.State == "queued" || (j.State == "resetting" && req.Action != "delete" && req.Action != "discard") {
								requestErr = errors.New("stop the download before removing it")
								break
							}
							if req.Action != "remove" {
								if req.Job.Destination != j.Destination {
									requestErr = errors.New("download destination changed; review the selection again")
									break
								}
								fatal := false
								if req.Action == "delete" {
									fatal, requestErr = deleteQueueFiles(&q, i, path, req.File)
								} else if j.State == "completed" {
									requestErr = errors.New("completed downloads have no partial data to discard")
								} else {
									fatal, requestErr = cleanupQueueJob(&q, i, path, "discard")
								}
								if fatal {
									shutdown(requestErr)
								}
								if requestErr != nil {
									break
								}
							}
							if req.Action != "discard" {
								delete(layouts, j.ID)
								q.Jobs = append(q.Jobs[:i], q.Jobs[i+1:]...)
							}
							changed = true
							break
						}
						next := queueActionState(j.State, req.Action)
						if next == "" {
							requestErr = errors.New("action is not available for this download state")
						} else if cancel := active[j.ID]; cancel != nil {
							requested[j.ID] = next
							generation++
							cancel()
						} else {
							j.State, j.Message = next, ""
							if req.Action == "retry" || req.Action == "resume" {
								budget.retryOrigin(queueOrigin(j.URL))
								if m := measurements[j.ID]; m != nil {
									if origin := m.origin.Load(); origin != nil {
										budget.retryOrigin(*origin)
									}
								}
							}
							changed = true
						}
					case "initialize":
						if len(active) != 0 {
							requestErr = errors.New("cannot initialize a desktop session while downloads are active")
							break
						}
						if req.Management != nil {
							requestErr = budget.configure(*req.Management)
							if requestErr != nil {
								break
							}
							generation++
						}
						if !desktopInitialized {
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
							previousPolicy, _ := budget.snapshot()
							if req.Management != nil {
								requestErr = budget.configure(*req.Management)
							}
							if requestErr == nil {
								applyAutomatic(automatic.discard(generation, clock()))
								maxActive, connections = req.MaxActive, req.Connections
								if req.Management != nil && req.Management.Mode == "automatic" && previousPolicy.Policy.Mode != "automatic" {
									automatic.files = 1
								}
								automatic.files = min(automatic.files, maxActive)
								policy, _ := budget.snapshot()
								for _, control := range workerControls {
									target := connections
									if policy.Policy.Mode == "automatic" {
										target = min(control.settings.Load().Target, connections)
										if previousPolicy.Policy.Mode != "automatic" {
											target = 1
										}
									}
									_ = control.set(connections, target)
								}
								generation++
							}
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
				response.Jobs = desktopEntries(q.Jobs, speeds, liveSegmentLayouts(layouts, progressWriters), clock())
				management, jobRequests := budget.snapshot()
				if management.Policy.Mode == "automatic" {
					management.FileTarget = automatic.files
					management.Draining = len(active) > automatic.files
					management.Status, management.Reason = automatic.status, automatic.reason
				}
				management.Hardware = &resources.hardware
				bufferState := buffers.snapshot()
				management.Buffers = &bufferState
				response.Management = &management
				for i := range response.Jobs {
					entry := &response.Jobs[i]
					entry.BrowserPending = pendingBrowserJob(q, entry.queueJob)
					if w := resources.storage[entry.ID]; w != nil {
						view := w.view
						entry.Storage = &view
					}
					if m := measurements[entry.ID]; m != nil {
						snapshot := m.snapshot()
						entry.Measurements = &snapshot
					}
					if observation, ok := observations[entry.ID]; ok && observation.Generation == generation {
						entry.Observation = &observation
					}
					requestState := jobRequests[entry.ID]
					entry.Requests = &requestState
					if management.Policy.Mode == "automatic" && (entry.State == "running" || entry.State == "queued") {
						entry.AutomaticReason = automatic.reason
						if budget.originCooling(queueOrigin(entry.URL), clock()) || requestState.Cooldown {
							entry.AutomaticReason = "Waiting for the server cooldown; retry manually if requested"
						}
					}
					if entry.State == "running" {
						switch {
						case snapshotRetrying(entry.Measurements):
							entry.Activity = "Waiting for retry"
						case requestState.Active == 0 && requestState.Waiting > 0:
							entry.Activity = "Waiting for a connection slot"
						case len(entry.Parts) == 0:
							entry.Activity = "Checking transfer method"
						case partsVerifying(entry.Parts):
							entry.Activity = "Verifying saved data"
						}
					}
				}
				// Remove transient measurements for entries removed from history.
				for id := range measurements {
					if find(id) < 0 {
						delete(measurements, id)
						delete(samplers, id)
						delete(observations, id)
					}
				}

				keepOrigins := map[string]bool{}
				for _, job := range q.Jobs {
					keepOrigins[queueOrigin(job.URL)] = true
					if m := measurements[job.ID]; m != nil {
						if origin := m.origin.Load(); origin != nil {
							keepOrigins[*origin] = true
						}
					}
				}
				budget.pruneOrigins(keepOrigins)
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
				if pendingBrowserJob(q, q.Jobs[i]) && command != "remove" {
					fmt.Fprintln(output, "Complete the browser handoff in the extension before starting this job.")
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
				next := queueActionState(j.State, command)
				if command == "remove" {
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
	if budget, ok := ctx.Value(bufferBudgetKey{}).(*bufferBudget); ok {
		buffers, err := budget.acquire(ctx)
		if err != nil {
			return err
		}
		defer buffers.release()
		ctx = context.WithValue(ctx, transferBufferKey{}, buffers)
	}
	client := defaultDownloadClient()
	if budget, ok := ctx.Value(budgetContextKey{}).(*requestBudget); ok {
		client.Transport = &requestTransport{base: client.Transport, budget: budget, job: j.ID, metrics: measurementsFrom(ctx)}
	}
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
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyCancellation(wrapped.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}
