package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/Parsania80/FluxGet/internal/downloadcategory"
	"github.com/Parsania80/FluxGet/media"
)

func youtubeFormats(v media.Video, format string, audioOnly bool) ([]media.Format, []media.Format) {
	videos, audios := v.OutputFormats(strings.ToLower(format), audioOnly)
	sort.SliceStable(videos, func(i, j int) bool {
		if videos[i].Height != videos[j].Height {
			return videos[i].Height > videos[j].Height
		}
		if videos[i].FPS != videos[j].FPS {
			return videos[i].FPS > videos[j].FPS
		}
		return videos[i].ID < videos[j].ID
	})
	sort.SliceStable(audios, func(i, j int) bool {
		if audios[i].AudioBitrate != audios[j].AudioBitrate {
			return audios[i].AudioBitrate > audios[j].AudioBitrate
		}
		return audios[i].ID < audios[j].ID
	})
	return videos, audios
}

func youtubeQuality(f media.Format) string {
	quality := "Unknown resolution"
	if f.Height > 0 {
		quality = fmt.Sprintf("%dp", f.Height)
	}
	if f.FPS > 0 {
		quality += fmt.Sprintf(" · %g fps", f.FPS)
	}
	return fmt.Sprintf("%s · %s · ID %s", quality, f.VideoCodec, f.ID)
}

func youtubeAudio(f media.Format) string {
	language := f.Language
	if language == "" {
		language = "Language unspecified"
	}
	bitrate := ""
	if f.AudioBitrate > 0 {
		bitrate = fmt.Sprintf(" · %g kbps", f.AudioBitrate)
	}
	return fmt.Sprintf("%s · %s%s · ID %s", language, f.AudioCodec, bitrate, f.ID)
}

func youtubeSize(v media.Video, s media.Selection, unit string) string {
	var total int64
	estimated := false
	for _, id := range []string{s.VideoFormat, s.AudioFormat} {
		if id == "" {
			continue
		}
		found := false
		for _, f := range v.Formats {
			if f.ID != id {
				continue
			}
			size := f.Size
			if size == 0 {
				size, estimated = f.EstimatedSize, true
			}
			if size <= 0 || size > math.MaxInt64-total {
				return "Source transfer size unknown. Final file size may differ."
			}
			total += size
			found = true
			break
		}
		if !found {
			return "Source transfer size unknown."
		}
	}
	if total == 0 {
		return "Source transfer size unknown."
	}
	prefix := "Source transfer: "
	if estimated {
		prefix = "Estimated source transfer: "
	}
	return prefix + dataNumber(float64(total), unit) + " " + displayUnit(unit) + ". Final file size may differ."
}

func youtubeFilename(title, id string) string {
	var name []rune
	for _, r := range title {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			r = ' '
		}
		name = append(name, r)
		if len(name) == 40 {
			break
		}
	}
	value := strings.Trim(strings.Join(strings.Fields(string(name)), " "), ". ")
	if value == "" {
		value = "YouTube"
	}
	// The ID makes the default stable and avoids reserved Windows basenames.
	return value + " [" + id + "]"
}

func youtubeDestination(folder, name, extension string, sortFiles bool) (string, error) {
	if !filepath.IsAbs(folder) {
		return "", errors.New("Choose an existing destination folder.")
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return "", errors.New("Choose an existing destination folder.")
	}
	if name == "" || len(name) > 240 || name == "." || name == ".." || strings.TrimRight(name, ". ") != name || strings.ContainsAny(name, `/\:*?"<>|`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("Enter a plain filename without an extension or special path characters.")
	}
	stem := strings.TrimRight(strings.ToUpper(strings.SplitN(name, ".", 2)[0]), " ")
	deviceNumber := ""
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		deviceNumber = stem[3:]
	}
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || deviceNumber != "" && strings.Contains("|1|2|3|4|5|6|7|8|9|¹|²|³|", "|"+deviceNumber+"|") {
		return "", errors.New("Choose a filename that is valid on Windows and Linux.")
	}
	switch extension {
	case "mp4", "webm", "mkv", "m4a", "opus", "mp3":
	default:
		return "", media.ErrSelection
	}
	path := filepath.Join(folder, name+"."+extension)
	if sortFiles {
		path, err = downloadcategory.Destination(folder, name+"."+extension)
		if err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("This filename is unavailable. Choose another name; existing files are kept.")
	}
	return path, nil
}
