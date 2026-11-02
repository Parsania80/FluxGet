package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

// checkpoint stores one sequential prefix; it is not proof that resuming is safe.
type checkpoint struct {
	Version        int    `json:"version"`
	URL            string `json:"url"`
	ResolvedURL    string `json:"resolved_url"`
	Destination    string `json:"destination"`
	Partial        string `json:"partial"`
	ETag           string `json:"etag"`
	PrefixSHA256   string `json:"prefix_sha256"`
	ExpectedSHA256 string `json:"expected_sha256"`
	State          string `json:"state"`
	Total          *int64 `json:"total"`
	Completed      int64  `json:"completed"`
}

func (c checkpoint) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("invalid checkpoint URL")
	}
	if c.Version != 1 || !filepath.IsAbs(c.Destination) || c.Partial != c.Destination+".part" || c.Completed < 0 ||
		(c.Total != nil && (*c.Total < 0 || c.Completed > *c.Total)) || (c.State != "" && c.State != "ready") {
		return errors.New("invalid checkpoint metadata")
	}
	return nil
}

// saveCheckpoint requires exclusive ownership and no concurrent writes to partial.
func saveCheckpoint(path string, partial *os.File, c checkpoint) error {
	return saveCheckpointWithHook(path, partial, c, nil)
}

func saveCheckpointWithHook(path string, partial *os.File, c checkpoint, hook func(string)) error {
	if err := c.validate(); err != nil {
		return err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve checkpoint path: %w", err)
	}
	if absolutePath == filepath.Clean(c.Destination) || absolutePath == filepath.Clean(c.Partial) {
		return errors.New("checkpoint path conflicts with download files")
	}
	if partial == nil {
		return errors.New("partial file is required")
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	if len(data) > 1<<20 {
		return errors.New("checkpoint exceeds size limit")
	}
	opened, err := partial.Stat()
	if err != nil {
		return fmt.Errorf("inspect partial file: %w", err)
	}
	named, err := os.Lstat(c.Partial)
	if err != nil {
		return fmt.Errorf("inspect partial path: %w", err)
	}
	if !opened.Mode().IsRegular() || !named.Mode().IsRegular() || !os.SameFile(opened, named) || opened.Size() < c.Completed {
		return errors.New("partial file does not match checkpoint")
	}
	if existing, err := os.Stat(path); err == nil && os.SameFile(opened, existing) {
		return errors.New("checkpoint path refers to partial file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect checkpoint path: %w", err)
	}
	if err := partial.Sync(); err != nil {
		return fmt.Errorf("sync partial file: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fluxget-checkpoint-*")
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if hook != nil {
		hook("checkpoint-written")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if hook != nil {
		hook("checkpoint-synced")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("publish checkpoint: %w", err)
	}
	if hook != nil {
		hook("checkpoint-published")
	}
	return nil
}

func loadCheckpoint(path string) (checkpoint, error) {
	var c checkpoint
	file, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open checkpoint: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return c, fmt.Errorf("read checkpoint: %w", err)
	}
	if len(data) > 1<<20 {
		return c, errors.New("checkpoint exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return checkpoint{}, fmt.Errorf("decode checkpoint: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return checkpoint{}, errors.New("checkpoint contains trailing data")
	}
	if err := c.validate(); err != nil {
		return checkpoint{}, err
	}
	return c, nil
}
