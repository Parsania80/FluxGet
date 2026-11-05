package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type job struct {
	URL      string       `json:"url"`
	Path     string       `json:"path"`
	State    string       `json:"state"`
	Message  string       `json:"message,omitempty"`
	Progress *float64     `json:"progress,omitempty"`
	Replace  *replacement `json:"replace,omitempty"`
}

type replacement struct {
	Path     string `json:"path"`
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

func historyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fluxget", "desktop-history.json"), nil
}

func loadHistory() ([]job, error) {
	path, err := historyPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, errors.New("desktop history is too large")
	}
	var jobs []job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("read desktop history: %w", err)
	}
	for i := range jobs {
		if jobs[i].State == "Downloading" {
			jobs[i].State = "Interrupted"
		}
	}
	return jobs, nil
}

func saveHistory(jobs []job) error {
	path, err := historyPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(jobs)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return errors.New("desktop history exceeds its size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".desktop-history-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func copyPath(path string) (string, error) {
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	for i := 1; i <= 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			if _, err := os.Lstat(candidate + ".part"); errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			} else if err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not find an available filename")
}

func completeReplacement(j *job) error {
	if j.Replace == nil {
		return nil
	}
	if !filepath.IsAbs(j.Path) || !filepath.IsAbs(j.Replace.Path) || j.Path == j.Replace.Path || filepath.Dir(j.Path) != filepath.Dir(j.Replace.Path) {
		return errors.New("replacement files must be distinct and in the same directory")
	}
	staged, err := os.Lstat(j.Path)
	if err != nil || !staged.Mode().IsRegular() {
		return errors.New("completed replacement is unavailable or is not a regular file")
	}
	if !replacementMatches(j.Replace) {
		return errors.New("the original file changed; the new copy was kept separately")
	}
	if err := os.Rename(j.Path, j.Replace.Path); err != nil {
		return fmt.Errorf("replace file; the new copy was kept separately: %w", err)
	}
	j.Path = j.Replace.Path
	j.Replace = nil
	return nil
}
