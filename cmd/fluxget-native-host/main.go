package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Parsania80/FluxGet/browserbridge"
)

func caller(browser string, args []string) bool {
	if !browserbridge.ValidBrowser(browser) {
		return false
	}
	expected := "chrome-extension://" + browserbridge.ChromiumID + "/"
	for _, a := range args {
		if browser == "firefox" && a == browserbridge.FirefoxID {
			return true
		}
		if browser != "firefox" && a == expected {
			return true
		}
	}
	return false
}
func run(in io.Reader, out io.Writer, browser string, args []string) error {
	if !caller(browser, args) {
		return errors.New("unrecognized native extension identity")
	}
	for {
		var req browserbridge.Request
		err := browserbridge.Read(in, &req)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		req.Browser = browser // The registered executable, not page input, selects the browser.
		var reply browserbridge.Response
		if err := browserbridge.Validate(req); err != nil {
			reply = browserbridge.Failure(err.Error())
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
			reply, err = browserbridge.Call(ctx, req)
			if err != nil && !browserbridge.DesktopRunning() {
				self, e := os.Executable()
				if e == nil {
					gui := filepath.Join(filepath.Dir(self), "fluxget-gui")
					cmd := exec.Command(gui)
					cmd.Stdout = os.Stderr
					cmd.Stderr = os.Stderr
					if cmd.Start() == nil {
						_ = cmd.Process.Release()
						until := time.Now().Add(8 * time.Second)
						for time.Now().Before(until) {
							time.Sleep(150 * time.Millisecond)
							reply, err = browserbridge.Call(ctx, req)
							if err == nil {
								break
							}
						}
					}
				}
			}
			cancel()
			if err != nil {
				reply = browserbridge.Failure("Open the updated FluxGet app and test the connection again")
			}
		}
		if err = browserbridge.Write(out, reply); err != nil {
			return err
		}
	}
}
func main() {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	browser := strings.TrimPrefix(name, "fluxget-native-")
	if err := run(os.Stdin, os.Stdout, browser, os.Args[1:]); err != nil {
		_, _ = io.WriteString(os.Stderr, "FluxGet native messaging stopped: invalid input or connection\n")
		os.Exit(1)
	}
}
