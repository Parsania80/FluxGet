package main

import (
	"fmt"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
	"strings"
)

type queueEntry struct {
	Media           *media.Job           `json:"media,omitempty"`
	MediaPhase      string               `json:"media_phase,omitempty"`
	Proxy           *networkproxy.Config `json:"proxy,omitempty"`
	BrowserPending  bool                 `json:"browser_pending,omitempty"`
	IntakeID        string               `json:"browser_intake_id,omitempty"`
	AutomaticReason string               `json:"automatic_reason,omitempty"`
	Activity        string               `json:"activity,omitempty"`
	ID              int                  `json:"id"`
	URL             string               `json:"url,omitempty"`
	State           string               `json:"state"`
	Destination     string               `json:"destination"`
	AutomaticName   bool                 `json:"automatic_name,omitempty"`
	ExpectedSHA256  string               `json:"expected_sha256,omitempty"`
	Message         string               `json:"message,omitempty"`
	Progress        string               `json:"progress,omitempty"`
	Conflict        string               `json:"conflict,omitempty"`
	OutputPath      string               `json:"output_path,omitempty"`
	SavedProgress   *float64             `json:"saved_progress,omitempty"`
	Speed           *float64             `json:"speed_bytes_per_second,omitempty"`
	Segments        []downloadSegment    `json:"segments,omitempty"`
	TransferMode    string               `json:"transfer_mode,omitempty"`
	ConnectionLimit int                  `json:"connection_limit,omitempty"`
	WorkerTarget    int                  `json:"worker_target,omitempty"`
	WorkerReason    string               `json:"worker_reason,omitempty"`
	Requests        *jobBudgetSnapshot   `json:"requests,omitempty"`
	Measurements    *measurementSnapshot `json:"measurements,omitempty"`
}

func (j queueEntry) output() string {
	if j.OutputPath != "" {
		return j.OutputPath
	}
	return j.Destination
}

func downloadState(state string) string {
	switch strings.ToLower(state) {
	case "queued":
		return "Waiting"
	case "running", "downloading":
		return "Downloading"
	case "paused", "interrupted":
		return "Paused"
	case "completed":
		return "Completed"
	case "failed":
		return "Failed"
	case "canceled", "stopped":
		return "Canceled"
	case "resetting":
		return "Needs attention"
	default:
		return state
	}
}

func (j queueEntry) displayState() string {
	if j.Media != nil && j.State == "running" {
		switch j.MediaPhase {
		case "checking":
			return "Checking"
		case "processing":
			return "Processing"
		case "saving", "publishing":
			return "Verifying"
		case "budget", "limit":
			return "Waiting"
		default:
			return "Downloading"
		}
	}
	if j.State == "running" && j.Activity != "" {
		switch j.Activity {
		case "Checking transfer method":
			return "Checking"
		case "Verifying saved data":
			return "Verifying"
		default:
			return "Waiting"
		}
	}
	if j.State == "running" && j.Requests != nil && j.Requests.Active == 0 && j.Requests.Waiting > 0 {
		return "Waiting"
	}
	return downloadState(j.State)
}

func jobVisible(category, state string) bool {
	state = downloadState(state)
	switch category {
	case "All downloads":
		return true
	case "Finished", "Completed":
		return state == "Completed"
	case "Unfinished":
		return state != "Completed"
	case "Downloading", "Waiting", "Paused", "Failed":
		return state == category
	default:
		return false
	}
}

type downloadRow struct {
	Key          string
	Entry        queueEntry
	HistoryIndex int
}

func downloadRows(entries []queueEntry, history []job, category string) []downloadRow {
	rows := make([]downloadRow, 0, len(entries)+len(history))
	for _, entry := range entries {
		index := -1
		for i, h := range history {
			if h.QueueID == entry.ID {
				index = i
				break
			}
		}
		if index >= 0 && history[index].State == "Needs attention" {
			entry.State, entry.Message = "Needs attention", history[index].Message
		}
		filterState := entry.displayState()
		if entry.Media != nil && entry.State == "running" && filterState != "Waiting" {
			filterState = "Downloading"
		}
		if jobVisible(category, filterState) {
			rows = append(rows, downloadRow{fmt.Sprintf("queue:%d", entry.ID), entry, index})
		}
	}
	for i, h := range history {
		if h.QueueID != 0 || h.Archived {
			continue
		}
		entry := queueEntry{URL: h.URL, Destination: h.Path, State: h.State, Message: h.Message}
		entry.SavedProgress = h.Progress
		if jobVisible(category, entry.displayState()) {
			rows = append(rows, downloadRow{fmt.Sprintf("download:%d", i), entry, i})
		}
	}
	return rows
}

func downloadDetails(j queueEntry, unit string) string {
	switch downloadState(j.State) {
	case "Waiting":
		if j.AutomaticReason != "" {
			if strings.HasPrefix(j.AutomaticReason, "Waiting for the server") {
				return j.AutomaticReason
			}
			return "Waiting for Automatic to admit this download"
		}
		return "Waiting for a download slot"
	case "Paused":
		if j.Progress != "" {
			return "Saved progress · " + downloadProgress(j.Progress, unit)
		}
		if j.SavedProgress != nil {
			return fmt.Sprintf("%.1f%% saved · ready when you resume", *j.SavedProgress*100)
		}
		return "Ready when you resume"
	case "Completed":
		return "Download complete"
	case "Canceled":
		return "Canceled · files kept"
	case "Downloading":
		if j.Progress != "" {
			detail := downloadProgress(j.Progress, unit)
			if j.Speed != nil {
				detail += remainingTimeWithSpeed(j.Progress, *j.Speed)
			}
			return detail
		}
		return "Connecting…"
	default:
		if j.Message != "" {
			return j.Message
		}
		return "Select the download for details"
	}
}
