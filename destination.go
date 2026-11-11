package main

import (
	"bufio"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

func defaultDestination(rawURL, directory, name string) (string, error) {
	if !validQueueURL(rawURL) {
		return "", errors.New("provide a valid HTTP or HTTPS URL without credentials")
	}
	if name == "" {
		u, _ := url.Parse(rawURL)
		if u.Path == "" || strings.HasSuffix(u.Path, "/") {
			name = "download"
		} else {
			name = path.Base(u.Path)
		}
	}
	if !safeFilename(name) {
		return "", errors.New("filename must be a single safe filename")
	}
	if directory == "" {
		var err error
		directory, err = downloadsDirectory()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return "", fmt.Errorf("create Downloads directory: %w", err)
		}
	}
	return filepath.Join(directory, name), nil
}

func safeFilename(name string) bool {
	if runtime.GOOS == "windows" && !safeWindowsFilename(name) {
		return false
	}
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\") && strings.IndexFunc(name, unicode.IsControl) < 0
}

func serverFilename(header http.Header) string {
	values := header.Values("Content-Disposition")
	if len(values) != 1 {
		return ""
	}
	_, params, err := mime.ParseMediaType(values[0])
	if err != nil || !safeFilename(params["filename"]) {
		return ""
	}
	return params["filename"]
}

func downloadsDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	config, err := os.UserConfigDir()
	if err == nil {
		file, err := os.Open(filepath.Join(config, "user-dirs.dirs"))
		if err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				value, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "XDG_DOWNLOAD_DIR=")
				if !ok {
					continue
				}
				value, err = strconv.Unquote(value)
				if err != nil {
					break
				}
				if value == "$HOME" {
					value = home
				} else if strings.HasPrefix(value, "$HOME/") {
					value = filepath.Join(home, strings.TrimPrefix(value, "$HOME/"))
				}
				if filepath.IsAbs(value) && !strings.Contains(value, "$") {
					return filepath.Clean(value), nil
				}
				break
			}
		}
	}
	return filepath.Join(home, "Downloads"), nil
}

// Windows treats device basenames and alternate streams specially even when
// an extension is present. Refuse them before any file is opened.
func safeWindowsFilename(name string) bool {
	if strings.ContainsAny(name, `<>:"/\|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
	if (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && len([]rune(suffix)) == 1 && strings.Contains("123456789¹²³", suffix) {
		return false
	}
	return true
}
