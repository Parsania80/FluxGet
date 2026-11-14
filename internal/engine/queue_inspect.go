package engine

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// inspectQueueJob reports local metadata only; resume still validates the data and server.
func inspectQueueJob(output io.Writer, j queueJob) {
	if j.Media != nil {
		fmt.Fprintf(output, "YouTube media: %s · %s · %s\n", j.Media.Video.ID, j.Media.Selection.Container, j.State)
		fmt.Fprintln(output, "Saved media is managed by this queue; resume checks tools, selected formats and local file integrity.")
		return
	}
	fmt.Fprintf(output, "Job %d: %s, output %q\n", j.ID, j.State, j.Destination)
	if j.State == "resetting" && j.Cleanup != nil {
		fmt.Fprintf(output, "Cleanup interrupted: repeat %s %d.\n", j.Cleanup.Action, j.ID)
		return
	}
	path, err := checkpointLocation(j.Destination)
	if err != nil {
		fmt.Fprintln(output, "Recovery details unavailable.")
		return
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Lstat(j.Destination + ".part"); err == nil {
			fmt.Fprintln(output, "Partial file has no checkpoint; manual inspection required.")
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(output, "Partial file cannot be inspected.")
		} else if j.State == "completed" {
			fmt.Fprintln(output, "No checkpoint remains for this completed job.")
		} else {
			fmt.Fprintln(output, "No checkpoint or partial file; a new transfer can start if the destination is absent.")
		}
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		fmt.Fprintln(output, "Checkpoint cannot be inspected; manual inspection required.")
		return
	}
	c, err := loadCheckpoint(path)
	if err != nil || c.URL != j.URL || c.Destination != j.Destination {
		fmt.Fprintln(output, "Checkpoint is invalid or does not match this job; manual inspection required.")
		return
	}
	total := "?"
	if c.Total != nil {
		total = fmt.Sprint(*c.Total)
	}
	fmt.Fprintf(output, "Saved: %d/%s bytes", c.Completed, total)
	if c.parallel() {
		done := 0
		for _, chunk := range c.Chunks {
			if chunk.SHA256 != "" {
				done++
			}
		}
		fmt.Fprintf(output, "; %d/%d chunks", done, len(c.Chunks))
	}
	fmt.Fprintln(output)
	state := savedJobState(path, c)
	switch state {
	case "active":
		fmt.Fprintln(output, "Transfer is active; saved bytes may lag live progress.")
	case "ready-to-reconcile":
		fmt.Fprintln(output, "Publication needs reconciliation; resume or retry this job.")
	case "interrupted":
		fmt.Fprintln(output, "Saved data may be resumed after local and server validation.")
		switch j.State {
		case "paused":
			fmt.Fprintf(output, "Use resume %d, then start if the queue is stopped.\n", j.ID)
		case "failed", "canceled":
			fmt.Fprintf(output, "Use retry %d, then start if the queue is stopped.\n", j.ID)
		case "queued":
			fmt.Fprintln(output, "Use start to run the queue.")
		}
	case "interrupted (restart required)":
		fmt.Fprintln(output, "Saved data cannot be resumed under current rules; a fresh restart may be needed.")
		if c.PartialToken == "" {
			fmt.Fprintln(output, "Automatic partial removal is unavailable for this checkpoint; manual inspection required.")
		} else {
			fmt.Fprintf(output, "After pausing or canceling, restart %d can remove the verified partial and queue a fresh download.\n", j.ID)
		}
	default:
		fmt.Fprintln(output, "Saved files need manual inspection before recovery.")
	}
}
