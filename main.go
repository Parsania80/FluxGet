package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fluxget", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rawURL := flags.String("url", "", "Public HTTP or HTTPS file URL")
	destination := flags.String("out", "", "Destination file (must not already exist)")
	directory := flags.String("dir", "", "Download directory (default: Downloads)")
	name := flags.String("name", "", "Download filename (default: server or URL filename)")
	checksum := flags.String("sha256", "", "Expected SHA-256 checksum (optional)")
	resume := flags.Bool("resume", false, "Resume a safely checkpointed download")
	list := flags.Bool("list", false, "List saved download jobs")
	queue := flags.Bool("queue", false, "Manage a persistent download queue")
	suggestOutput := flags.Bool("suggest-output", false, "Print the URL-based output path without downloading")
	maxActive := flags.Int("max-active", 5, "Maximum simultaneous queue downloads (1–64)")
	connections := flags.Int("connections", 4, "Maximum connections per download (1–16)")
	liveFile := flags.String("live-speed-file", "", "Desktop live speed settings file")
	liveKey := flags.String("live-speed-key", "", "Desktop download identifier")
	speedLimit := flags.Int64("speed-limit", 0, "Maximum download speed in KiB/s (0 is unlimited)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: fluxget -url URL [-dir DIR] [-name NAME] | fluxget -resume -out FILE | fluxget -list | fluxget -queue [-max-active N]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *connections < 1 || *connections > 16 {
		fmt.Fprintln(stderr, "-connections must be between 1 and 16.")
		return 2
	}
	if *speedLimit < 0 || *speedLimit > 1<<30 {
		fmt.Fprintln(stderr, "-speed-limit must be between 0 and 1073741824 KiB/s.")
		return 2
	}
	var limiter *speedLimiter
	if *speedLimit > 0 {
		limiter = &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: *speedLimit * 1024}
	}
	if *queue {
		if *list || *suggestOutput || *resume || *rawURL != "" || *destination != "" || *directory != "" || *name != "" || *checksum != "" || flags.NArg() != 0 || *maxActive < 1 || *maxActive > 64 {
			fmt.Fprintln(stderr, "Use -queue alone, optionally with -max-active from 1 to 64.")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if limiter != nil {
			ctx = context.WithValue(ctx, limiterContextKey{}, limiter)
		}
		if *liveFile != "" {
			ctx = context.WithValue(ctx, liveSpeedKey{}, *liveFile)
		}
		if err := runQueue(ctx, os.Stdin, stdout, *maxActive, *connections); err != nil {
			fmt.Fprintf(stderr, "Queue failed: %v\n", err)
			return 1
		}
		return 0
	}
	if *maxActive != 5 {
		fmt.Fprintln(stderr, "-max-active requires -queue.")
		return 2
	}
	if *suggestOutput {
		if *rawURL == "" || *list || *resume || *destination != "" || *checksum != "" || flags.NArg() != 0 {
			fmt.Fprintln(stderr, "Use -suggest-output with -url and optional -dir and -name.")
			return 2
		}
		path, err := defaultDestination(*rawURL, *directory, *name)
		if err != nil {
			fmt.Fprintf(stderr, "Choose destination: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "%q\n", path)
		return 0
	}
	if *list && *rawURL == "" && *destination == "" && *directory == "" && *name == "" && *checksum == "" && !*resume && flags.NArg() == 0 {
		if err := listJobs(stdout); err != nil {
			fmt.Fprintf(stderr, "List failed: %v\n", err)
			return 1
		}
		return 0
	}
	if *list || (!*resume && *rawURL == "") || (*resume && (*rawURL != "" || *destination == "" || *directory != "" || *name != "")) || (*destination != "" && (*directory != "" || *name != "")) || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Use -list alone, or provide either -url for a new download or -resume with -out for a saved download; -out cannot be combined with -dir or -name.")
		flags.Usage()
		return 2
	}
	pendingName := !*resume && *destination == "" && *name == ""
	if !*resume && *destination == "" {
		path, err := defaultDestination(*rawURL, *directory, *name)
		if err != nil {
			fmt.Fprintf(stderr, "Choose destination: %v\n", err)
			return 2
		}
		*destination = path
	}

	// Ctrl+C cancels the request and lets the download function close its files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := defaultDownloadClient()
	limitClient(client, limiter)
	if *liveFile != "" {
		limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: *liveFile, key: *liveKey}})
	}
	defer client.CloseIdleConnections()
	var written int64
	first := true
	err := withRetries(ctx, time.Second, func() error {
		var n int64
		var attemptErr error
		if *resume {
			n, attemptErr = resumeDownloadWithLimit(ctx, client, *destination, *checksum, stderr, *connections)
		} else if first || pendingName {
			var resolve func(http.Header)
			if pendingName {
				resolve = func(header http.Header) {
					if filename := serverFilename(header); filename != "" {
						*destination = filepath.Join(filepath.Dir(*destination), filename)
					}
					pendingName = false
				}
			}
			n, attemptErr = downloadFreshNamed(ctx, client, *rawURL, destination, *checksum, stderr, *connections, resolve)
		} else {
			n, attemptErr = runDownloadAttempt(ctx, client, queueJob{URL: *rawURL, Destination: *destination}, *checksum, stderr, *connections)
		}
		first = false
		if n > written {
			written = n
		}
		return attemptErr
	})
	if err != nil {
		fmt.Fprintf(stderr, "Download failed after %d bytes: %v\n", written, err)
		return 1
	}
	fmt.Fprintf(stdout, "Downloaded %d bytes to %s\n", written, *destination)
	return 0
}
