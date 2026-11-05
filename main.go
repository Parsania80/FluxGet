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
	maxActive := flags.Int("max-active", 5, "Maximum simultaneous queue downloads (1–64)")
	connections := flags.Int("connections", 4, "Maximum connections per download (1–16)")
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
	if *queue {
		if *list || *resume || *rawURL != "" || *destination != "" || *directory != "" || *name != "" || *checksum != "" || flags.NArg() != 0 || *maxActive < 1 || *maxActive > 64 {
			fmt.Fprintln(stderr, "Use -queue alone, optionally with -max-active from 1 to 64.")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
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
