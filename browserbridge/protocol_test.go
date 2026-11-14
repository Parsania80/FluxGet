package browserbridge

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func request() Request {
	return Request{Version: Version, Operation: "prepare", Browser: "chrome", Profile: "profile-1234567890", RequestID: "request-1234567890", URL: "https://example.org/file.iso", Filename: "file.iso", Kind: "capture", Proof: &Proof{Method: "GET", HeadersObserved: true, Safe: true}}
}
func TestFramesAndValidation(t *testing.T) {
	r := request()
	var b bytes.Buffer
	if err := Write(&b, r); err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := Read(&b, &decoded); err != nil || Validate(decoded) != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{{1, 0, 0}, {1, 0, 0, 0}, {0, 0, 0, 0}, append([]byte{2, 0, 0, 0}, []byte("{}")...)} {
		var out Request
		err := Read(bytes.NewReader(data), &out)
		if err == nil && Validate(out) == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	var huge bytes.Buffer
	_ = binary.Write(&huge, binary.NativeEndian, uint32(MaximumMessage+1))
	if Read(&huge, &decoded) == nil {
		t.Fatal("unbounded frame")
	}
	var unknown bytes.Buffer
	payload := []byte(`{"version":1,"unknown":true}`)
	_ = binary.Write(&unknown, binary.NativeEndian, uint32(len(payload)))
	unknown.Write(payload)
	if Read(&unknown, &decoded) == nil {
		t.Fatal("unknown protocol field")
	}
	for _, change := range []func(*Request){func(r *Request) { r.Proof.Method = "POST" }, func(r *Request) { r.Proof.Credentials = true }, func(r *Request) { r.Proof.Safe = false }, func(r *Request) { r.Proof.Private = true }, func(r *Request) { r.Proof.HeadersObserved = false }, func(r *Request) { r.URL = "https://example.org/file#fragment" }, func(r *Request) { r.URL = "https://user:password@example.org/file" }, func(r *Request) { r.Filename = "../file" }, func(r *Request) { r.Profile = "bad" }, func(r *Request) { r.Version++ }} {
		r := request()
		change(&r)
		if Validate(r) == nil {
			t.Fatal("unsafe capture accepted", r.Operation)
		}
	}
	if Read(bytes.NewReader(nil), &decoded) != io.EOF {
		t.Fatal("EOF handling")
	}
}
func TestQueryLinksPreservedAndDiagnosticsRedacted(t *testing.T) {
	r := request()
	r.URL += "?token=private%2Bvalue&part=1&part=2&empty="
	if err := Validate(r); err != nil {
		t.Fatal("query link rejected", err)
	}
	var b bytes.Buffer
	if err := Write(&b, r); err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := Read(&b, &decoded); err != nil || decoded.URL != r.URL {
		t.Fatal("query was changed in transit")
	}
	message := Failure(`Get "` + r.URL + `": connection failed`).Error
	if strings.Contains(message, "private") || strings.Contains(message, "token=") || !strings.Contains(message, "connection failed") {
		t.Fatal("unsafe or unhelpful diagnostic")
	}
}
func TestPolicyConsentAndPersistence(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
	s, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	if s.Snapshot().Allowed(r) {
		t.Fatal("default on")
	}
	if err = s.Edit(func(p *Policy) error {
		p.Enabled = true
		p.Browsers["chrome"] = true
		p.Profiles[r.Profile] = Profile{Browser: "chrome", Enabled: true}
		p.Exclusions = []string{"private.example.org"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore()
	if err != nil || !s.Snapshot().Allowed(r) || !s.Snapshot().Excluded("https://sub.private.example.org/file") {
		t.Fatal("policy lost", err)
	}
	for _, kind := range []string{"master", "browser", "profile", "wrong-browser"} {
		p := s.Snapshot()
		switch kind {
		case "master":
			p.Enabled = false
		case "browser":
			p.Browsers["chrome"] = false
		case "profile":
			delete(p.Profiles, r.Profile)
		case "wrong-browser":
			p.Profiles[r.Profile] = Profile{Browser: "firefox", Enabled: true}
		}
		status := r
		status.Operation = "profile-status"
		reply := p.Reply(status)
		connected := kind == "master" || kind == "browser"
		if reply.Enabled || reply.Connected == nil || *reply.Connected != connected {
			t.Fatal("incorrect profile or capture state", kind, reply)
		}
	}
	if reply := s.Snapshot().Reply(r); reply.Connected != nil {
		t.Fatal("legacy replies must not include new fields")
	}
	// A failed save must not change the accepted in-memory policy.
	dir := t.TempDir()
	s.path = filepath.Join(dir, "missing", "file.json")
	if err = os.WriteFile(filepath.Join(dir, "missing"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.Edit(func(p *Policy) error { p.Enabled = false; return nil }) == nil || !s.Snapshot().Allowed(r) {
		t.Fatal("failed settings save changed policy")
	}
}
