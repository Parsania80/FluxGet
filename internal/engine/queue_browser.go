package engine

import (
	"errors"
	"strings"
	"time"

	"github.com/Parsania80/FluxGet/browserbridge"
)

// Receipts survive list removal, so a lost browser acknowledgment cannot create
// a second download. Bounded retention fails closed instead of forgetting IDs.
type browserReceipt struct {
	Later   bool   `json:"later,omitempty"`
	JobID   int    `json:"job_id"`
	Stage   string `json:"stage"`
	Kind    string `json:"kind"`
	Expires int64  `json:"expires"`
}

func validIntakeID(id string) bool {
	p := strings.Split(id, ":")
	return len(p) == 3 && browserbridge.ValidBrowser(p[0]) && browserbridge.ValidToken(p[1]) && browserbridge.ValidToken(p[2])
}
func pendingBrowserJob(q queueData, j queueJob) bool {
	r, ok := q.BrowserReceipts[j.IntakeID]
	return j.IntakeID != "" && ok && r.Stage == "pending"
}
func browserQueueOperation(q *queueData, path string, req desktopRequest, now time.Time) (int, bool, error) {
	key := req.Job.IntakeID
	if !validIntakeID(key) {
		return 0, false, errors.New("invalid browser handoff ID")
	}
	receipt, exists := q.BrowserReceipts[key]
	index := -1
	if exists {
		for i, j := range q.Jobs {
			if j.ID == receipt.JobID && j.IntakeID == key {
				index = i
				break
			}
		}
	}
	switch req.Operation {
	case "browser-prepare":
		if exists {
			if index < 0 || receipt.Stage != "pending" {
				return receipt.JobID, false, errors.New("browser handoff was already handled")
			}
			j := q.Jobs[index]
			if j.URL != req.Job.URL || j.Destination != req.Job.Destination {
				return 0, false, errors.New("handoff identifier was reused for another file")
			}
			if now.Unix() > receipt.Expires {
				return receipt.JobID, false, errors.New("browser handoff expired; use the browser")
			}
			return receipt.JobID, false, nil
		}
		if len(q.BrowserReceipts) >= 2048 {
			return 0, false, errors.New("browser handoff history is full")
		}
		if req.Action != "link" && req.Action != "capture" {
			return 0, false, errors.New("invalid browser handoff kind")
		}
		if err := browserbridge.ValidateURL(req.Job.URL); err != nil {
			return 0, false, err
		}
		if err := backupQueueNamed(path, ".before-browser"); err != nil {
			return 0, false, err
		}
		input := req.Job
		input.IntakeID = ""
		input.State = "paused"
		input.Message = "Waiting for the browser handoff; no transfer has started"
		id, err := desktopAdd(q, input, false)
		if err != nil {
			return 0, false, err
		}
		q.Jobs[len(q.Jobs)-1].IntakeID = key
		if q.BrowserReceipts == nil {
			q.BrowserReceipts = map[string]browserReceipt{}
		}
		q.BrowserReceipts[key] = browserReceipt{JobID: id, Stage: "pending", Kind: req.Action, Later: req.BrowserLater, Expires: now.Add(60 * time.Second).Unix()}
		q.Version = max(q.Version, 3)
		return id, true, nil
	case "browser-status":
		if exists && receipt.Stage == "pending" && index < 0 {
			return 0, false, errors.New("pending browser destination was removed")
		}
		if !exists {
			return 0, false, errors.New("browser handoff not found")
		}
		return receipt.JobID, false, nil
	case "browser-abort":
		if !exists {
			return 0, false, nil
		}
		if receipt.Stage != "pending" {
			return receipt.JobID, false, nil
		}
		if index >= 0 {
			q.Jobs[index].State = "canceled"
			q.Jobs[index].Message = "Kept in the browser; FluxGet did not start this download"
		}
		receipt.Stage = "aborted"
		q.BrowserReceipts[key] = receipt
		return receipt.JobID, true, nil
	case "browser-activate":
		if !exists || index < 0 {
			return 0, false, errors.New("accepted browser download is unavailable")
		}
		if receipt.Stage == "active" {
			return receipt.JobID, false, nil
		}
		if receipt.Stage != "pending" {
			return 0, false, errors.New("browser handoff was abandoned")
		}
		if receipt.Kind == "capture" && req.Action != "browser-canceled" || receipt.Kind == "link" && req.Action != "link" {
			return 0, false, errors.New("browser ownership is not confirmed")
		}
		receipt.Stage = "active"
		q.BrowserReceipts[key] = receipt
		j := &q.Jobs[index]
		j.State = "paused"
		j.Message = "Browser handoff saved. Select Resume when ready."
		if req.Job.State == "queued" && !receipt.Later && now.Unix() <= receipt.Expires {
			j.State = "queued"
			j.Message = ""
		}
		return receipt.JobID, true, nil
	}
	return 0, false, errors.New("unknown browser operation")
}
