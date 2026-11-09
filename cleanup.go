package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
)

// cleanupQueueJob records the user's intent before removing either owned file.
func cleanupQueueJob(q *queueData, index int, queuePath, action string) (bool, error) {
	return cleanupQueueJobWithHook(q, index, queuePath, action, nil)
}

func cleanupQueueJobWithHook(q *queueData, index int, queuePath, action string, hook func(string)) (bool, error) {
	j := &q.Jobs[index]
	if j.State == "resetting" && (j.Cleanup == nil || j.Cleanup.Action != action) {
		return false, errors.New("repeat the original cleanup command")
	}
	if err := requireAbsent(j.Destination); err != nil {
		return false, fmt.Errorf("final destination must be absent: %w", err)
	}
	path, err := checkpointLocation(j.Destination)
	if err != nil {
		return false, err
	}
	cpInfo, err := optionalFileInfo(path)
	if err != nil {
		return false, err
	}
	partInfo, err := optionalFileInfo(j.Destination + ".part")
	if err != nil {
		return false, err
	}
	if cpInfo != nil && !cpInfo.Mode().IsRegular() {
		return false, errors.New("checkpoint is not a regular file")
	}
	if partInfo != nil && !partInfo.Mode().IsRegular() {
		return false, errors.New("partial is not a regular file")
	}
	if partInfo != nil && cpInfo == nil {
		return false, errors.New("partial has no checkpoint; ownership cannot be verified")
	}
	if cpInfo != nil && partInfo == nil && j.State != "resetting" {
		return false, errors.New("partial is missing; ownership cannot be verified")
	}
	var partial *os.File
	if partInfo != nil {
		partial, err = os.OpenFile(j.Destination+".part", os.O_RDWR, 0)
		if err != nil {
			return false, err
		}
		defer partial.Close()
		opened, e := partial.Stat()
		if e != nil || !os.SameFile(partInfo, opened) {
			return false, errors.New("partial changed while opening")
		}
		lock, e := lockJob(path+".lock", partial)
		if e != nil {
			return false, e
		}
		defer lock.Close()
	} else {
		lock, e := lockJobRecord(path + ".lock")
		if e != nil {
			return false, e
		}
		defer lock.Close()
	}
	if cpInfo != nil {
		c, err := loadCheckpoint(path)
		if err != nil {
			return false, err
		}
		if c.URL != j.URL || c.Destination != j.Destination || c.PartialIdentity == nil {
			return false, errors.New("checkpoint does not prove ownership of this partial")
		}
		if c.State == "ready" {
			return false, errors.New("completed publication requires reconciliation, not cleanup")
		}
		checksum, err := hex.DecodeString(c.ExpectedSHA256)
		if c.ExpectedSHA256 != "" && (err != nil || len(checksum) != sha256.Size) {
			return false, errors.New("invalid saved checksum")
		}
		if j.ExpectedSHA256 != "" && c.ExpectedSHA256 != "" && !strings.EqualFold(j.ExpectedSHA256, c.ExpectedSHA256) {
			return false, errors.New("saved checksum differs from queue")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		digest := sha256.Sum256(raw)
		fingerprint := hex.EncodeToString(digest[:])
		named, err := os.Lstat(path)
		if err != nil || !os.SameFile(cpInfo, named) {
			return false, errors.New("checkpoint changed during cleanup")
		}
		if j.State == "resetting" {
			if j.Cleanup.CheckpointHash != fingerprint || j.Cleanup.Identity != *c.PartialIdentity {
				return false, errors.New("checkpoint differs from recorded cleanup intent")
			}
		} else {
			if partial == nil {
				return false, errors.New("partial is missing")
			}
			if err := verifyOwnedPartial(partial, c); err != nil {
				return false, err
			}
			j.State = "resetting"
			j.Cleanup = &cleanupIntent{Action: action, Identity: *c.PartialIdentity, CheckpointHash: fingerprint}
			if j.ExpectedSHA256 == "" {
				j.ExpectedSHA256 = c.ExpectedSHA256
			}
			if err := saveQueue(queuePath, *q); err != nil {
				return true, fmt.Errorf("save cleanup intent: %w", err)
			}
			if hook != nil {
				hook("intent")
			}
		}
		if partial != nil {
			if err := verifyOwnedPartial(partial, c); err != nil {
				return false, err
			}
			named, err := os.Lstat(c.Partial)
			opened, e := partial.Stat()
			if err != nil || e != nil || !named.Mode().IsRegular() || !os.SameFile(named, opened) {
				return false, errors.New("partial changed during cleanup")
			}
			if err := requireAbsent(j.Destination); err != nil {
				return false, err
			}
			if err := os.Remove(c.Partial); err != nil {
				return false, fmt.Errorf("remove partial: %w", err)
			}
			if hook != nil {
				hook("partial")
			}
		}
		if err := requireAbsent(j.Destination); err != nil {
			return false, err
		}
		named, err = os.Lstat(path)
		if err != nil || !os.SameFile(cpInfo, named) {
			return false, errors.New("checkpoint changed during cleanup")
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return false, err
		}
		digest = sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != j.Cleanup.CheckpointHash {
			return false, errors.New("checkpoint changed during cleanup")
		}
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("remove checkpoint: %w", err)
		}
		if hook != nil {
			hook("checkpoint")
		}
	}
	if action == "restart" {
		j.State = "queued"
	} else {
		j.State = "canceled"
	}
	j.Cleanup = nil
	if err := saveQueue(queuePath, *q); err != nil {
		return true, fmt.Errorf("save cleanup result: %w", err)
	}
	return false, nil
}

func verifyOwnedPartial(partial *os.File, c checkpoint) error {
	if !checkPartialToken(partial, c.PartialToken) {
		return errors.New("partial ownership marker is missing or changed")
	}
	identity, links, err := identifyPartial(partial)
	if err != nil || links != 1 || !reflect.DeepEqual(identity, c.PartialIdentity) {
		return errors.New("partial identity or link count does not match checkpoint")
	}
	if c.parallel() {
		for _, chunk := range c.Chunks {
			if chunk.savedLength() == 0 {
				continue
			}
			digest, err := chunkPrefixDigest(partial, chunk)
			if err != nil || !strings.EqualFold(digest, chunk.savedDigest()) {
				return errors.New("saved chunk does not match partial")
			}
		}
	} else if c.Completed > 0 {
		hash := sha256.New()
		if _, err := io.CopyN(hash, io.NewSectionReader(partial, 0, c.Completed), c.Completed); err != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), c.PrefixSHA256) {
			return errors.New("saved prefix does not match partial")
		}
	}
	return nil
}
