package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func backupDesktopFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
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

func migrateDownloads(ctx context.Context, q *queueClient, history []job, settings *speedSettings) ([]job, []queueEntry, error) {
	path, err := historyPath()
	if err != nil {
		return history, nil, err
	}
	for _, p := range []string{path, settings.path} {
		if err = backupDesktopFile(p); err != nil {
			return history, nil, fmt.Errorf("back up download metadata: %w", err)
		}
	}
	response, err := q.request(ctx, desktopRequest{Operation: "initialize"})
	if err != nil {
		return history, nil, err
	}
	for i := range history {
		h := &history[i]
		if h.Archived || h.QueueID > 0 || !filepath.IsAbs(h.Path) || h.State == "Already exists" {
			continue
		}
		state := strings.ToLower(h.State)
		entry := queueEntry{URL: h.URL, Destination: h.Path, State: state, Message: h.Message}
		entry.SavedProgress = h.Progress
		adopted, e := q.request(ctx, desktopRequest{Operation: "adopt", Job: entry})
		if e != nil {
			continue
		} // Keep unimportable history visible without touching its files.
		h.QueueID = adopted.JobID
		oldKey, newKey := fmt.Sprintf("download:%d", i), fmt.Sprintf("queue:%d", h.QueueID)
		if limit := settings.Overrides[oldKey]; limit > 0 {
			if current := settings.Overrides[newKey]; current == 0 || limit < current {
				settings.Overrides[newKey] = limit
			}
		}
		response = adopted
	}
	if err = settings.save(); err != nil {
		return history, response.Jobs, err
	}
	if err = saveHistory(history); err != nil {
		return history, response.Jobs, err
	}
	return history, response.Jobs, nil
}
