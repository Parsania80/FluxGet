package media

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Parsania80/FluxGet/internal/platformcmd"
	"github.com/Parsania80/FluxGet/networkproxy"
)

//go:embed tools.lock.json
var toolsLock []byte

type toolArtifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Name   string `json:"name"`
}

type toolLock struct {
	Versions  map[string]string                  `json:"versions"`
	Platforms map[string]map[string]toolArtifact `json:"platforms"`
}

// DefaultToolsManifest identifies this exact tool set; an upgrade cannot replace
// tools still referenced by a saved download from an earlier release.
func DefaultToolsManifest() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(toolsLock)
	return filepath.Join(cache, "fluxget", "media-tools", hex.EncodeToString(digest[:8]), "tools.json"), nil
}

// SetupTools downloads only the embedded, checksum-locked upstream artifacts.
// The caller must obtain explicit user consent before invoking it.
func SetupTools(ctx context.Context, proxy networkproxy.Config) (string, error) {
	manifest, err := DefaultToolsManifest()
	if err != nil {
		return "", err
	}
	var lock toolLock
	if json.Unmarshal(toolsLock, &lock) != nil || runtime.GOARCH != "amd64" {
		return "", errors.New("YouTube tool setup supports Linux and Windows amd64")
	}
	artifacts := lock.Platforms[runtime.GOOS+"-"+runtime.GOARCH]
	if len(artifacts) != 3 {
		return "", errors.New("YouTube tool setup is unavailable on this platform")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := networkproxy.Configure(transport, proxy); err != nil {
		return "", err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Minute}
	return installTools(ctx, filepath.Dir(manifest), artifacts, lock.Versions, client)
}

func installTools(ctx context.Context, destination string, artifacts map[string]toolArtifact, versions map[string]string, client *http.Client) (string, error) {
	manifest := filepath.Join(destination, "tools.json")
	if _, err := os.Lstat(destination); err == nil {
		tools, err := LoadTools(manifest)
		if err == nil {
			_, err = New(tools)
		}
		if err != nil {
			return "", errors.New("existing YouTube tool folder is incomplete; choose another manifest or remove that folder before setup")
		}
		return manifest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".setup-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	paths := make(map[string]string)
	for _, tool := range []string{"yt_dlp", "deno", "ffmpeg"} {
		artifact := artifacts[tool]
		if artifact.URL == "" || len(artifact.SHA256) != 64 {
			return "", errors.New("invalid media tool lock")
		}
		archive, err := fetchTool(ctx, client, stage, artifact)
		if err != nil {
			return "", fmt.Errorf("%s: %w", tool, err)
		}
		names := []string{tool}
		if tool == "ffmpeg" {
			names = []string{"ffmpeg", "ffprobe"}
		}
		for _, name := range names {
			filename := name + suffix
			if name == "yt_dlp" {
				filename = "yt-dlp" + suffix
			}
			if err := unpackTool(ctx, archive, artifact.Name, filename, filepath.Join(stage, filename)); err != nil {
				return "", fmt.Errorf("%s: %w", tool, err)
			}
			paths[name] = filepath.Join(destination, filename)
		}
		if err := os.Remove(archive); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(struct {
		Versions map[string]string `json:"versions"`
		Tools    map[string]string `json:"tools"`
	}{versions, paths}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "tools.json"), append(data, '\n'), 0600); err != nil {
		return "", err
	}
	if err := os.Rename(stage, destination); err != nil {
		return "", errors.New("cannot publish YouTube tool folder; another setup may have finished first")
	}
	return manifest, nil
}

const maxToolSize = 256 << 20

func fetchTool(ctx context.Context, client *http.Client, directory string, artifact toolArtifact) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", errors.New("invalid tool download address")
	}
	request.Header.Set("User-Agent", "FluxGet-media-setup")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("tool download failed; check the connection or proxy and retry")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tool server returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp(directory, ".archive-")
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, maxToolSize+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || n > maxToolSize || hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return "", errors.New("tool download is incomplete, oversized or failed SHA-256 verification")
	}
	return file.Name(), nil
}

func unpackTool(ctx context.Context, archive, archiveName, filename, destination string) error {
	var reader io.ReadCloser
	if strings.HasSuffix(archiveName, ".zip") {
		bundle, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer bundle.Close()
		for _, entry := range bundle.File {
			if path.Base(entry.Name) != filename {
				continue
			}
			if reader != nil || !entry.Mode().IsRegular() || entry.UncompressedSize64 > maxToolSize {
				if reader != nil {
					reader.Close()
				}
				return errors.New("unexpected tool archive contents")
			}
			reader, err = entry.Open()
			if err != nil {
				return err
			}
		}
		if reader == nil {
			return errors.New("tool executable missing from archive")
		}
	} else if strings.HasSuffix(archiveName, ".tar.xz") {
		list := platformcmd.CommandContext(ctx, "/usr/bin/tar", "-tJf", archive)
		data, err := list.Output()
		if err != nil {
			return errors.New("cannot read tool archive; install tar and xz-utils")
		}
		member := ""
		for _, name := range strings.Split(string(data), "\n") {
			if path.Base(name) == filename {
				if member != "" {
					return errors.New("duplicate executable in tool archive")
				}
				member = name
			}
		}
		if member == "" {
			return errors.New("tool executable missing from archive")
		}
		command := platformcmd.CommandContext(ctx, "/usr/bin/tar", "-xJOf", archive, "--", member)
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			return err
		}
		command.Stdout = output
		err = command.Run()
		closeErr := output.Close()
		if err != nil {
			return errors.New("cannot extract tool executable")
		}
		return closeErr
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		reader = file
	}
	defer reader.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	n, err := io.Copy(output, io.LimitReader(reader, maxToolSize+1))
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxToolSize {
		return errors.New("tool executable exceeds size limit")
	}
	return ctx.Err()
}
