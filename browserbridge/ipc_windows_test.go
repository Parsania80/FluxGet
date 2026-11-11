//go:build windows

package browserbridge

import (
	"bytes"
	"context"
	"github.com/Microsoft/go-winio"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsPipeExclusiveRoundtripAndShutdown(t *testing.T) {
	server, err := Serve(func(context.Context, Request) Response { return Response{Version: Version, OK: true, Enabled: true} })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if other, err := Serve(func(context.Context, Request) Response { return Response{} }); err == nil {
		other.Close()
		t.Fatal("second pipe owner accepted")
	}
	r := request()
	r.Operation = "policy"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := Call(ctx, r)
	if err != nil || !reply.OK {
		t.Fatalf("reply=%+v error=%v", reply, err)
	}
	name, _, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	idle, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	done := make(chan struct{})
	go func() { server.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle client blocked shutdown")
	}
}

// Exercise the real executable across process boundaries, including the host
// basename, pinned extension origin and named-pipe peer process identity.
func TestWindowsNativeHostRoundtrip(t *testing.T) {
	server, err := Serve(func(context.Context, Request) Response { return Response{Version: Version, OK: true, Enabled: true} })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	binary := filepath.Join(t.TempDir(), "fluxget-native-chrome.exe")
	build := exec.Command("go", "build", "-o", binary, "./cmd/fluxget-native-host")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native host build: %v %s", err, out)
	}
	var input bytes.Buffer
	r := request()
	r.Operation = "policy"
	if err := Write(&input, r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := exec.CommandContext(ctx, binary, "chrome-extension://"+ChromiumID+"/")
	host.Stdin = &input
	output, err := host.Output()
	if err != nil {
		t.Fatal(err)
	}
	var reply Response
	if err := Read(bytes.NewReader(output), &reply); err != nil || !reply.OK {
		t.Fatalf("native reply=%+v error=%v", reply, err)
	}
}
