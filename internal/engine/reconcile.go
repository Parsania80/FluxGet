package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// reconcilePublication only removes a partial file proven to be the published inode.
func reconcilePublication(ctx context.Context, path string, c checkpoint, expected string) (int64, error) {
	digest, err := hex.DecodeString(c.PrefixSHA256)
	if err != nil || len(digest) != sha256.Size || c.State != "ready" || c.Total == nil || c.Completed != *c.Total {
		return 0, errors.New("final publication checkpoint is invalid")
	}
	partialInfo, err := optionalFileInfo(c.Partial)
	if err != nil {
		return 0, err
	}
	finalInfo, err := optionalFileInfo(c.Destination)
	if err != nil {
		return 0, err
	}
	if partialInfo == nil && finalInfo == nil {
		return 0, errors.New("completed file and partial file are both missing")
	}
	anchor := c.Partial
	anchorInfo := partialInfo
	if anchorInfo == nil {
		anchor, anchorInfo = c.Destination, finalInfo
	}
	if !anchorInfo.Mode().IsRegular() || (finalInfo != nil && !finalInfo.Mode().IsRegular()) ||
		(partialInfo != nil && finalInfo != nil && !os.SameFile(partialInfo, finalInfo)) {
		return 0, errors.New("publication files conflict; all files kept")
	}
	file, err := openPartial(anchor, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open publication file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(anchorInfo, opened) || opened.Size() != c.Completed {
		return 0, errors.New("publication file has changed; all files kept")
	}
	lock, err := lockJob(path+".lock", file)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	current, err := loadCheckpoint(path)
	if err != nil || !reflect.DeepEqual(current, c) {
		return 0, errors.New("checkpoint changed while acquiring ownership")
	}
	partialNow, err := optionalFileInfo(c.Partial)
	if err != nil {
		return 0, err
	}
	finalNow, err := optionalFileInfo(c.Destination)
	if err != nil {
		return 0, err
	}
	if (partialInfo == nil) != (partialNow == nil) || (finalInfo == nil) != (finalNow == nil) ||
		(partialNow != nil && !os.SameFile(opened, partialNow)) || (finalNow != nil && !os.SameFile(opened, finalNow)) {
		return 0, errors.New("publication files changed while acquiring ownership")
	}
	hash := sha256.New()
	if _, err := copyTransfer(ctx, hash, file, true); err != nil {
		return 0, fmt.Errorf("verify publication file: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, c.PrefixSHA256) || (expected != "" && !strings.EqualFold(actual, expected)) {
		return 0, errors.New("publication file checksum does not match; all files kept")
	}
	clearPartialToken(file, c.PartialToken)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	latest, err := file.Stat()
	if err != nil || latest.Size() != opened.Size() || !latest.ModTime().Equal(opened.ModTime()) {
		return 0, errors.New("publication file changed during verification; all files kept")
	}
	if partialNow != nil {
		latestPartial, err := os.Lstat(c.Partial)
		if err != nil || !os.SameFile(opened, latestPartial) {
			return 0, errors.New("partial file changed during verification; all files kept")
		}
	}
	if finalNow != nil {
		latestFinal, err := os.Lstat(c.Destination)
		if err != nil || !os.SameFile(opened, latestFinal) {
			return 0, errors.New("final file changed during verification; all files kept")
		}
	}
	if finalNow == nil {
		if err := publishFile(c.Partial, c.Destination, nil); err != nil {
			return 0, err
		}
	} else if partialNow != nil {
		if err := os.Remove(c.Partial); err != nil {
			return c.Completed, fmt.Errorf("file completed, but partial cleanup failed: %w", err)
		}
	}
	if err := os.Remove(path); err != nil {
		return c.Completed, fmt.Errorf("file completed, but checkpoint cleanup failed: %w", err)
	}
	return c.Completed, nil
}

func optionalFileInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect publication file: %w", err)
	}
	return info, nil
}
