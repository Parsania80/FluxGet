package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type queueProgress struct {
	id     int
	text   string
	source *queueProgressWriter
}
type queueProgressWriter struct {
	id      int
	updates chan<- queueProgress
}

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
	for i := range q.Jobs {
		if q.Jobs[i].State == "running" {
			q.Jobs[i].State = "paused"
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
	progressWriters := make(map[int]*queueProgressWriter)
	active := make(map[int]context.CancelFunc)
	requested := make(map[int]string)
	started, stopping := false, false
	ctxDone := ctx.Done()
	var stopErr error
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
				go func() { done <- queueResult{job.ID, runQueueDownloadWithProgress(workerCtx, job, connections, writer)} }()
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
				fmt.Fprintf(output, "Job %d: %s\n", update.id, update.text)
			}
		case result := <-done:
			active[result.id]()
			delete(active, result.id)
			delete(progressWriters, result.id)
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
			case "help":
				fmt.Fprintln(output, "add URL OUTPUT | import OUTPUT | list | inspect ID | start | pause ID | resume ID | cancel ID | retry ID | restart ID | discard ID | remove ID | quit")
			case "list":
				for _, j := range q.Jobs {
					fmt.Fprintf(output, "%d\t%s\t%q\n", j.ID, j.State, j.Destination)
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
			case "add", "import":
				raw, dest := "", arg
				if command == "add" {
					raw, dest, _ = strings.Cut(arg, " ")
					dest = strings.TrimSpace(dest)
				}
				if strings.TrimSpace(dest) == "" {
					fmt.Fprintln(output, "Invalid URL or destination.")
					continue
				}
				dest, err = filepath.Abs(dest)
				if err != nil || arg == "" || (command == "add" && (!validQueueURL(raw) || strings.TrimSpace(dest) == "")) {
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
				if command == "import" {
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
	defer client.CloseIdleConnections()
	return withRetries(ctx, time.Second, func() error {
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
