package engine

import (
	"errors"
	"fmt"
	"os"
)

// File deletion stays with the queue owner after all job writers have stopped.
func deleteQueueFiles(q *queueData, index int, queuePath string, expected *outputRemoval) (bool, error) {
	j := q.Jobs[index]
	if j.Media != nil && j.State == "completed" {
		if err := cleanupMediaRecord(j, false); err != nil {
			return false, err
		}
	}
	if j.State != "completed" {
		return cleanupQueueJob(q, index, queuePath, "discard")
	}
	path := j.Destination
	if j.OutputPath != "" {
		path = j.OutputPath
	}
	for _, other := range q.Jobs {
		if other.ID != j.ID && (other.Destination == path || other.OutputPath == path) {
			return false, errors.New("another download uses this output file")
		}
	}
	cp, err := checkpointLocation(j.Destination)
	if err != nil {
		return false, err
	}
	lock, err := lockJobRecord(cp + ".lock")
	if err != nil {
		return false, err
	}
	defer lock.Close()
	for _, extra := range []string{j.Destination + ".part", cp} {
		if err := requireAbsent(extra); err != nil {
			return false, fmt.Errorf("saved transfer data needs attention before deleting the completed file: %w", err)
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || expected == nil || expected.Path != path {
		return false, errors.New("output differs from the deletion confirmation; review the file again")
	}
	file, err := openPartial(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	identity, _, err := identifyPartial(file)
	opened, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(info, opened) || identity.Device != expected.Device || identity.Inode != expected.Inode || opened.Size() != expected.Size || opened.ModTime().UnixNano() != expected.Modified {
		return false, errors.New("output changed since the deletion confirmation; file kept")
	}
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(opened, named) || named.Size() != expected.Size || named.ModTime().UnixNano() != expected.Modified {
		return false, errors.New("output changed during deletion; file kept")
	}
	return false, os.Remove(path)
}

func queueActionState(state, action string) string {
	switch action {
	case "pause":
		if state == "queued" || state == "running" {
			return "paused"
		}
	case "resume":
		if state == "paused" {
			return "queued"
		}
	case "retry":
		if state == "failed" || state == "canceled" {
			return "queued"
		}
	case "cancel":
		if state == "queued" || state == "running" || state == "paused" || state == "failed" {
			return "canceled"
		}
	}
	return ""
}
