package main

import (
	"fmt"
	"strings"
)

type queueEntry struct {
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
		if jobVisible(category, entry.displayState()) {
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
