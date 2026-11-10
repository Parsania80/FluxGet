package browserbridge

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Profile struct {
	Browser string `json:"browser"`
	Enabled bool   `json:"enabled"`
}
type Policy struct {
	Version    int                `json:"version"`
	Enabled    bool               `json:"enabled"`
	Browsers   map[string]bool    `json:"browsers"`
	Mode       string             `json:"mode"`
	Profiles   map[string]Profile `json:"profiles"`
	Exclusions []string           `json:"exclusions,omitempty"`
}
type Store struct {
	mu     sync.Mutex
	path   string
	policy Policy
}

func ConfigDir() (string, error) { p, e := os.UserConfigDir(); return filepath.Join(p, "fluxget"), e }
func OpenStore() (*Store, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "browser-integration.json"), policy: Policy{Version: Version, Mode: "ask", Browsers: map[string]bool{}, Profiles: map[string]Profile{}}}
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaximumMessage+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaximumMessage {
		return nil, errors.New("browser settings too large")
	}
	if json.Unmarshal(data, &s.policy) != nil || s.policy.Version != Version || !validMode(s.policy.Mode) {
		return nil, errors.New("invalid browser integration settings")
	}
	if s.policy.Browsers == nil {
		s.policy.Browsers = map[string]bool{}
	}
	if s.policy.Profiles == nil {
		s.policy.Profiles = map[string]Profile{}
	}
	for id, p := range s.policy.Profiles {
		if !ValidToken(id) || !ValidBrowser(p.Browser) {
			return nil, errors.New("invalid browser profile")
		}
	}
	return s, nil
}
func validMode(m string) bool     { return m == "ask" || m == "automatic" || m == "manual" }
func (s *Store) Snapshot() Policy { s.mu.Lock(); defer s.mu.Unlock(); return clone(s.policy) }
func clone(p Policy) Policy {
	data, _ := json.Marshal(p)
	var q Policy
	_ = json.Unmarshal(data, &q)
	return q
}
func (s *Store) Edit(edit func(*Policy) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.policy)
	if err := edit(&next); err != nil {
		return err
	}
	if !validMode(next.Mode) || len(next.Profiles) > 100 || len(next.Exclusions) > 100 {
		return errors.New("invalid integration settings")
	}
	for _, site := range next.Exclusions {
		if !ValidSite(site) {
			return errors.New("enter excluded hostnames without paths")
		}
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(data) > MaximumMessage {
		return errors.New("too many browser settings")
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".browser-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.policy = next
	return nil
}
func ValidSite(s string) bool {
	if s == "" || len(s) > 253 || s != strings.ToLower(s) || strings.ContainsAny(s, "/:?#@ \t\r\n") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
func (p Policy) Allowed(r Request) bool {
	profile, ok := p.Profiles[r.Profile]
	return p.Enabled && p.Browsers[r.Browser] && ok && profile.Enabled && profile.Browser == r.Browser
}
func (p Policy) Excluded(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, s := range p.Exclusions {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}
func (p Policy) Reply(r Request) Response {
	return Response{Version: Version, OK: true, Enabled: p.Allowed(r), Mode: p.Mode}
}
