package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/platformcmd"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func downloadArgs(url, directory, name string) []string {
	args := []string{"-url", url}
	if directory != "" {
		args = append(args, "-dir", directory)
	}
	if name != "" {
		args = append(args, "-name", name)
	}
	return args
}

func download(ctx context.Context, args []string, update func(string)) error {
	executable, err := cliPath()
	if err != nil {
		return err
	}
	return runDownload(ctx, executable, args, update)
}

func cliPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "fluxget"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(executable), name), nil
}

func discoverSavedJobs(existing []job) ([]job, error) {
	executable, err := cliPath()
	if err != nil {
		return existing, err
	}
	output, err := platformcmd.Command(executable, "-list").Output()
	if err != nil {
		return existing, fmt.Errorf("list saved downloads: %w", err)
	}
	return mergeSavedJobs(existing, string(output)), nil
}

func mergeSavedJobs(existing []job, output string) []job {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || !(strings.HasPrefix(parts[0], "interrupted") || parts[0] == "ready-to-reconcile") {
			continue
		}
		path, err := strconv.Unquote(parts[2])
		if err != nil || !filepath.IsAbs(path) {
			continue
		}
		found := false
		for _, j := range existing {
			if j.Path == path {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, job{Path: path, State: "Interrupted", Message: parts[0] + ": " + parts[1]})
		}
	}
	return existing
}

func runDownload(ctx context.Context, executable string, args []string, update func(string)) error {
	cmd := platformcmd.CommandContext(ctx, executable, append(args, "-cancel-on-stdin-close")...)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	cmd.Cancel = input.Close
	cmd.WaitDelay = 15 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return fmt.Errorf("start FluxGet CLI: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			update(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			reader.CloseWithError(err)
		}
		done <- scanner.Err()
	}()
	err = cmd.Wait()
	writer.Close()
	scanErr := <-done
	reader.Close()
	if scanErr != nil {
		return fmt.Errorf("read download status: %w", scanErr)
	}
	return err
}
