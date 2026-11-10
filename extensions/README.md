# FluxGet browser extension preview

This is a development preview for public direct downloads. It uses a local native host and the same FluxGet Downloads list. Chrome, Firefox and Opera have separate manifests; Chrome is the first native acceptance target. Store signing/distribution and other browser/package acceptance are still pending.

## Build

From the repository root, with the normal desktop build dependencies installed:

```bash
mkdir -p dist
go build -o dist/fluxget .
(cd desktop && go build -o ../dist/fluxget-gui .)
go build -o dist/fluxget-native-chrome ./cmd/fluxget-native-host
cp dist/fluxget-native-chrome dist/fluxget-native-firefox
cp dist/fluxget-native-chrome dist/fluxget-native-opera
python3 packaging/build_browser_extensions.py
python3 packaging/install_browser_bridge.py --prefix "$PWD/dist" --browsers chrome
```

Keep the CLI, GUI and native hosts together. Register only the browsers you choose. Add `firefox` or `opera` to `--browsers` when testing their native packages; this does not install or approve an extension. Repair reruns the same registration helper. To unregister, add `--uninstall`; downloads and browser profiles are kept.

## Set up Chrome

1. Open `chrome://extensions`, turn on Developer mode, and choose **Load unpacked**.
2. Select `dist/extensions/chrome`. Pin FluxGet if desired.
3. Open the updated FluxGet app. In **Settings → Browser downloads**, enable integration and Chrome. Choose **Ask before downloading** or **Download automatically**, then save.
4. Open the extension popup and enable this browser profile. Approve the one-time permission for all HTTP/HTTPS websites. Manual-only mode does not need website access. Use **Test connection**.
5. For an already enabled profile, choose **Enable capture for all websites** and approve the browser permission. Reload the page before retrying a download. New sites, download hosts and redirect destinations need no separate grants. To keep selected download hosts in the browser, enter them in FluxGet **Settings → Browser downloads → Excluded hosts**; an entry also covers its subdomains.
6. Start a supported download. FluxGet asks for the destination, unless automatic mode is selected. Extra jobs wait in the same list. A conflicting destination leaves the download in the browser; choose a different name or use Add download directly.

Normal left-click uses FluxGet when capture is enabled. **Ctrl + left-click** keeps the matching download in the browser, including links opened in a new tab. Browser navigation behavior is preserved. This uses an isolated content script registered only on granted HTTP/HTTPS sites, with optional `scripting` permission requested alongside website access. Existing users should reload the extension, choose **Enable capture for all websites** once to finish shortcut setup, and reload their web pages. Browser-owned/restricted pages cannot run the shortcut script; the popup's **Use browser for 15 minutes** remains available.

Bypass intent applies once to the matching originating tab or its opened tab and observed link/redirect chain. Intent and used IDs live only in bounded browser session storage: up to 32 tickets, 30-second lifetime for links, five seconds for buttons without a link, and 64 short-lived opener associations. Unsupported or delayed site-specific flows may need the temporary browser bypass. Synthetic page events cannot request a bypass.

Manual sending uses **right-click a public link → Send link to FluxGet** and always asks for the destination. It does not require automatic site monitoring. The master switch, browser switch, and profile consent must all be enabled.

## Firefox and Opera

For Firefox development, open `about:debugging#/runtime/this-firefox`, choose **Load Temporary Add-on**, and select `dist/extensions/firefox/manifest.json`. Temporary add-ons are removed when Firefox closes; signing is required for a normal release. Snap/Flatpak Firefox also need the host system's native-messaging portal. A manifest copied to `~/.mozilla/native-messaging-hosts` alone does not establish that the confined browser can reach it.

For Opera, load the Opera folder from its extensions developer page. Opera registration and behavior must be verified independently on an installed version. Unsupported combinations keep normal browser downloads.

## Capture boundaries

Automatic capture needs an observed, unambiguous GET request, visible request-header checks, a still-active browser item explicitly marked safe, non-private browsing, website permission, and current FluxGet/profile consent. The normal setup requests all HTTP/HTTPS websites once; existing limited grants remain until the user approves broader access. Only a credential-presence boolean is retained; cookie/header values are never stored or sent to FluxGet. Query-string/CDN links are supported: capture preserves the complete final URL exactly, including encoded values, repeated parameters and their order. Embedded username/password credentials, fragments, POST, cookies/auth headers, blob/data URLs, security warnings and unknown cases remain in the browser. Cached or unobservable requests may fall back. The popup shows the fallback reason near the top.

Source URLs, including query tokens, are saved in local queue/checkpoint/history metadata so transfers can resume. Diagnostic messages omit URLs; metadata is not a secret vault and should not be shared. A link can expire or be single-use, and replay/resume may fail even when its browser request succeeded. Such failures require a fresh link or use of the browser; preserving a query does not provide cookie/header replay or guaranteed CDN compatibility.

A browser download may already have received temporary bytes before capture, so its initial browser entry can appear briefly. The extension pauses it, obtains durable destination acceptance, then cancels it and activates that same FluxGet request. After confirmed ownership, it persists a cleanup stage and erases only that exact canceled item from browser download history; it does not delete files or clear unrelated entries. A failed erase remains retryable without reactivating the transfer. Missing browser state after confirmed cleanup differs from missing state before ownership confirmation. No full duplicate transfer is started in the normal sequence. If acceptance fails before cancellation, the browser resumes and its history remains. After cancellation, lost acknowledgments keep a recoverable handoff; use **Retry pending handoffs** after reopening FluxGet. Do not start another copy while one needs recovery. Missing browser state is never guessed as successful cancellation.

Disabling integration/browser/profile stops new handoffs without canceling existing FluxGet jobs. A handoff already canceled in the browser is saved paused if policy was disabled, its acceptance expired, or Download later was chosen; resume it explicitly in FluxGet. **Use browser for 15 minutes** pauses new captures and manual sending locally. **Remove website access** revokes capture permissions; manual sending remains available. Excluded hosts prevent FluxGet intake rather than revoking website-monitoring permission. Private browsing is not supported.

The first accepted browser handoff backs up `queue.json.before-browser` and upgrades the queue to schema 3 for bounded, durable request receipts. Checkpoints stay version 4. Old CLI builds cannot read schema 3: keep the updated executable pair together and retain the backup. Unconfirmed jobs cannot be started manually; finish/abandon them from the extension or remove their list entry. Receipts survive list removal and are capped at 2,048; new handoffs fail safely at the cap rather than silently forgetting deduplication history.

## Tests

```bash
node --test extensions/core.test.cjs
go test -race ./...
(cd desktop && go test -race ./...)
```

The logic suite covers prepare/cancel/activate ordering, fallback, disable/site-revocation races, lost acknowledgments, missing/completed browser items, credential/POST/redirect rejection, identity initialization, manual-link recovery, all-site consent/denial/revocation, limited-grant readiness, trusted Ctrl-click matching/expiry/bounds, and history-cleanup recovery. Native browser acceptance is separate from mocked API tests; see [project.md](../project.md#browser-integration--development-preview) for the tested browser/version/package matrix.

The optional Linux native smoke uses the installed Chrome binary with an isolated test profile and documented extension-debugging controls; it keeps browser sandboxing enabled. It tests the real popup/profile consent and native handoff through a query-bearing redirect, checks the exact final query and output, verifies a trusted Ctrl-click redirect stays in Chrome, and checks exact-ID history cleanup plus excluded-host and master-disable browser fallback. Only the copied test manifest pre-grants all-site capture; shipping permissions remain optional and the fixture serves local test files. Run from `desktop/` with `FLUXGET_BROWSER_NATIVE_SMOKE=1 go test -count=1 -race -run '^TestBrowserNativeChromeAcceptance$' -v .`; Set `FLUXGET_BROWSER_NATIVE_MODE=ask` for a separate run that also verifies exactly one destination prompt in Ask first mode. Normal user setup/permission approval remains a separate acceptance step.

Official protocol references: [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging), [Chrome downloads](https://developer.chrome.com/docs/extensions/reference/api/downloads), [Firefox native messaging](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging), and [confined Firefox portal design](https://firefox-source-docs.mozilla.org/toolkit/components/extensions/webextensions/native-messaging-portal-design.html).
