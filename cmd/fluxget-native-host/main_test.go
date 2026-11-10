package main

import (
	"bytes"
	"github.com/Parsania80/FluxGet/browserbridge"
	"testing"
)

func TestCallerIdentityCannotSelectAnotherBrowser(t *testing.T) {
	chrome := "chrome-extension://" + browserbridge.ChromiumID + "/"
	if !caller("chrome", []string{chrome}) || !caller("firefox", []string{"/manifest.json", browserbridge.FirefoxID}) || !caller("opera", []string{chrome}) {
		t.Fatal("registered identity rejected")
	}
	for _, v := range []struct {
		browser string
		args    []string
	}{{"chrome", []string{"chrome-extension://wrong/"}}, {"firefox", []string{chrome}}, {"chrome", []string{browserbridge.FirefoxID}}, {"unknown", []string{chrome}}} {
		var out bytes.Buffer
		if run(bytes.NewReader(nil), &out, v.browser, v.args) == nil || out.Len() != 0 {
			t.Fatal("unrecognized caller accepted")
		}
	}
}
