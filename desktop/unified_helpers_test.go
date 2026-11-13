package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func unifiedCLI(t *testing.T) string {
	t.Helper()
	binary := testBinaryPath(t)
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}
func unifiedQueue(t *testing.T, binary string, active int) *queueClient {
	t.Helper()
	q, err := startConfiguredQueue(binary, active, 1, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-q.done:
			return
		default:
			_ = q.send("quit")
		}
		select {
		case <-q.done:
		case <-time.After(10 * time.Second):
			t.Error("scheduler did not stop")
		}
	})
	return q
}
func unifiedRequest(t *testing.T, q *queueClient, request desktopRequest) desktopResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := q.request(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func unifiedWait(t *testing.T, q *queueClient, match func([]queueEntry) bool) []queueEntry {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"})
		if match(response.Jobs) {
			return response.Jobs
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("download state did not converge")
	return nil
}
