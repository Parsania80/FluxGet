// Package networkproxy defines explicit per-transfer proxy routing. Disabled
// means Direct, independent of environment proxy or bypass variables.
package networkproxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

var ErrInvalid = errors.New("enter an HTTP, HTTPS or SOCKS5 proxy address with host and port, such as socks5://127.0.0.1:10808; credentials and subscription links are not supported")

// Normalize returns a canonical address without exposing rejected input in an
// error. Local v2ray clients supply an HTTP or SOCKS listening address here.
func (c Config) Normalize() (Config, error) {
	c.URL = strings.TrimSpace(c.URL)
	if c.URL == "" && !c.Enabled {
		return c, nil
	}
	if len(c.URL) > 2048 || strings.ContainsAny(c.URL, "\r\n\t") {
		return Config{}, ErrInvalid
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return Config{}, ErrInvalid
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return Config{}, ErrInvalid
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || strings.ContainsAny(host, " /\\%") {
		return Config{}, ErrInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, ErrInvalid
	}
	if u.Scheme == "socks5" {
		u.Scheme = "socks5h"
	} // Remote DNS in both backends.
	u.Host = net.JoinHostPort(strings.ToLower(host), strconv.Itoa(n))
	u.Path = ""
	u.RawPath = ""
	c.URL = u.String()
	return c, nil
}

// Configure must be called before the transport is used. No environment lookup
// or direct fallback is installed, including for localhost destinations.
func Configure(transport *http.Transport, config Config) error {
	c, err := config.Normalize()
	if err != nil {
		return err
	}
	transport.Proxy = nil
	if c.Enabled {
		u, _ := url.Parse(c.URL)
		transport.Proxy = http.ProxyURL(u)
	}
	return nil
}

// Argument is suitable for yt-dlp's --proxy. Empty explicitly selects Direct.
func (c Config) Argument() (string, error) {
	c, err := c.Normalize()
	if err != nil {
		return "", err
	}
	if !c.Enabled {
		return "", nil
	}
	return c.URL, nil
}
