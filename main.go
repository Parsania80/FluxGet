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
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: fluxget -url URL -out FILE")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *rawURL == "" || *destination == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Provide both -url and -out, without positional arguments.")
		flags.Usage()
		return 2
	}

	// Ctrl+C cancels the request and lets the download function close its files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	written, err := download(ctx, &http.Client{}, *rawURL, *destination)
	if err != nil {
		fmt.Fprintf(stderr, "Download failed after %d bytes: %v\n", written, err)
		return 1
	}
	fmt.Fprintf(stdout, "Downloaded %d bytes to %s\n", written, *destination)
	return 0
}
