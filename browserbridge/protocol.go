// Package browserbridge defines the bounded browser/desktop protocol. It never
// downloads files or starts an independent queue owner.
package browserbridge

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const Version = 1
const HostName = "io.github.parsania80.fluxget"
const MaximumMessage = 64 << 10
const FirefoxID = "fluxget@parsania80.github.io"

// ChromiumID is derived from the public manifest key, pinned during packaging.
const ChromiumID = "phfanjlpchehjklocnblfjdhgmlcjoaa"

var token = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)
var diagnosticURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

func ValidToken(s string) bool   { return token.MatchString(s) }
func ValidBrowser(s string) bool { return s == "chrome" || s == "firefox" || s == "opera" }

type Proof struct {
	Method          string `json:"method"`
	HeadersObserved bool   `json:"headers_observed"`
	Credentials     bool   `json:"credentials"`
	Safe            bool   `json:"safe"`
	Private         bool   `json:"private"`
}
type Request struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Browser   string `json:"browser"`
	Profile   string `json:"profile"`
	RequestID string `json:"request_id,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Consent   bool   `json:"consent,omitempty"`
	Canceled  bool   `json:"canceled,omitempty"`
	Proof     *Proof `json:"proof,omitempty"`
}
type Response struct {
	Version   int    `json:"version"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Enabled   bool   `json:"enabled"`
	Connected *bool  `json:"connected,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Stage     string `json:"stage,omitempty"`
	JobID     int    `json:"job_id,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// Diagnostics omit complete URLs: queries and paths can carry access tokens.
// The original URL is preserved only in the transfer request/saved metadata.
func Diagnostic(message string) string {
	return diagnosticURL.ReplaceAllString(message, "[download URL]")
}
func Failure(message string) Response { return Response{Version: Version, Error: Diagnostic(message)} }
func (r Request) Key() string         { return r.Browser + ":" + r.Profile + ":" + r.RequestID }
func Validate(r Request) error {
	if r.Version != Version || !ValidBrowser(r.Browser) || !ValidToken(r.Profile) {
		return errors.New("unsupported protocol or browser profile")
	}
	switch r.Operation {
	case "policy", "profile", "profile-status":
		return nil
	case "prepare", "activate", "abort", "status":
	default:
		return errors.New("unknown browser operation")
	}
	if !ValidToken(r.RequestID) {
		return errors.New("invalid handoff identifier")
	}
	if r.Operation != "prepare" {
		return nil
	}
	if err := ValidateURL(r.URL); err != nil {
		return err
	}
	if r.Filename == "" || len(r.Filename) > 240 || r.Filename == "." || r.Filename == ".." || strings.ContainsAny(r.Filename, "/\\") || filepath.Base(r.Filename) != r.Filename {
		return errors.New("choose a plain download filename")
	}
	for _, c := range r.Filename {
		if unicode.IsControl(c) {
			return errors.New("invalid filename")
		}
	}
	if r.Kind != "link" && r.Kind != "capture" {
		return errors.New("unsupported handoff kind")
	}
	if r.Kind == "capture" && (r.Proof == nil || r.Proof.Method != "GET" || !r.Proof.HeadersObserved || r.Proof.Credentials || !r.Proof.Safe || r.Proof.Private) {
		return errors.New("this browser request cannot be safely captured")
	}
	return nil
}
func ValidateURL(raw string) error {
	if len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("unsupported download link")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("use an HTTP/HTTPS link without embedded credentials or fragments")
	}
	return nil
}
func Read(r io.Reader, target any) error {
	var size uint32
	if err := binary.Read(r, binary.NativeEndian, &size); err != nil {
		return err
	}
	if size == 0 || size > MaximumMessage {
		return errors.New("browser message exceeds its limit")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("invalid browser message")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid browser message")
	}
	return nil
}
func Write(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaximumMessage {
		return errors.New("browser message exceeds its limit")
	}
	frame := make([]byte, 4+len(data))
	binary.NativeEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
