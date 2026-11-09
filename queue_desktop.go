package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Desktop requests are processed by the queue owner, never by transfer workers.
type desktopRequest struct {
	ID          int            `json:"id"`
	Operation   string         `json:"operation"`
	Job         queueJob       `json:"job"`
	MaxActive   int            `json:"max_active"`
	Connections int            `json:"connections"`
	Management  *requestPolicy `json:"management,omitempty"`
	Action      string         `json:"action,omitempty"`
	File        *outputRemoval `json:"file,omitempty"`
}

type outputRemoval struct {
	Path           string
	Device, Inode  uint64
	Size, Modified int64
}

type desktopResponse struct {
	Revision   uint64          `json:"revision"`
	ID         int             `json:"id"`
	JobID      int             `json:"job_id,omitempty"`
	Jobs       []desktopEntry  `json:"jobs,omitempty"`
	Error      string          `json:"error,omitempty"`
	Management *budgetSnapshot `json:"management,omitempty"`
}

func backupQueue(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path+".before-unified", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path + ".before-unified")
		return err
	}
	return closeErr
}

func queueDestinationAvailable(q queueData, destination string, except int) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errors.New("choose an absolute output path")
	}
	for _, j := range q.Jobs {
		if j.ID != except && j.Destination == destination {
			return errors.New("this filename is already used by another download; choose another name")
		}
	}
	for _, path := range []string{destination, destination + ".part"} {
		if err := requireAbsent(path); err != nil {
			return err
		}
	}
	cp, err := checkpointLocation(destination)
	if err != nil {
		return err
	}
	return requireAbsent(cp)
}

func desktopAdd(q *queueData, input queueJob, adopt bool) (int, error) {
	if !filepath.IsAbs(input.Destination) || filepath.Clean(input.Destination) != input.Destination {
		return 0, errors.New("choose an absolute output path")
	}
	if adopt {
		cp, err := checkpointLocation(input.Destination)
		if err != nil {
			return 0, err
		}
		if c, err := loadCheckpoint(cp); err == nil {
			busy, e := jobActive(cp + ".lock")
			if e != nil || busy || c.Destination != input.Destination || (input.URL != "" && c.URL != input.URL) {
				return 0, errors.New("saved download is active or does not match its history")
			}
			input.URL, input.ExpectedSHA256 = c.URL, c.ExpectedSHA256
			if c.Total != nil {
				input.Progress = fmt.Sprintf("Downloaded %d/%d bytes", c.Completed, *c.Total)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("cannot import saved download: %w", err)
		}
	}
	if !validQueueURL(input.URL) {
		return 0, errors.New("provide a public HTTP or HTTPS link without credentials")
	}
	if input.ExpectedSHA256 != "" {
		sum, err := hex.DecodeString(input.ExpectedSHA256)
		if err != nil || len(sum) != sha256.Size {
			return 0, errors.New("invalid expected checksum")
		}
	}
	if input.SavedProgress != nil && (*input.SavedProgress < 0 || *input.SavedProgress > 1) {
		return 0, errors.New("invalid saved progress")
	}
	for _, j := range q.Jobs {
		if j.Destination == input.Destination {
			if adopt && j.URL == input.URL {
				return j.ID, nil
			}
			return 0, errors.New("this filename is already used by another download")
		}
	}
	if !adopt {
		if err := queueDestinationAvailable(*q, input.Destination, 0); err != nil {
			return 0, err
		}
	}
	if adopt {
		switch input.State {
		case "completed", "failed", "canceled":
		default:
			input.State = "paused"
		}
		input.AutomaticName = false
	} else if input.State != "paused" {
		input.State = "queued"
	}
	input.ID, input.Cleanup, input.Conflict = q.NextID, nil, ""
	input.OutputPath = ""
	q.NextID++
	q.Jobs = append(q.Jobs, input)
	return input.ID, nil
}

func decodeDesktopRequest(data string) (desktopRequest, error) {
	var req desktopRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil || req.ID <= 0 || len(req.Job.Message) > 4096 || len(req.Job.Progress) > 1024 {
		return req, errors.New("invalid desktop request")
	}
	return req, nil
}
