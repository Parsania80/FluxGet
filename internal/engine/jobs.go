package engine

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// listJobs reports a snapshot; it never changes or removes saved records.
func listJobs(output io.Writer) error {
	dir, err := jobDirectory()
	if err != nil {
		return fmt.Errorf("locate saved jobs: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		_, err = fmt.Fprintln(output, "No saved jobs.")
		return err
	}
	if err != nil {
		return fmt.Errorf("read saved jobs: %w", err)
	}
	writer := bufio.NewWriter(output)
	found := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found = true
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			fmt.Fprintf(writer, "needs-attention\t?\t%q\n", entry.Name())
			continue
		}
		c, err := loadCheckpoint(path)
		id := sha256.Sum256([]byte(c.Destination))
		if err != nil || entry.Name() != hex.EncodeToString(id[:])+".json" {
			fmt.Fprintf(writer, "needs-attention\t?\t%q\n", entry.Name())
			continue
		}
		state := savedJobState(path, c)
		total := "?"
		if c.Total != nil {
			total = fmt.Sprint(*c.Total)
		}
		fmt.Fprintf(writer, "%s\t%d/%s bytes\t%q\n", state, c.Completed, total, c.Destination)
	}
	if !found {
		fmt.Fprintln(writer, "No saved jobs.")
	}
	return writer.Flush()
}

func savedJobState(path string, c checkpoint) string {
	active, err := jobActive(path + ".lock")
	if err != nil {
		return "needs-attention"
	}
	if active {
		return "active"
	}
	if c.State == "ready" {
		partial, partialErr := optionalFileInfo(c.Partial)
		final, finalErr := optionalFileInfo(c.Destination)
		digest, digestErr := hex.DecodeString(c.PrefixSHA256)
		if partialErr != nil || finalErr != nil || (partial == nil && final == nil) ||
			(partial != nil && !partial.Mode().IsRegular()) || (final != nil && !final.Mode().IsRegular()) ||
			(partial != nil && final != nil && !os.SameFile(partial, final)) ||
			c.Total == nil || c.Completed != *c.Total || digestErr != nil || len(digest) != sha256.Size {
			return "needs-attention"
		}
		return "ready-to-reconcile"
	}
	info, err := os.Lstat(c.Partial)
	if err != nil || !info.Mode().IsRegular() || info.Size() < c.Completed {
		return "needs-attention"
	}
	if c.parallel() {
		for _, chunk := range c.Chunks {
			if chunk.savedLength() > 0 && info.Size() < chunk.Start+chunk.savedLength() {
				return "needs-attention"
			}
		}
		return "interrupted"
	}
	if c.Completed > 0 {
		digest, err := hex.DecodeString(c.PrefixSHA256)
		if err != nil || len(digest) != sha256.Size {
			return "needs-attention"
		}
	}
	if c.Completed <= 0 || (c.Total != nil && c.Completed > *c.Total) {
		return "interrupted (restart required)"
	}
	return "interrupted"
}
