package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
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
	checksum := flags.String("sha256", "", "Expected SHA-256 checksum (optional)")
	resume := flags.Bool("resume", false, "Resume a safely checkpointed download")
	list := flags.Bool("list", false, "List saved download jobs")
	queue := flags.Bool("queue", false, "Manage a persistent download queue")
	maxActive := flags.Int("max-active", 5, "Maximum simultaneous queue downloads (1–64)")
	connections := flags.Int("connections", 4, "Maximum connections per download (1–16)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: fluxget -url URL -out FILE | fluxget -resume -out FILE | fluxget -list | fluxget -queue [-max-active N]")
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
		if *list || *resume || *rawURL != "" || *destination != "" || *checksum != "" || flags.NArg() != 0 || *maxActive < 1 || *maxActive > 64 {
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
	if *list && *rawURL == "" && *destination == "" && *checksum == "" && !*resume && flags.NArg() == 0 {
		if err := listJobs(stdout); err != nil {
			fmt.Fprintf(stderr, "List failed: %v\n", err)
			return 1
		}
		return 0
	}
	if *list || *destination == "" || (!*resume && *rawURL == "") || (*resume && *rawURL != "") || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Use -list alone, or provide -out and either -url for a new download or -resume for a saved download.")
		flags.Usage()
		return 2
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
		} else if first {
			n, attemptErr = downloadFresh(ctx, client, *rawURL, *destination, *checksum, stderr, *connections)
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
