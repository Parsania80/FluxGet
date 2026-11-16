package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Parsania80/FluxGet/browserbridge"
)

func browserDownloadCanceled(message string) browserbridge.Response {
	response := browserbridge.Failure(message)
	response.Stage = "canceled"
	return response
}

func (b *browserIntegration) prepareDownload(ctx context.Context, request browserbridge.Request, draft downloadDraft, respond func(browserbridge.Response)) {
	v := b.view
	if ctx.Err() != nil {
		respond(browserDownloadCanceled("Handoff timed out; retry in FluxGet"))
		return
	}
	if path := v.downloadConflict(draft, true); path != "" {
		b.promptWindow = v.resolveDuplicate(draft, path, true, func(choice duplicateChoice) {
			b.promptWindow = nil
			switch {
			case choice.Draft != nil:
				b.admitDownload(ctx, request, *choice.Draft, respond)
			case choice.ExistingID > 0:
				b.continueDownload(ctx, choice.ExistingID, respond)
			default:
				respond(browserDownloadCanceled("Extra download canceled; existing file kept"))
			}
		})
		return
	}
	b.admitDownload(ctx, request, draft, respond)
}

func (b *browserIntegration) admitDownload(ctx context.Context, request browserbridge.Request, draft downloadDraft, respond func(browserbridge.Response)) {
	v := b.view
	if ctx.Err() != nil {
		respond(browserDownloadCanceled("Handoff timed out; retry in FluxGet"))
		return
	}
	v.workResult(func(q *queueClient) (desktopResponse, error) {
		policy := b.store.Snapshot()
		if !policy.Allowed(request) || policy.Excluded(request.URL) || (request.Kind == "capture" && policy.Mode == "manual") {
			return desktopResponse{}, fmt.Errorf("capture was disabled")
		}
		return q.request(ctx, desktopRequest{Operation: "browser-prepare", Action: request.Kind, BrowserLater: draft.Later,
			Job: queueEntry{Proxy: draft.Proxy, URL: draft.URL, Destination: draft.Destination, State: "paused", IntakeID: request.Key()}})
	}, func(response desktopResponse) {
		if err := v.saveDraftReplacement(draft, response.JobID); err != nil {
			respond(browserDownloadCanceled("Could not save replacement details; original file kept"))
			return
		}
		respond(browserbridge.Response{Version: browserbridge.Version, OK: true, JobID: response.JobID, Stage: response.BrowserStage, Filename: filepath.Base(draft.Destination)})
	}, func(err error) {
		// Choosing FluxGet never silently starts a second copy in the browser,
		// including a conflict created between the snapshot and reservation.
		respond(browserDownloadCanceled(err.Error()))
	})
}

func (b *browserIntegration) continueDownload(ctx context.Context, id int, respond func(browserbridge.Response)) {
	v := b.view
	v.workResult(func(q *queueClient) (desktopResponse, error) {
		current, err := q.request(ctx, desktopRequest{Operation: "snapshot"})
		if err != nil {
			return current, err
		}
		for _, entry := range current.Jobs {
			if entry.ID != id || entry.BrowserPending {
				continue
			}
			switch entry.State {
			case "paused":
				return q.request(ctx, desktopRequest{Operation: "action", Action: "resume", Job: queueEntry{ID: id}})
			case "running", "queued", "completed":
				return current, nil
			}
		}
		return current, fmt.Errorf("the existing download changed; select it in FluxGet")
	}, func(response desktopResponse) {
		v.filters.SetSelected("All downloads")
		v.selected = fmt.Sprintf("queue:%d", id)
		v.status.SetText("Continuing the existing download.")
		respond(browserDownloadCanceled("Continuing the existing FluxGet download; extra request canceled"))
	}, func(err error) { respond(browserDownloadCanceled(err.Error())) })
}
