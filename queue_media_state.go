package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Parsania80/FluxGet/internal/platformfile"
)

type mediaSavedFile struct {
	Identity partialIdentity `json:"identity"`
	Size     int64           `json:"size"`
	Modified int64           `json:"modified"`
	SHA256   string          `json:"sha256"`
}

type mediaCounter struct{ Downloaded, Total int64 }

type mediaRecovery struct {
	Version   int                       `json:"version"`
	JobHash   string                    `json:"job_hash"`
	Workspace string                    `json:"workspace"`
	Owner     partialIdentity           `json:"owner"`
	Token     string                    `json:"token"`
	Tools     map[string]string         `json:"tools"`
	Clean     bool                      `json:"clean"`
	Ready     string                    `json:"ready,omitempty"`
	Files     map[string]mediaSavedFile `json:"files"`
	Counters  map[string]mediaCounter   `json:"counters"`
}

func mediaRecordPath(destination string) (string, error) {
	path, err := checkpointLocation(destination)
	return path + ".media", err
}

func mediaJobHash(j queueJob) string {
	data, _ := json.Marshal(struct {
		URL, Destination string
		Media            any
	}{j.URL, j.Destination, j.Media})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func saveMediaRecord(path string, state mediaRecovery) error {
	data, err := json.Marshal(state)
	if err != nil || len(data) > 1<<20 {
		return errors.New("media recovery record exceeds its limit")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".fluxget-media-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return platformfile.Replace(file.Name(), path)
}

func loadMediaRecord(path string, j queueJob) (mediaRecovery, error) {
	var state mediaRecovery
	file, err := openPartial(path, os.O_RDONLY, 0)
	if err != nil {
		return state, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &state) != nil {
		return state, errors.New("invalid media recovery record")
	}
	if state.Version != 1 || state.JobHash != mediaJobHash(j) || filepath.Dir(state.Workspace) != filepath.Dir(j.Destination) || !strings.HasPrefix(filepath.Base(state.Workspace), ".fluxget-media-") || !filepath.IsAbs(state.Workspace) || len(state.Files) > 256 || len(state.Token) != 32 || state.Owner.Inode == 0 || len(state.Tools) > 4 || len(state.Counters) > 2 {
		return state, errors.New("media recovery record does not match this download")
	}
	if state.Ready != "" && state.Ready != "asset."+j.Media.Selection.Container {
		return state, errors.New("invalid media publication record")
	}
	for name, saved := range state.Files {
		if !safeMediaAsset(name) || saved.Size < 0 || saved.Identity.Inode == 0 || !validChunkDigest(saved.SHA256) {
			return state, errors.New("invalid media file record")
		}
	}
	for id, counter := range state.Counters {
		if (id != j.Media.Selection.VideoFormat && id != j.Media.Selection.AudioFormat) || id == "" || counter.Downloaded < 0 || counter.Total < 0 {
			return state, errors.New("invalid media counters")
		}
	}
	return state, nil
}

func safeMediaAsset(name string) bool {
	return len(name) <= 240 && filepath.Base(name) == name && strings.HasPrefix(name, "asset.") && !strings.ContainsAny(name, `/\:`) && safeFilename(name) && safeWindowsFilename(name)
}

func createMediaRecord(j queueJob, versions map[string]string) (mediaRecovery, error) {
	state := mediaRecovery{Version: 1, JobHash: mediaJobHash(j), Tools: versions, Clean: true, Files: map[string]mediaSavedFile{}, Counters: map[string]mediaCounter{}}
	workspace, err := os.MkdirTemp(filepath.Dir(j.Destination), ".fluxget-media-")
	if err != nil {
		return state, errors.New("cannot create a private media workspace in the destination folder")
	}
	state.Workspace = workspace
	file, err := openPartial(filepath.Join(workspace, ".owner"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return state, err
	}
	defer file.Close()
	identity, links, err := identifyPartial(file)
	if err != nil || links != 1 {
		return state, errors.New("media workspace ownership is unavailable")
	}
	state.Owner = *identity
	state.Token = createPartialToken(file)
	if state.Token == "" {
		return state, errors.New("media workspace requires filesystem ownership markers")
	}
	return state, file.Sync()
}

func openMediaWorkspace(state mediaRecovery) (*os.Root, error) {
	info, err := os.Lstat(state.Workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("media workspace was replaced; all files kept")
	}
	root, err := os.OpenRoot(state.Workspace)
	if err != nil {
		return nil, err
	}
	fail := func() (*os.Root, error) {
		root.Close()
		return nil, errors.New("media workspace ownership changed; all files kept")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return fail()
	}
	marker, err := openPartial(filepath.Join(state.Workspace, ".owner"), os.O_RDONLY, 0)
	if err != nil {
		return fail()
	}
	defer marker.Close()
	id, links, err := identifyPartial(marker)
	if err != nil || links != 1 || *id != state.Owner || !checkPartialToken(marker, state.Token) {
		return fail()
	}
	return root, nil
}

func mediaFileSignature(ctx context.Context, root *os.Root, name string, allowPublished bool, j queueJob) (mediaSavedFile, error) {
	var result mediaSavedFile
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("unexpected media file; all files kept")
	}
	file, err := openPartial(filepath.Join(root.Name(), name), os.O_RDONLY, 0)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	id, links, idErr := identifyPartial(file)
	if err != nil || idErr != nil || !os.SameFile(info, opened) {
		return result, errors.New("media file changed while opening")
	}
	if links != 1 {
		final, e := os.Lstat(j.Destination)
		if !allowPublished || links != 2 || e != nil || !final.Mode().IsRegular() || !os.SameFile(opened, final) {
			return result, errors.New("media file has unexpected hard links; all files kept")
		}
	}
	hash := sha256.New()
	if _, err := copyTransfer(ctx, hash, file, true); err != nil {
		return result, err
	}
	latest, err := file.Stat()
	named, namedErr := root.Lstat(name)
	if err != nil || namedErr != nil || !os.SameFile(latest, named) || latest.Size() != opened.Size() || !latest.ModTime().Equal(opened.ModTime()) {
		return result, errors.New("media file changed during verification")
	}
	return mediaSavedFile{Identity: *id, Size: opened.Size(), Modified: opened.ModTime().UnixNano(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func snapshotMediaFiles(ctx context.Context, state *mediaRecovery, j queueJob) error {
	root, err := openMediaWorkspace(*state)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	files, err := dir.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(files) > 256 {
		return errors.New("too many media workspace files")
	}
	result := map[string]mediaSavedFile{}
	for _, entry := range files {
		if entry.Name() == ".owner" {
			continue
		}
		if !safeMediaAsset(entry.Name()) {
			return errors.New("unknown workspace content; all files kept")
		}
		saved, err := mediaFileSignature(ctx, root, entry.Name(), false, j)
		if err != nil {
			return err
		}
		result[entry.Name()] = saved
	}
	state.Files = result
	return nil
}

func verifyMediaFiles(ctx context.Context, state mediaRecovery, j queueJob, allowMissing bool) error {
	root, err := openMediaWorkspace(state)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 256 {
		return errors.New("too many media workspace files")
	}
	for _, entry := range entries {
		if entry.Name() == ".owner" {
			continue
		}
		if _, ok := state.Files[entry.Name()]; !ok {
			return errors.New("unknown workspace content; all files kept")
		}
	}
	for name, want := range state.Files {
		if _, err := root.Lstat(name); allowMissing && errors.Is(err, os.ErrNotExist) {
			continue
		}
		got, err := mediaFileSignature(ctx, root, name, state.Ready == name, j)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			return errors.New("saved media data changed; all files kept")
		}
	}
	return nil
}

func publishMedia(ctx context.Context, state mediaRecovery, j queueJob) error {
	if state.Ready == "" || !state.Clean {
		return errors.New("media output is not validated")
	}
	if _, ok := state.Files[state.Ready]; !ok {
		return errors.New("media publication proof is missing")
	}
	if err := verifyMediaFiles(ctx, state, j, false); err != nil {
		return err
	}
	source := filepath.Join(state.Workspace, state.Ready)
	file, err := openPartial(source, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	id, _, idErr := identifyPartial(file)
	proof := state.Files[state.Ready]
	if err != nil || idErr != nil || *id != proof.Identity || opened.Size() != proof.Size || opened.ModTime().UnixNano() != proof.Modified {
		return errors.New("media output changed before publication")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if final, err := os.Lstat(j.Destination); err == nil {
		if final.Mode().IsRegular() && os.SameFile(final, opened) {
			return nil
		}
		return errors.New("destination already exists; media output kept without overwriting")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(source, j.Destination); err != nil {
		return fmt.Errorf("cannot publish media without overwriting; scratch kept: %w", err)
	}
	final, e := os.Lstat(j.Destination)
	latest, e2 := file.Stat()
	if e != nil || e2 != nil || !final.Mode().IsRegular() || !os.SameFile(final, opened) || latest.Size() != proof.Size || latest.ModTime().UnixNano() != proof.Modified {
		return errors.New("media output changed during publication; files kept")
	}
	return nil
}

// Completed jobs retain publication proof until the queue has persisted success.
// Missing owned cache files are allowed so interrupted cleanup is repeatable.
func cleanupMediaRecord(j queueJob, restart bool) error {
	path, err := mediaRecordPath(j.Destination)
	if err != nil {
		return err
	}
	lock, err := lockJobRecord(strings.TrimSuffix(path, ".media") + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := loadMediaRecord(path, j)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if restart && (!state.Clean || verifyMediaFiles(context.Background(), state, j, false) != nil) {
		// Explicit restart preserves suspect data and its provenance, never deletes it.
		data, err := platformfile.ReadFile(path)
		if err != nil {
			return err
		}
		archive, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".kept-*")
		if err != nil {
			return err
		}
		if _, err = archive.Write(data); err == nil {
			err = archive.Sync()
		}
		err = errors.Join(err, archive.Close())
		if err != nil {
			return err
		}
		return os.Remove(path)
	}
	if !state.Clean {
		return errors.New("interrupted media scratch is unvalidated; use Restart to keep it and start afresh")
	}
	if err := verifyMediaFiles(context.Background(), state, j, true); err != nil {
		return err
	}
	root, err := openMediaWorkspace(state)
	if err != nil {
		return err
	}
	defer root.Close()
	for name := range state.Files {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := root.Remove(".owner"); err != nil {
		return err
	}
	root.Close()
	if err := os.Remove(state.Workspace); err != nil {
		return err
	}
	return os.Remove(path)
}
