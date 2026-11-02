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
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: fluxget -url URL -out FILE | fluxget -resume -out FILE")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *destination == "" || (!*resume && *rawURL == "") || (*resume && *rawURL != "") || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Provide -out and either -url for a new download or -resume for a saved download.")
		flags.Usage()
		return 2
	}

	// Ctrl+C cancels the request and lets the download function close its files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var written int64
	var err error
	if *resume {
		written, err = resumeDownload(ctx, &http.Client{}, *destination, *checksum, stderr)
	} else {
		written, err = download(ctx, &http.Client{}, *rawURL, *destination, *checksum, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Download failed after %d bytes: %v\n", written, err)
		return 1
	}
	fmt.Fprintf(stdout, "Downloaded %d bytes to %s\n", written, *destination)
	return 0
}
