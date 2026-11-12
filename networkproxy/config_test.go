package networkproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigRejectsSecretsAndUnsupportedInputs(t *testing.T) {
	for _, raw := range []string{"vmess://secret", "socks4://127.0.0.1:1080", "http://user:secret@host:80", "http://host:0", "http://host:70000", "http://host", "http://host:80/path", "http://host:80?token=secret", "http://host:80#secret", "http://bad host:80", "http://host:80\nsecret"} {
		_, err := (Config{Enabled: true, URL: raw}).Normalize()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid proxy accepted or echoed: %v", err)
		}
	}
	for _, raw := range []string{"http://localhost:8080/", "https://proxy.example:8443", "socks5://127.0.0.1:10808", "socks5h://[::1]:1080"} {
		c, err := (Config{Enabled: true, URL: raw}).Normalize()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(c.URL, "socks5:") {
			t.Fatal("SOCKS route did not request proxy DNS")
		}
	}
}

func newClient(t *testing.T, config Config) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := Configure(transport, config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}

func TestExplicitHTTPProxyAndDirectIgnoreEnvironment(t *testing.T) {
	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !r.URL.IsAbs() {
			t.Error("proxy did not receive absolute request")
		}
		_, _ = io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "*")
	proxied := newClient(t, Config{Enabled: true, URL: proxy.URL})
	response, err := proxied.Get("http://does-not-resolve.invalid/file")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "via proxy" || hits.Load() != 1 {
		t.Fatal("explicit proxy was bypassed by environment")
	}
	t.Setenv("NO_PROXY", "")
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "direct origin") }))
	defer origin.Close()
	direct := newClient(t, Config{})
	// Resolve only the synthetic origin locally, avoiding external DNS. An
	// inherited environment proxy would instead receive the real proxy address.
	direct.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "does-not-resolve.invalid:80" {
			address = origin.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://does-not-resolve.invalid/file", nil)
	response, err = direct.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "direct origin" {
		t.Fatal("Direct used the environment proxy")
	}
	if hits.Load() != 1 {
		t.Fatal("Direct contacted proxy")
	}
}

func TestProxyFailureNeverFallsBackToOrigin(t *testing.T) {
	var originHits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { originHits.Add(1); w.WriteHeader(200) }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	client := newClient(t, Config{Enabled: true, URL: "http://" + address})
	if response, err := client.Get(origin.URL); err == nil {
		response.Body.Close()
		t.Fatal("broken proxy succeeded")
	}
	if originHits.Load() != 0 {
		t.Fatal("proxy failure fell back to direct origin")
	}
}

func TestHTTPSProxyTunnelPreservesTLSVerification(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "tls fixture") }))
	defer origin.Close()
	var connects atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		connects.Add(1)
		target, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "target unavailable", 502)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			target.Close()
			return
		}
		defer conn.Close()
		defer target.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_ = target.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		go func() { _, _ = io.Copy(target, buffer); target.Close() }()
		_, _ = io.Copy(conn, target)
	}))
	defer proxy.Close()
	client := newClient(t, Config{Enabled: true, URL: proxy.URL})
	if response, err := client.Get(origin.URL); err == nil {
		response.Body.Close()
		t.Fatal("proxy bypassed origin certificate verification")
	}
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{RootCAs: origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if connects.Load() != 2 {
		t.Fatal("HTTPS was not tunneled through proxy")
	}
}

func TestHTTPSProxyConnectionValidatesProxyCertificate(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "encrypted proxy fixture") }))
	defer proxy.Close()
	client := newClient(t, Config{Enabled: true, URL: proxy.URL})
	if response, err := client.Get("http://remote.invalid/file"); err == nil {
		response.Body.Close()
		t.Fatal("untrusted HTTPS proxy certificate accepted")
	}
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: proxy.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	response, err := client.Get("http://remote.invalid/file")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "encrypted proxy fixture" {
		t.Fatal("HTTPS proxy did not carry request")
	}
}

func TestSOCKS5UsesRemoteDNS(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "socks fixture") }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var handlers sync.WaitGroup
	handlers.Add(1)
	defer func() { listener.Close(); handlers.Wait() }()
	accepted := make(chan string, 1)
	go func() {
		defer handlers.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(conn)
		header := make([]byte, 2)
		if _, err = io.ReadFull(reader, header); err != nil {
			return
		}
		if header[0] != 5 {
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err = io.ReadFull(reader, methods); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0})
		request := make([]byte, 4)
		if _, err = io.ReadFull(reader, request); err != nil {
			return
		}
		if request[3] != 3 {
			return
		}
		n, err := reader.ReadByte()
		if err != nil {
			return
		}
		domain := make([]byte, int(n))
		if _, err = io.ReadFull(reader, domain); err != nil {
			return
		}
		port := make([]byte, 2)
		if _, err = io.ReadFull(reader, port); err != nil {
			return
		}
		accepted <- string(domain) + ":" + strconv.Itoa(int(port[0])<<8|int(port[1]))
		target, err := net.DialTimeout("tcp", strings.TrimPrefix(origin.URL, "http://"), time.Second)
		if err != nil {
			return
		}
		defer target.Close()
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
		go func() { _, _ = io.Copy(target, reader); target.Close() }()
		_, _ = io.Copy(conn, target)
	}()
	client := newClient(t, Config{Enabled: true, URL: "socks5://" + listener.Addr().String()})
	response, err := client.Get("http://remote-dns.invalid:80/file")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	if string(data) != "socks fixture" || <-accepted != "remote-dns.invalid:80" {
		t.Fatal("SOCKS did not route target hostname through proxy")
	}
}
