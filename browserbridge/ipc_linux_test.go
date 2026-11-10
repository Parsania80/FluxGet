//go:build linux

package browserbridge

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateIPCExclusiveCancelableAndBounded(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	server, err := Serve(func(ctx context.Context, r Request) Response {
		return Response{Version: Version, OK: true, Enabled: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	path, err := SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("public socket")
	}
	if other, err := Serve(func(context.Context, Request) Response { return Response{} }); err == nil {
		other.Close()
		t.Fatal("second owner")
	}
	r := request()
	r.Operation = "policy"
	reply, err := Call(context.Background(), r)
	if err != nil || !reply.OK {
		t.Fatal(reply, err)
	}
	// A peer sending no frame must not delay shutdown for the read deadline.
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() { server.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle connection blocked close")
	}
	if err = os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("do not remove"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, e := Serve(func(context.Context, Request) Response { return Response{} }); e == nil {
		s.Close()
		t.Fatal("non-socket replaced")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "do not remove" {
		t.Fatal("IPC replaced unrelated file")
	}
}
func TestIPCRejectsSharedDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "fluxget-browser")
	_ = os.Mkdir(dir, 0755)
	if _, err := SocketPath(); err == nil {
		t.Fatal("shared IPC accepted")
	}
}
