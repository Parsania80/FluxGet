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

func TestMismatchedBrowserPackageDoesNotReachDesktop(t *testing.T) {
	var input, output bytes.Buffer
	req := browserbridge.Request{Version: browserbridge.Version, Operation: "policy", Browser: "opera", Profile: "profile-1234567890"}
	if err := browserbridge.Write(&input, req); err != nil {
		t.Fatal(err)
	}
	if err := run(&input, &output, "chrome", []string{"chrome-extension://" + browserbridge.ChromiumID + "/"}); err != nil {
		t.Fatal(err)
	}
	var reply browserbridge.Response
	if err := browserbridge.Read(&output, &reply); err != nil || reply.OK || reply.Error != "Native host does not match this browser package" {
		t.Fatalf("reply=%+v error=%v", reply, err)
	}
}
