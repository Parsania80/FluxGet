package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

var ErrOutputLimit = errors.New("media tool output exceeded its limit")

// ToolError deliberately excludes command arguments and raw stderr. Both can
// contain signed URLs, cookies from upstream errors, or untrusted metadata.
type ToolError struct {
	ExitCode int
	reason   string
}

func (e *ToolError) Error() string {
	if e.reason != "" {
		return e.reason
	}
	return fmt.Sprintf("media tool failed (exit code %d); check tool compatibility or source availability", e.ExitCode)
}

// Keep only a bounded in-memory tail. Never return raw tool output: even an
// error line may include signed media URLs, headers or proxy credentials.
type toolDiagnostic struct{ tail []byte }

func (d *toolDiagnostic) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 8192
	if len(p) >= limit {
		d.tail = append(d.tail[:0], p[len(p)-limit:]...)
	} else {
		if extra := len(d.tail) + len(p) - limit; extra > 0 {
			d.tail = d.tail[extra:]
		}
		d.tail = append(d.tail, p...)
	}
	return n, nil
}

func (d *toolDiagnostic) reason() string {
	s := strings.ToLower(string(d.tail))
	switch {
	case strings.Contains(s, "timed out"), strings.Contains(s, "timeout error"):
		return "Media connection timed out; check your proxy or network, then retry"
	case strings.Contains(s, "http error 403"), strings.Contains(s, "http error 401"):
		return "The media server denied access; retry or analyze the video again. Your proxy route may be restricted"
	case strings.Contains(s, "http error 429"):
		return "The media server is limiting requests; wait before retrying"
	case strings.Contains(s, "connection refused"), strings.Contains(s, "unable to connect to proxy"):
		return "Could not connect; check that your proxy is running and its address is correct"
	case strings.Contains(s, "requested format is not available"):
		return "The selected format is no longer available; analyze the video again and choose another format"
	case strings.Contains(s, "video unavailable"), strings.Contains(s, "this video is unavailable"), strings.Contains(s, "sign in to confirm"):
		return "This video is unavailable or requires sign-in; only public videos without login are supported"
	case strings.Contains(s, "no space left on device"):
		return "Not enough disk space to save or process this download"
	case strings.Contains(s, "ffmpeg not found"), strings.Contains(s, "ffprobe and ffmpeg not found"):
		return "Media processing tools are missing; check the configured media tools"
	}
	return ""
}

type cappedWriter struct {
	writer io.Writer
	left   int
	cancel context.CancelCauseFunc
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		w.cancel(ErrOutputLimit)
		return 0, ErrOutputLimit
	}
	w.left -= len(p)
	n, err := w.writer.Write(p)
	if err != nil {
		w.cancel(err)
	}
	return n, err
}

// run owns the process tree until all I/O is reaped. Both streams are bounded;
// stderr is discarded by default. Consumers must not block from Write.
func run(ctx context.Context, executable string, args []string, directory string, stdout io.Writer, limit int) error {
	return runStreams(ctx, executable, args, directory, stdout, io.Discard, limit)
}

func runStreams(ctx context.Context, executable string, args []string, directory string, stdout, stderr io.Writer, limit int) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = directory
	// Do not inherit Python/Node import injection or tool configuration variables.
	cmd.Env = safeEnvironment()
	cmd.Stdout = &cappedWriter{writer: stdout, left: limit, cancel: cancel}
	diagnostic := &toolDiagnostic{}
	cmd.Stderr = &cappedWriter{writer: io.MultiWriter(stderr, diagnostic), left: 8 << 20, cancel: cancel}
	cmd.WaitDelay = 2 * time.Second
	tree, err := startTree(cmd)
	if err != nil {
		return &ToolError{ExitCode: -1}
	}
	defer tree.close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		tree.interrupt()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			tree.kill()
			<-done
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return &ToolError{ExitCode: code, reason: diagnostic.reason()}
	}
	return nil
}

func capture(ctx context.Context, executable string, args []string, dir string, limit int) ([]byte, error) {
	var output bytes.Buffer
	err := run(ctx, executable, args, dir, &output, limit)
	return output.Bytes(), err
}

func safeEnvironment() []string {
	var result []string
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return append(result, "PYTHONNOUSERSITE=1", "PYTHONSAFEPATH=1", "NO_COLOR=1")
}
