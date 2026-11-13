package downloadcategory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Folder classifies the final filename, without trusting a URL or MIME claim.
func Folder(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".m4a", ".aac", ".flac", ".wav", ".ogg", ".opus", ".wma", ".aiff", ".alac", ".mid", ".midi":
		return "Music"
	case ".mp4", ".mkv", ".webm", ".avi", ".mov", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg", ".3gp", ".ts":
		return "Videos"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".svg", ".ico", ".heic", ".avif":
		return "Pictures"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".txt", ".rtf", ".csv", ".epub", ".mobi", ".md":
		return "Documents"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".zst", ".tgz", ".tbz2", ".txz":
		return "Archives"
	case ".exe", ".msi", ".msix", ".appx", ".deb", ".rpm", ".appimage", ".dmg", ".pkg", ".apk":
		return "Programs"
	default:
		return "Other"
	}
}

// Destination creates only the required category inside an existing base folder.
func Destination(base, name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", errors.New("choose a plain download filename")
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return "", errors.New("choose an existing download folder")
	}
	directory := filepath.Join(base, Folder(name))
	if err = os.Mkdir(directory, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create download category: %w", err)
	}
	info, err = os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("download category must be a real folder, not a link or file")
	}
	return filepath.Join(directory, name), nil
}
