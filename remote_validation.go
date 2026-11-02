package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// validateRemote checks a saved representation, but does not validate local data.
func validateRemote(ctx context.Context, client *http.Client, c checkpoint) error {
	if ctx == nil || client == nil {
		return errors.New("a context and HTTP client are required")
	}
	if err := c.validate(); err != nil {
		return err
	}
	if c.Total == nil || c.Completed <= 0 || c.Completed >= *c.Total || !strongETag(c.ETag) || c.ResolvedURL == "" {
		return errors.New("resume requires an incomplete file with a known size, resolved URL, and strong ETag")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return fmt.Errorf("prepare validation request: %w", err)
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("If-Range", c.ETag)
	req.Header.Set("Accept-Encoding", "identity")
	response, err := safeHTTPClient(client).Do(req)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return fmt.Errorf("validate remote file: %w", err)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != c.ResolvedURL ||
		response.StatusCode != http.StatusPartialContent || len(response.Header.Values("ETag")) != 1 || response.Header.Get("ETag") != c.ETag ||
		len(response.Header.Values("Content-Range")) != 1 ||
		response.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-0/%d", *c.Total) ||
		len(response.Header.Values("Content-Encoding")) > 1 || (response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) {
		return errors.New("remote file does not match saved checkpoint or does not support safe ranges")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2))
	if err != nil {
		return fmt.Errorf("read remote validation response: %w", err)
	}
	if len(body) != 1 {
		return errors.New("remote range response has an invalid length")
	}
	return nil
}

func strongETag(tag string) bool {
	if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for i := 1; i < len(tag)-1; i++ {
		b := tag[i]
		if b != 0x21 && (b < 0x23 || b == 0x7f) {
			return false
		}
	}
	return true
}
