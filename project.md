# FluxGet — Project Roadmap

FluxGet is a Go download manager inspired by IDM. The priorities are reliable transfers, understandable desktop controls, optional browser integration, and later experimental multi-interface downloading. It is also a learning and portfolio project for Go, concurrency, networking, persistence, and recovery.

**Status — 2026-11-13:** [v1.0.0 is published](https://github.com/Parsania80/FluxGet/releases/tag/v1.0.0) for Linux/amd64 and Windows/amd64; [release verification](https://github.com/Parsania80/FluxGet/actions/runs/43024303927) passed native checks and installer gates. The next release's isolated media backend spike is implemented and passes real Linux fixture transfers; native Windows and broader public YouTube acceptance remain open; a full public audio-to-MP3 transfer now passes through the unchanged proxy. Single-video selection and stage-3 persistent media jobs are implemented, installed and operator-approved as a Linux preview on 2026-11-13; stage-4 playlist selection and durable batch admission are implemented and installed locally, with public four-entry metadata extraction passed and the normal-playlist preview operator-approved on 2026-11-13. Broader public-transfer acceptance remains open and the next version number is undecided. The current acceptance checkout is `codex/stage5-media-acceptance`, prepared for the operator to push through GitHub Desktop; `main` remains the development baseline. Windows browser acceptance, Firefox/Opera acceptance, Automatic stage 5, and multi-interface downloading remain open.

This file describes current decisions and remaining work. [README.md](README.md) is the simple user guide, [BENCHMARKS.md](BENCHMARKS.md) records measured workloads, and [SESSION-LOG.md](SESSION-LOG.md) preserves development history. Implementation does not by itself mean release acceptance has passed.

## Roadmap

Keep these priorities while continuing engine release verification alongside product development.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1 | CLI and desktop prototype baseline | ✅ Done | Sequential/parallel downloads, persistent queue, recovery, progress, and live limits |
| 2 | Unified Downloads experience | 🔄 Current | Implemented and preview approved; detailed interactive acceptance remains |
| 3 | Automatic download management | 🔄 Current | Stages 1–4 implemented; stage 5 compares Automatic with Manual |
| 4 | Browser capability spike and bridge contract | 🔄 Current | Chrome 157/native messaging tested; Firefox Snap portal exists but native acceptance is open; Opera absent |
| 5 | Shared intake, native host, and manual Send to FluxGet | 🔄 Current | Implemented with private IPC and durable receipts; broader crash/setup/native acceptance remains |
| 6 | Chrome capture and integration settings | 🔄 Current | Preview implemented; real popup consent, capture and master-disable fallback tested; normal setup/permission review remains |
| 7 | Firefox and Opera rollout | ⬜ Todo | Manifests and shared implementation prepared; package-specific native verification is pending |
| 8 | Browser packaging and acceptance | ⬜ Todo | Stable extension IDs, distribution, repair/uninstall, privacy, crash tests |
| 9 | v1.0.0 Windows/Linux release | ✅ Done | Native .deb/.exe installers and checksums published after native test and installer gates; broader browser acceptance remains open |
| 10 | Multi-interface experiment | ⬜ Todo | Prove routing, correct output, failover, and benefit on independent uplinks |
| 11 | Authenticated browser downloads | ⬜ Todo | Separate consent, credential, request-replay, and recovery design |
| 12 | YouTube videos and playlists | 🔄 Current | Stages 1–4 implemented; Linux single-video and normal-playlist previews operator-approved; broader public-transfer/native Windows acceptance remains open |
| 13 | Optional proxy routing per download | ✅ Done | Settings default, per-download Proxy/Direct controls, HTTP/HTTPS/SOCKS5, browser-intake defaults, CLI and media adapter; native Windows acceptance still required |

### ➡️ Current: #12 — Stage 5 recovery/resource acceptance 🔄 In progress

### ⏭️ Next: #12 — Publish acceptance branch, complete Windows CI, then extended wall-clock/storage checks

**Desktop UI refresh — ✅ Done:** Operator-requested refinement covers all three tabs: the installed YouTube playlist workspace; Downloads with full-width filenames, contextual actions and a persistent selection bar; Settings split into General, Connections, Proxy and Browser downloads with persistent save controls. The refreshed Linux GUI is installed, saved queue/settings/browser-consent data is preserved, and the operator approved committing the changes on 2026-11-13. Functional stage-4 approval remains recorded; stage-5 recovery/resource acceptance is underway, with remaining native/public checks still open.

**Next concrete work:** Push the prepared acceptance branch through GitHub Desktop and finish native Windows CI, then extend wall-clock/slow-storage and resource-extreme trials. Initial recovery, one-hour media and bounded real Linux CPU/memory-pressure checks are recorded below. The operator confirmed normal playlists work and approved stage 4 on 2026-11-13; bounded YouTube Mix import was explicitly declined. Public video/audio merging and all four sample playlist entries (M4A) now pass on Linux, as recorded below. The latest published Windows native media/proxy step passed, but desktop controls failed; the new branch adds failure annotations for diagnosis. Run its native matrix before closing Windows acceptance. Public metadata and a complete audio-to-MP3 transfer passed through the unchanged proxy; generated MP4 merge, rate ceiling and process-tree cancellation are also verified. These scoped public samples do not prove the broader format matrix or acceptance of the current source on Windows. The operator approved stages 2–3 while these checks remain pending. The operator approved the installed Linux single-video preview on 2026-11-13, completing its hands-on preview review; do not treat the remaining acceptance checks as passed. Retain existing browser acceptance and store-distribution tasks. Only numeric `vMAJOR.MINOR.PATCH` tags matching the source version build/publish packages; branch pushes run ordinary CI. Preserve the published v1.0.0 tag and assets.

## Product scope and release boundaries

The v1 release targets are **Linux/amd64 and Windows/amd64**. Linux .deb installers are built on Ubuntu 22.04 for an older glibc baseline; native Windows checks use Windows Server 2022. Ubuntu 26.04.1 with Go 1.26.8 has local runtime evidence. Windows browser acceptance is pending, macOS remains cross-build-only, and ARM64 is pending. Windows downloads/checkpoints require NTFS ownership streams and hard links. Keep shared code portable and OS behavior in small adapters. WSL tests count as Linux evidence.

| Milestone | Scope | Release gate |
|---|---|---|
| v1.0.0: First desktop release (supersedes planned v0.1 naming) | CLI, HTTP/HTTPS, queue, parallel ranges, conditional resume, recovery, optional SHA-256, resource limits | Reproducible correctness/failure tests and packaged native smoke tests |
| Desktop, parallel track | One Downloads list, shared settings, Automatic/Manual, migration, batch actions | Preserved user data and interactive acceptance |
| Browser integration, parallel track | Optional Chrome/Firefox/Opera extensions and local native host | Tested handoff, browser fallback, per-browser consent, installation matrix |
| Next release: YouTube media | Public single videos, finite playlists, available video/audio formats, merging and selected audio conversion | Staged media acceptance below; version number selected before release |
| Later: Multi-interface experiment | Explicit network paths on one selected OS | Correct egress, output, and failover on independent real uplinks |
| Later: Broader interface support | Remaining OS adapters | Native capability and privilege matrix |
| Later | Authenticated browser downloads and adaptive interface scheduling | Separate security, replay, and routing designs |

Desktop and browser work precede multi-interface development while retaining the engine correctness requirements. There are no fixed release dates or claims of complete IDM parity, guaranteed acceleration, or uninterrupted browsing.

Outside current scope: website login automation, cookies/header credential storage, live-stream recording, general-purpose media-site extraction, torrents, FTP/SFTP, cloud sync, remote administration, and automatic updates. YouTube extraction and assembly for completed public videos are now planned for the next release, through the separate media backend below. Operator-approved query-string/CDN URLs remain supported without cookie/header replay; expiry and single-use recovery are not guaranteed. No hosted service, database server, account system, or public control API is required. Scheduled queues, mirrors, notifications, metered-network controls, and a background service remain optional future work with a demonstrated need.

## Tech Stack Lock

| Area | Selected technology | Boundary |
|---|---|---|
| Engine and CLI | Go 1.26 baseline; standard library plus Windows adapters below | Root module `github.com/Parsania80/FluxGet` |
| Desktop | Go 1.26.8; Fyne v2.8.2 and pinned dependencies | Separate `desktop/go.mod` and `desktop/go.sum`; no replacement UI framework |
| Persistence | Versioned local JSON | Queue schemas 1–6 readable; browser receipts use schema 3+, proxy schema 4, media schema 5 and backed-up playlist receipts schema 6; checkpoints remain v4; no database |
| Browser extensions | Plain JavaScript, HTML, CSS, JSON; official WebExtensions APIs | Manifest V3 where supported, browser-specific background/manifest adapters; no initial framework |
| Native bridge | Go native messaging; Linux Unix sockets; Windows go-winio named pipes | Browser-specific registration and explicit extension-ID allowlists |
| Windows primitives | `golang.org/x/sys` v0.49.0; `github.com/Microsoft/go-winio` v0.6.4 | Windows-only locks/file IDs/SIDs/ACLs and cancellable local named pipes |
| Release packaging | Python 3 standard library, dpkg-deb, Inno Setup 6, POSIX shell, Windows PowerShell, GCC/MinGW windres | Native GitHub Actions builds; .deb system installation and .exe per-user setup; checksums and uninstall; no hosted runtime |
| Development packaging | Python 3 standard library | Copy extension assets and register selected native hosts; no Python dependency in the running Go app/extension |
| YouTube spike | yt-dlp 2026.10.26 with EJS 0.8.0; Deno 2.9.8; FFmpeg/ffprobe N-128757-g4b42530309 | Explicit external development executables; SHA-256/artifact URLs in packaging/media-tools.lock.json; production packaging remains gated |
| Proxy routing | Go standard-library net/http proxy transport; explicit yt-dlp --proxy | HTTP/HTTPS/SOCKS5 endpoints without credentials; direct ignores proxy environment; no new Go dependency |

Packaging helpers use the Python standard library already available on the development host. Plain shell would make JSON/path handling more fragile; a separate Go packaging command would need another build step for simple file generation. The helpers add no third-party library or running-app dependency. Native host and extension runtime remain Go and plain JavaScript respectively. Windows adds x/sys for maintained Win32 calls and go-winio for cancellable named pipes with explicit ACLs, exclusive first-instance ownership and remote-client rejection. Handwritten syscall/overlapped-I/O code would substantially enlarge the security and cancellation surface; TCP loopback would require a separate authentication design. These dependencies are pinned in both Go modules and do not change Linux transfer behavior. Native installers use dpkg-deb on Linux and Inno Setup 6 on the Windows build runner. Inno supplies a standard setup wizard and uninstall entry with less custom scripting than NSIS; it is a build-time tool, not a downloader runtime dependency. Debian payloads install in /opt/fluxget with system Chrome/Firefox manifests; Windows keeps per-user registration.

New dependencies require a documented need and comparison. JSON was selected over SQLite to keep dependencies small; locking, validation, and atomic replacement remain the application's responsibility. Fyne was selected after a working desktop proof of concept. Keep the current source layout until a concrete boundary justifies restructuring.

## Architecture and ownership

```text
Fyne desktop -> structured companion-process requests -> CLI queue owner
Standalone CLI ---------------------------------------> engine

Settings + transfer/resource observations
    -> queue owner + Automatic controller
    -> global/origin request leases + per-file worker targets
    -> transfer owner -> validated HTTP bodies -> OUTPUT.part
    -> synchronized checkpoints -> verification -> final output
    -> structured progress/state snapshots -> Downloads UI
```

The desktop launches the CLI beside it and connects to one exclusively owned persistent scheduler. The queue owner changes job state, reserves destinations, admits files, manages budgets, and acknowledges configuration. A competing CLI queue is reported rather than opening a second owner. Transfer owners control part assignment and checkpoint publication; individual workers never write queue/checkpoint JSON directly.

Settings are saved only after scheduler acknowledgment; a settings-write failure restores the prior scheduler policy. A private speed-settings JSON file supplies live default, individual, and shared ceilings. The standalone CLI defaults to Manual; desktop startup explicitly supplies its mode.

| Source area | Responsibility |
|---|---|
| `main.go`, `destination.go` | CLI input, cancellation, safe filename/destination selection |
| `download.go`, `parallel.go`, `transfer_policy.go` | Sequential/range requests, validation, timeouts, retries |
| `resume.go`, `remote_validation.go`, `reconcile.go` | Local/remote verification and publication recovery |
| `checkpoint.go`, `jobs.go`, `job_lock_*.go`, `partial_identity_*.go` | Versioned recovery records, identity, exclusive ownership |
| `queue*.go`, `cleanup*.go` | Persistent queue, desktop protocol, transitions, deletion, snapshots; queue_playlist.go owns atomic batch admission and replay receipts |
| `adaptive.go`, `automatic_queue.go`, `request_budget.go`, `worker_control.go` | Pure policy, live coordination, request leases, safe worker targets |
| `measurements.go`, `hardware*.go`, `resource_guard.go`, `storage_observations*.go`, `transfer_buffers.go` | Runtime observations and resource controls |
| `desktop/` | Fyne Downloads/YouTube/Settings, companion client, migration, units, history, batch controls; youtube.go handles metadata lifecycle and queue submission; youtube_selection.go handles selection and destination validation; youtube_playlist.go handles paged selection and per-item preparation |
| `desktop/assets/`, `packaging/linux/` | Embedded app icon and Linux launcher template |
| `.github/workflows/ci.yml`, `release.yml` | CI plus native Linux/Windows release checks, packaging, checksums and joint publication |
| `internal/winplatform/`, `internal/platformcmd/`, `internal/platformfile/`, `internal/buildinfo/` | Shared Windows primitives, hidden companion consoles, replacement-safe metadata readers and one version source |
| `packaging/build_release.py`, `packaging/windows/`, `packaging/linux/` | Paired .deb/.exe installers, app icon, system Linux/per-user Windows install/uninstall; no browser permission changes |

The engine currently lives in the root `main` package. A future `cmd/` and `internal/` split is optional, not implemented architecture. The browser preview uses `browserbridge/`, `cmd/fluxget-native-host/`, `extensions/`, and the two Python packaging helpers; these do not restructure the transfer engine.

## Current behavior and correctness contracts

### Transfers and output

- Accept public direct HTTP/HTTPS URLs. Verify TLS, allow up to nine redirects, and reject HTTPS-to-HTTP downgrade. Treat headers and filenames as untrusted; explicit names take precedence over safe server names and URL fallbacks.
- Stream to an exclusively created `.part` file. Unknown-length responses have byte progress without invented percentages. Reject unexpected statuses, truncated bodies, invalid ranges, changed representations, and unsupported range transformations.
- Eligible Linux/Windows files of at least 1 MiB can use parallel ranges after a strong-ETag/range probe. Validate each actual response's range, total, validator, and body length; never write a full response blindly at a range offset. Unsupported servers use a fresh sequential stream.
- Publish only after required synchronization, close, and verification. Final publication uses a same-directory hard link and does not overwrite an existing destination. Failure keeps the partial file; hard-link support is required.
- Standalone downloads accept an optional trusted expected SHA-256 checksum. Streaming/prefix digests and server validators do not replace that trust check. Queue checksum input remains unimplemented.
- HTTP 408, 429, 500, 502, 503, 504 and selected transient network failures may retry, subject to safe resume. Permanent validation/storage failures follow their error path rather than being tuned around.

### Persistence and recovery

- Queue reads schemas 1–6. First media admission creates `queue.json.before-media` and uses schema 5; first playlist submission creates `queue.json.before-playlist` and uses schema 6. Without browser receipts or proxy jobs it keeps schema 2; the first accepted browser handoff backs up `queue.json.before-browser` and writes schema 3. Adding/enabling a proxy job through the scheduler backs up `queue.json.before-proxy` and writes schema 4, preserving browser receipts. Disabling that job's proxy does not downgrade the queue. Older CLI builds reject the upgraded format instead of silently losing routing, so retain paired current executables and the backup. Checkpoint v4 records verified offsets/hashes within unfinished parts; versions 1–3 remain readable. Keep formats versioned and validate paths, identity, ranges, sizes, and partial availability before reuse.
- Synchronize partial data before publishing metadata: write temporary JSON in the metadata directory, sync and close it, then replace the record. Failed updates must not claim unsaved progress. Locks prevent duplicate FluxGet writers; unrelated programs can still race filesystem changes.
- Parallel recovery requires a strong validator and validated ranges. Sequential recovery can instead compare the saved prefix with one fresh known-length response before appending; this redownloads the prefix and does not authenticate the source.
- Prefix checkpoints occur after roughly 1 MiB or two seconds while data arrives, plus clean interruption. A crash may lose work since the last safe checkpoint. Unrecorded prefixes in older checkpoints cannot be recovered retroactively.
- Explicit `-resume` can verify/reconcile interrupted final publication using its completed-file digest and identity. Older records without publication state require manual inspection. Startup-wide reconciliation is not complete.
- A crash before the first record can leave an undiscovered partial file. Process-kill tests do not establish power-loss durability; that guarantee remains out of scope until separately specified and tested.
- Queue/checkpoint/history metadata contains complete source URLs, including approved CDN query tokens; keep metadata private. Human-readable browser failure diagnostics redact URLs. Cookie/header credential storage and authenticated workflows need a separate design. Never execute downloaded content or shell commands derived from it.

### Lifecycle and file deletion

| Action | Behavior |
|---|---|
| Pause | Cancel requests, wait for writers, checkpoint safely, retain partial data; report save failures |
| Resume | Validate saved data and remote identity before reusing bytes; queue within limits |
| Cancel | Stop safely and keep partials by default; no automatic resume |
| Reopen app | Restore active/waiting desktop work paused; opening does not start transfers |
| Download now/later | New approved jobs start when eligible; later jobs remain paused |
| Retry | Retry an eligible failed job through existing validation and retry rules |
| Fresh restart/discard | Explicitly remove only verified owned partial data; persist cleanup intent for interrupted cleanup |
| Remove from list | Keep files; archive the entry so checkpoint discovery does not recreate it |
| Delete files | Confirm separately, stop writers, validate ownership/identity, report individual failures |

Automatic partial cleanup requires recorded file identity, valid saved hashes where applicable, and a random ownership marker (Linux user extended attribute; Windows NTFS alternate stream). Windows opens transfer files with delete sharing, refuses leaf reparse points, and locks a byte beyond valid file data so mandatory locks do not block worker writes. Legacy/unmarked data requires manual inspection. Completed deletion checks the confirmed path/device/inode/size/mtime and rejects symlinks and conflicts. Replacement downloads a separate copy and verifies the original before replacement; it is not permission to overwrite an arbitrary file.

Persisted queue states include queued, running, paused, failed, completed, canceled, and resetting. Runtime snapshots distinguish connecting, downloading, verification, retry/budget waits, and retirement. Future state-model changes must document mutable-field ownership and migration.

## Desktop experience

Implemented: Downloads/Settings tabs; one list with All, Downloading, Waiting, Paused, Completed, and Failed filters; automatic scheduler startup; Add download with now/later; contextual and batch actions; safe duplicate choices; folder/file actions; and default/shared/individual live speed limits. Canceled entries remain in All/history.

Rows show filename, current aggregate speed, progress, useful remaining time, and actual part states. Sequential fallback has one part. Received counters are runtime observations, not proof of durable saved bytes. A displayed 0% part can be below 1%; Parts details show amounts and ranges. Fixed saved parts remain distinct from active connections.

Display defaults to decimal MB/MB/s with KB/MB/GB options. The engine stores whole KiB/s limits; short editor values round-trip to the same cap instead of exposing digits such as 0.999424. Remove limit disables the individual override; default and shared limits still apply. Recent speed excludes saved bytes, becomes zero after a stalled progress interval, and is a dash for inactive rows.

Legacy queue/history/settings migration backs up originals, maps stable queue IDs and speed keys, and imports eligible checkpoints paused. Unimportable entries remain visible. Existing completed history and partial files are preserved; new browser intake will reuse the same scheduler.

Still required: native interactive review of add/pause/resume/open-folder, migration, competing ownership, duplicate names, shared actions, units/limits, long filenames, unknown totals, keyboard navigation, accessibility, packaging, signing, and distribution. The operator approved the preview on 2026-11-09; this does not close every acceptance item.

## Optional proxy routing

**Implemented 2026-11-12:** `networkproxy/` supplies one validated configuration contract shared by the Go transfer engine, desktop and media adapter. It supports HTTP, HTTPS and SOCKS5/5h endpoints, including local v2ray ports. SOCKS addresses normalize to `socks5h` so both backends send destination hostnames to the proxy. The default is disabled; Direct uses the OS route without application-level proxying. System VPN/TUN routes are outside this setting.

- Settings saves a proxy address and **Use proxy by default for new downloads**. The address remains available for a one-off proxied job when the default is off. The Add dialog has **Download with proxy**; browser handoffs snapshot the current default when the job is prepared. Changing defaults affects new jobs only.
- Each row shows a **Proxy** or **Direct** button. Changing an existing job requires a paused/failed/canceled state and a completed browser handoff. The user can disable proxying without fixing or entering an address. Changing routes does not discard saved data or automatically resume the job; normal source/prefix/range validation still applies on resume.
- Persist the chosen enabled endpoint with the job, not just in global preferences. Requests, probes, redirects, retries, parallel workers and recovery use a fresh transport with that same fixed route. Connections cannot leak between routes. First proxy use upgrades the backed-up queue to schema 4; direct-only legacy queues remain readable and do not migrate merely because the setting is available.
- Direct explicitly bypasses HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY environment behavior. An enabled route always selects its endpoint, including for loopback targets, and fails without an automatic direct fallback. HTTP CONNECT retains normal destination TLS validation. Proxy bypass lists, PAC/system-proxy discovery and automatic route switching are not implemented.
- Address validation rejects userinfo/passwords, query strings, fragments, unsupported schemes and subscription links without echoing the rejected input. This first version is for credential-free listening endpoints; authentication to a remote service can be handled by the user's local proxy client. Authenticated proxy profiles require a separate credential-storage design.
- Standalone CLI transfers accept `-proxy <ADDRESS>`; empty means Direct. CLI queues retain per-job routing from their saved jobs; new proxy jobs and route edits use the desktop controls rather than a process-wide queue override. The media probe accepts the same flag and `media.NewWithProxy` applies it to analysis and transfer through explicit yt-dlp arguments. The later media UI must pass the selected job route to that constructor.
- Tests cover HTTP routing, environment bypass, no fallback, verified HTTPS CONNECT, SOCKS remote DNS, parallel ranges/redirects, per-job persistence and route-edit guards, browser receipts under schema 4, CLI routing, settings/toggle/row reuse and real yt-dlp proxy-on/direct-off fixture transfers. Linux runtime evidence and Windows cross-builds do not substitute for native Windows acceptance.

Transport references: [Go HTTP transport proxy behavior](https://pkg.go.dev/net/http#Transport), [yt-dlp proxy options](https://github.com/yt-dlp/yt-dlp#network-options). Proxy support does not itself establish that a particular public YouTube sample is reachable.

## Automatic download management

**Default:** Automatic for new desktop settings and settings without an explicit mode. Preserve prior folder/speed/concurrency values and an explicit Manual choice. Customization stays collapsed; no setup is required. Manual restores configured file/worker concurrency. Neither mode starts a user-paused job merely because settings change.

| Stage | Status | Delivered behavior |
|---|---|---|
| 1: Measurements and request budgets | ✅ Implemented | Atomic global/origin leases, transfer observations, pure policy, default Automatic UI |
| 2: Safe live resizing | ✅ Implemented | Durable per-worker retirement and verified suffix reuse within saved part boundaries |
| 3: Measured adaptation and admission | ✅ Implemented | One-variable trials, useful-worker selection, file draining, limit/cooldown awareness |
| 4: Hardware observations | ✅ Implemented | Linux CPU/memory signals, storage guards, bounded owned buffers, Settings diagnostics |
| 5: Full adaptive acceptance | ⬜ Next | Replay/native comparisons with Manual and operator review |

### Ceilings and request admission

| Resource | Default | Bound / meaning |
|---|---|---|
| Simultaneous files | Maximum 5 | 1–64; Automatic can use fewer |
| Workers per file | Maximum 4 | 1–16; limited by saved eligible parts; sequential uses one |
| Automatic in-flight requests | 8 total, 4 per origin | Total 1–64, per-origin 1–16; includes probes/retries/redirect hops |
| Owned streaming/hash slices | 32 KiB of each per queue transfer/worker | Reserve before launch; aggregate caps 128 MiB / 32 MiB |
| Connect / TLS / headers / body inactivity | 10s / 10s / 30s / 60s | No fixed total transfer-duration limit |
| Retry budget | 3 after initial attempt | Cancelable 1s, 2s, 4s backoff; Retry-After up to one minute |

An origin is scheme, lowercase hostname, and effective port, not a URL or an entire CDN. Requests are not necessarily separate TCP connections, particularly with HTTP/2. Global/origin budgets apply in Automatic; Manual keeps configured file/worker limits. Lower ceilings prevent new excess work while issued work drains.

Acquire global and origin capacity atomically; release exactly once on completion, close, error, redirect, or cancellation. Never hold capacity through retry backoff. Prefer admitted jobs with fewer grants; FIFO file admission considers eligible origins so a cooling origin does not block others. Sessions/waiters remain bounded. Fairness does not promise bounded waiting for huge files or periodic file preemption.

### Measurements and decisions

1. Start with one eligible file and one worker. Sample fresh accepted bytes once per second; saved bytes do not count. Measure raw network bytes, request/header/read waits, limiter/budget/retry waits, writes, sync, checkpointing, and local verification separately. Durations summed across workers can exceed wall time.
2. Require two usable 10-second baseline windows, change one variable across the queue, wait for stable ownership plus five seconds, then compare two windows. Trials spanning errors, retries, unstable windows (>1.5 ratio), worker/configuration/membership changes, unavailable speed settings, or interrupted sampling cannot establish gain. Only the expected file admission preserves its trial.
3. Keep an extra worker only with >=10% aggregate gain, affected-file throughput >=90% of baseline, and no doubled header-delay estimate where available. Favor files with fewer workers and at least 16 MiB remaining. Useful file opportunities take priority. Success waits at least 30 seconds; failed trials wait at least a minute before reconsideration.
4. Unhelpful workers retire safely. Unhelpful file trials lower desired admission and let already-started files finish; do not cancel files to force the target. Show draining honestly.
5. At roughly 95% of an applicable speed ceiling, confirmed by limiter waits, freeze relevant growth. A per-file ceiling can still leave room for another file; a shared ceiling cannot be bypassed. Unknown observations do not establish link capacity or gain.
6. HTTP 429/503 halves affected origin capacity/targets, minimum one, with shared cooldown. Retry-After beyond a minute stops automatic retry until explicit user action. After a quiet minute, recover at most one origin per tick and reset comparisons. Repeated timeouts reduce the affected file; unrelated origins can continue within ceilings.
7. Resource pressure overrides growth. Connectivity/configuration changes discard unproven learning; restart discards all learned targets. Header delay is a supporting signal, not network RTT or another app's responsiveness. Normal operation does not generate external speed-test traffic or inspect browsing.

The pure policy performs no filesystem/network/UI work. The queue owns decisions; transfer owners apply targets. Settings, grants, cooldowns, learned speeds, hardware readings, and filesystem timing observations have distinct ownership. Only user mode/ceilings persist; measurements, learned targets, leases, and hardware identities stay runtime-only.

### Saved parts and safe resizing

New Automatic transfers use fixed boundaries up to the ceiling, with roughly 256 KiB minimum parts, and initially run one worker. Existing four-part checkpoints stay four parts even if the ceiling becomes eight; live splitting/work stealing requires a separate recovery-format design. Persisted per-job worker overrides remain future work; structured worker targets are runtime controls.

A retiring worker stops HTTP work, finishes writes, and acknowledges a durable prefix before ownership is released. A replacement rehashes that prefix and requests `part.Start + savedLength` with fresh validation. Retirement is not a failure, whole-job pause, or retry charge; HTTP/2 cancellation is distinguished from real errors. Sequential streams drain rather than being repeatedly canceled for tuning. Checkpoint failures stop the affected transfer without falsely reporting safe saved bytes.

### Linux resource guardrails

The asynchronous adapter samples once a second; proc/sys reads do not block the queue owner. CPU uses engine process time relative to affinity and the minimum visible ancestor cgroup-v2 quota. Capacity/scope changes reset learning; readings older than 3.5 seconds are Unknown. Cgroup v1, inaccessible/malformed readings, and unsupported platforms do not invent healthy CPU/memory values. Constraints outside a cgroup namespace cannot be inferred.

| Signal | Backoff threshold | Recovery threshold |
|---|---|---|
| CPU utilization | >85% for 10s | <70% for 30s |
| Quota throttling | >=10% for 10s | <5% for 30s, with usable CPU observations |
| Memory PSI avg10 | some >=10% or full >=1% for 10s | some <5% and full <0.5% for 30s |
| Finite ancestor memory.high/max | Usage >=90% for 10s | <80% for 30s |
| Host available RAM | Below max(64 MiB, 5%) for 10s | Above max(128 MiB, 10%) for 30s |
| Local memory high/max/OOM events | New event: immediate backoff | 30s healthy observations, including the triggering source |
| Storage p95 | >500ms or >2× healthy baseline in two 10s windows | Observe one-worker reduction; retain only with >=20% latency improvement |

Memory includes host/cgroup pressure, visible ancestor limits, local event deltas, and engine-only RSS. Local counters avoid attributing a sibling's private limit event to FluxGet; long/missing intervals preserve event evidence. Missing triggering signals cannot clear pressure. These thresholds are conservative hypotheses, not optimal settings or an OOM-prevention guarantee.

Pressure holds new file admission/upward trials and reduces one parallel worker at a time, no more than once per 10 seconds. Sequential streams continue. Manual ignores hardware-driven target changes. Recovery resumes measured learning, not an immediate return to maximum workers.

Destination writes/data sync and checkpoint writes/sync/publication use separate bounded histograms and filesystem identities. Device IDs identify filesystem roles, not HDD/SSD type. Baselines belong to job/operation/filesystem. Only one storage reduction trial runs at a time; compare two valid post-change windows, restore an unhelpful reduction where current settings permit, expire after a minute, and wait a minute before reconsidering. Error/retry/retirement windows cannot prove improvement. Missing operations remain Unknown; p95 is a bucket upper-bound estimate with overflow capped at 60 seconds. Storage errors still fail safely rather than being tuned around.

Owned buffers allocate lazily after reservation, wait cancelably if exhausted, and release once when ownership ends. Copy fast paths cannot bypass this accounting. The caps cover these slices, not total RSS, GUI memory, Go/HTTP/TLS allocations, or OS cache. No new dependency, settings requirement, queue schema, or checkpoint version was added for stage 4.

### Stage 5 acceptance gate

- [ ] Compare Automatic with fixed Manual baselines using replayable workloads and native trials; record environment, versions, filesystem, limits, and variability.
- [ ] Cover a fast single stream, useful parallel ranges, changing bandwidth, individual/shared speed ceilings, and one throttled origin beside a healthy one.
- [ ] Verify 429/503 and short/long Retry-After, cross-origin redirects, HTTP/2, unknown-length sequential work, FIFO/grant fairness, and simultaneous settings changes.
- [ ] Exercise CPU quotas, low-memory pressure, HDD/SSD, slow checkpoint storage, and separate destination/checkpoint filesystems.
- [ ] Kill/reopen during retirement and checkpoint publication; verify exact output, no duplicate writers, missing bytes, falsely complete parts, leaked leases, or silently resumed paused jobs.
- [ ] Complete native Settings/Downloads review and document limits before claiming the default Automatic experience accepted.

## Browser integration — development preview

**Goal:** Capture supported browser downloads into the same Downloads list when enabled, with independent Chrome/Firefox/Opera controls and a reliable browser fallback. Begin on Linux/amd64 after the selected Automatic foundations and desktop acceptance.

**Implemented architecture:** WebExtension -> browser native messaging -> small Go host -> private local IPC -> single desktop/scheduler owner. Linux uses a private 0700 runtime directory, 0600 Unix socket, same-UID peer checks and exclusive ownership; Windows uses a SID-scoped local named pipe with a protected current-user ACL, remote-client rejection, exclusive ownership, and peer process-SID checks; other OSes return Unsupported. The extension owns browser download operations; the desktop owns jobs and integration policy. Native hosts must not start competing queue owners.

Native messaging avoids a public listener and gives explicit extension identities, but needs per-browser/package registration. Localhost HTTP is an alternative only if a documented environment makes native messaging unsuitable; it adds port/authentication/origin/lifecycle work. A custom URL scheme can support explicit links but cannot alone provide general capture.

### Settings and consent

| Control | Behavior |
|---|---|
| Master integration switch | Off until opt-in; disabling stops new extension intake, not existing FluxGet jobs |
| Chrome / Firefox / Opera | Independent switches and per-profile consent/readiness; selecting one never enables others |
| Status and setup | Not installed, Extension missing, Disconnected, Ready, Disabled, Unsupported; installation/test/repair actions |
| Capture mode | Ask first by default; optional automatic Download now after setup; Manual only for context-menu sending |
| Extension popup | Effective policy, settings shortcut, Use browser once, temporary capture pause; local disable wins |
| Exceptions | Per-site/file-type rules; exclusions win; distinguish initiating site from download host |
| Private browsing | Off; separate permission/consent and tested privacy behavior required before support |

Effective readiness requires master, browser, profile consent, and extension readiness. Normal capture setup requests all HTTP/HTTPS website permissions once, with excluded download hosts (including subdomains) managed in desktop Settings; users do not maintain a per-site allowlist. Website access remains an explicit browser approval, and old limited grants remain until approved. Manual-only profile setup requires no website monitoring permission. The host also validates policy; stale/unknown/disconnected state leaves new downloads in the browser. Define changes during pending handoff explicitly. Manual sending works when only automatic capture mode is off; master/browser disable blocks all extension intake.

### Implementation and native evidence (2026-11-10)

- `browserbridge/` defines strict version-1 framing, 64 KiB messages, URL/filename checks, persisted master/browser/profile policy, exclusions, and bounded Linux IPC. `cmd/fluxget-native-host` validates the pinned browser extension identity and relays to the existing owner; it can activate the sibling desktop when closed, without creating a separate scheduler. Closed-app activation still needs broader native acceptance.
- `queue_browser.go` persists up to 2,048 request receipts in schema 3 or 4. Prepare creates one paused job without network activity; only confirmed cancellation activates it. Replayed IDs cannot create duplicates or resume a user-paused job, and receipts survive list removal. Unconfirmed jobs cannot be started through normal Resume. Expired/disabled/Download-later handoffs remain paused after confirmed cancellation.
- `desktop/browser_integration.go` adds master and independent Chrome/Firefox/Opera controls, ask/automatic/manual modes, excluded hosts, profile consent counts/disconnect, destination prompts and native error responses. It uses the same queue client and owner, with one browser prompt at a time.
- `extensions/src/` is shared plain JavaScript/CSS/HTML with separate MV3 manifests/background adapters. The pinned Chromium ID is `phfanjlpchehjklocnblfjdhgmlcjoaa`; Firefox uses `fluxget@parsania80.github.io`. The popup exposes connection/profile consent, one-time all-website access/removal, limited-grant readiness, a 15-minute bypass and pending-handoff recovery. Manual sending uses the link context menu. Exclusions remain in the existing desktop policy rather than a separate popup list.
- Capture observes only explicitly granted sites through optional `webRequest`/host permissions. It requires one matching original-to-final GET trace, observed request headers with no Cookie/Authorization/Range, current site/profile policy, a non-private active browser item marked safe, and an HTTP/HTTPS URL without embedded credentials/fragments. Operator-approved query-string/CDN support preserves the final URL exactly through native transport, durable receipt validation and engine intake. Diagnostics redact URLs; local transfer metadata retains them. The popup displays specific fallback reasons near the top. It stores only credential-presence booleans, never cookie/header values. Unknown, cached, ambiguous, partially observed, POST, warning/scan-pending and unsupported requests stay in the browser. Site grants are checked again before cancellation. Capture is bounded to four concurrent attempts and 32 recoverable client records.
- The extension persists handoff intent/stages before cancellation. Before cancellation failures resume the browser; after cancellation it retries the same accepted request rather than making another copy. Missing browser state is not assumed canceled. A startup promise prevents concurrent native messages from creating competing profile identities.
- After active ownership is acknowledged, a durable client cleanup stage permits erasing only that handoff's interrupted/USER_CANCELED history item. Cleanup retries never recreate/activate a transfer, and missing browser state during confirmed cleanup is safe to reconcile. Rejected, user-canceled, bypassed and unrelated history stays intact. A brief initial browser entry remains possible with download-event capture; no global download-UI suppression or history sweep is used.
- Optional `scripting` permission enables an isolated document-start/all-frame Ctrl+left-click observer on granted HTTP/HTTPS pages. Trusted gestures create single-use, tab/link/redirect-scoped bypass tickets in session storage, including new-tab opener matching. It does not prevent or rewrite native click navigation. Tickets and used-ID tombstones are capped at 32 (30 seconds for links, five seconds for buttons); opener associations are capped at 64 for 30 seconds. Existing profiles finish shortcut setup via the same website-permission action and reload their pages. Restricted browser pages and delayed/custom site flows retain the temporary popup bypass alternative.
- Go race tests cover private/exclusive IPC and shutdown, malformed/bounded frames, policy save rollback/consent, receipt replay/restart/list removal, no transfer before cancellation, expiry, Download later, and actual output. Node tests cover handoff/fallback, disable and site-revocation races, lost acknowledgments, unavailable/completed browser items, credential/POST/redirect rejection, acceptance removal, identity races and manual-link recovery. CI runs the Node suite without npm dependencies.
- Real **Google Chrome 157.0.8103.50 on Linux** passed an isolated-profile native test: load the actual extension, pair through its real popup, pause/cancel a browser download through a query-bearing redirect, activate one existing-scheduler job with the exact final query, compare exact output, verify an excluded host stays in Chrome despite all-site access, then disable integration and verify the next file completes in Chrome. A separate Ask first run accepted exactly one destination prompt. Follow-up Chrome acceptance verifies a trusted Ctrl+left-click through a query-bearing redirect stays in the browser (including its new-tab path), ordinary capture erases only its confirmed canceled history item, and three browser-managed history entries/files remain intact. Shortcut/content-script permissions are pre-granted only in the isolated test profile. The updated test manifest pre-grants all-site capture only in the disposable test profile; ordinary optional-permission approval and normal installation still require operator review. Test files/profiles/configuration are isolated; sandboxing is retained. Popup/Settings renders were inspected.
- Installed Firefox is **158.0.1 Snap**; the WebExtensions portal is present, but host launch/consent and capture are not yet natively verified. Opera is not installed. Shared source and generated folders do not establish native support for either. Store signing/distribution, broader browser failures/crashes, private browsing and authenticated inputs remain open.

Build, registration, setup, limits and uninstall instructions are in [extensions/README.md](extensions/README.md). Generated folders live under ignored `dist/extensions/`; the installer registers only explicitly selected browsers and does not install extensions or edit browser profile databases. Checkpoints and current Automatic behavior are unchanged.

**Recommended consumer installation (2026-11-11; proposed, not implemented):** Replace unpacked/manual extension setup with a packaged desktop installer that installs the native hosts and browser registrations, plus a first-run per-browser Connect screen. Finalize production extension IDs and matching native allowlists before publishing store packages. Prefer Chrome Web Store distribution and Mozilla-signed AMO distribution; verify Opera independently. Chrome supports installer-assisted external installation using a preferences JSON on Linux and a registry/preferences entry on Windows/macOS; Linux installation can be automatic, while Windows/macOS require enable confirmation. For normal Firefox, link to the signed AMO add-on for user-approved installation. Provide a store-link fallback, permission/profile connection and actual native readiness checks, plus repair/uninstall and updates. Do not promise silent installation across every browser/OS or use managed-device policies as a consumer installer shortcut. Linux self-hosted/local CRX installation is an alternative when store distribution is unavailable, with signing-key custody, stable identity and update hosting required. Browser/Snap/Flatpak acceptance remains a release gate.

Distribution references: [Chrome installer-assisted installation](https://developer.chrome.com/docs/extensions/how-to/distribute/install-extensions), [Firefox desktop companion add-ons](https://extensionworkshop.com/documentation/publish/distribute-for-desktop-apps/), [Firefox signing/distribution](https://extensionworkshop.com/documentation/publish/signing-and-distribution-overview/).

### Delivery order and handoff contract

1. **Capability spike:** Record actual browser versions, profiles, native/Snap/Flatpak packaging, event timing, request metadata, background lifecycle, pause/cancel behavior, and host discovery. Set a finite handoff timeout and supported-input matrix. Do not weaken browser sandboxes.
2. **Shared intake:** Add stable request IDs, pending records, idempotent add/status, app activation, and private IPC. Test simultaneous requests, single-instance ownership, disconnects, and protocol versions.
3. **Native host and manual sending:** Bound length-prefixed JSON on stdin/stdout; reserve stdout for protocol and redact stderr. Validate extension IDs, schema/size, schemes, filenames, and policy. Launch binaries directly, never through a shell. Implement context-menu Send link to FluxGet and connection testing before interception; share JavaScript logic with separate browser packaging.
4. **Automatic capture:** Confirm policy/readiness/eligibility, pause the browser item, and persist a pending FluxGet request without network work. After destination acceptance, cancel the browser item; activate that same request only after cancellation is confirmed. Download events occur after browser work begins, so zero browser bytes cannot be promised.
5. **Failures and crashes:** If already complete, pause fails, user rejects, or acceptance/eligibility fails, abandon pending work and leave/resume the browser item. Persist IDs/stages across extension suspension, lost acknowledgments, and host/app crashes. Never auto-start an unconfirmed pending transfer. After browser cancellation, reconcile the accepted job or show explicit recovery; do not promise canceled items can always resume. Fallback bypasses capture once to avoid loops; do not erase unrelated history/files.
6. **Settings and rollout:** Test disable during handoff; provide user-driven install, repair, and uninstall. Do not silently install extensions or edit browser profile databases. Prove Chrome first, then Firefox, then Opera independently; record browser/version/OS/package capabilities.
7. **Distribution:** Package/register the host for selected browsers; establish stable extension IDs, store submission/signing, minimal permissions, privacy disclosures, updates, and cleanup. Unpacked development extensions are not a release installation experience.

### Supported inputs and acceptance

Start with proven replayable public HTTP/HTTPS GET downloads and user-selected public links. An HTTP URL or download event alone does not prove method, public accessibility, or replayability; unknown cases stay in the browser.

Leave POST/form bodies, blob/data URLs, generated files, authenticated/cookie-dependent downloads, protected downloads, and streaming media in the browser. Query-bearing GET links are now accepted under the same observed-request/security checks; expiry, single-use behavior and successful replay/resume are not guaranteed. Initial permissions cover only demonstrated needs such as downloads, native messaging, storage, and context menus. No cookies, arbitrary headers, request modification, or browsing-history transfer; all-site observation requires browser approval. Redact complete URLs from diagnostics; a public-looking URL is not proof of no secret.

Do not bypass browser malware/security blocks, enterprise policy, or certificate failures. If a security decision cannot be preserved, leave that download class in the browser. FluxGet must not imply it inherits browser scanning guarantees. Later authenticated support must design consent, scoped headers/cookies, redirect-origin restrictions, secure credential storage/expiry, and recovery first.

Browser acceptance remains open:

- [ ] Matching output without duplicate FluxGet jobs or completed browser copies.
- [ ] Independent master/browser/profile/extension disable, exclusions, and temporary bypass across restarts; existing FluxGet jobs continue.
- [ ] Missing host/extension, incompatible protocol, locked queue, startup failure, rejection, and full disk leave a browser download or explicit recoverable pending job.
- [ ] Instant downloads, simultaneous/duplicate events, disable during handoff, extension suspension, browser exit, and every handoff crash boundary.
- [ ] Unsupported, private-without-consent, unsafe, and security-blocked inputs follow the fallback policy.
- [ ] Manual install, approval, connection test, capture, repair, update, and uninstall on each claimed browser/package combination.

## YouTube videos and playlists — next release plan

**Requested 2026-11-12; stages 1–4 implemented; the installed single-video preview is approved, while playlist preview and release acceptance remain open.** Add an explicit YouTube intake to the existing Downloads experience. Start with completed, public videos and finite playlists accessible without a login. Support standard watch links, `youtu.be` links, Shorts and playlist links. A watch link containing a playlist must ask **This video** or **Choose from playlist**, defaulting to the single video. Do not start a playlist merely because its ID is in the URL. The development probe still handles only single videos. The desktop now offers explicit playlist intake with bounded pages.

### Backend choice and dependency boundary

| Approach | Benefits | Costs / decision |
|---|---|---|
| Go adapter around yt-dlp plus FFmpeg | Established extraction and media handling; Go keeps ownership of UI, queue and files | Additional executables and upstream changes; recommended plan, subject to stage 1 verification |
| Native Go extractor and media pipeline | Fewer external programs and direct engine integration | FluxGet must maintain YouTube extraction, manifests and muxing; defer until a proven need |
| Extract stream URLs and give them to the current range engine | Potential reuse of direct-download controls | Expiring URLs, segmented protocols and representation changes need new contracts; defer beyond the first media release |

yt-dlp owns extraction and media transfer; FFmpeg/ffprobe handle merging, conversion and output inspection. Use structured metadata/progress rather than parsing human-readable console messages. These capabilities and interfaces are documented in [yt-dlp's upstream guide](https://github.com/yt-dlp/yt-dlp#embedding-yt-dlp).

Current YouTube extraction also needs EJS scripts and a supported JavaScript runtime. Plan for pinned Deno and locally supplied, compatible EJS components; validate the selected standalone artifact's contents instead of assuming it contains every dependency. See [upstream EJS setup](https://github.com/yt-dlp/yt-dlp/wiki/EJS). This adds a media-tool runtime boundary; the existing Go engine and browser native host do not gain a Python runtime requirement.

The spike resolves explicit absolute tool paths, probes native versions and rejects missing tools; there is no PATH search or self-updater. A Python standard-library helper downloads SHA-256-locked standalone tools into an explicit development directory. On the current host verified copies are installed under `~/.local/opt/fluxget/media-tools` for the preview, not installed system-wide or shipped in v1 installers. Go modules/dependencies remain unchanged. Production dependency installation/notices remain a later release gate.

### User experience and format choices

1. Open **YouTube**, paste a link and analyze it without downloading the media. Show title, duration and available outputs; bounded metadata work must be cancelable.
2. Choose **Video** or **Audio only**, an output format, quality and destination. Default to an available MP4-compatible video/audio combination up to 1080p; if unavailable, offer the best supported alternative explicitly. Never invent a quality or silently upscale.
3. Offer MP4, WebM and MKV only where the selected source codecs and container are compatible. Show resolution, frame rate, codec, audio track/language and exact or estimated size when known. A container extension is not a quality setting; prefer merging/remuxing without re-encoding video.
4. Offer M4A/AAC and Opus when available, plus MP3 conversion with an explicit bitrate choice. Explain that converting to MP3 takes extra processing and does not improve source quality. Broad video transcoding, subtitles and thumbnail embedding can follow later.
5. For playlists, show a bounded, paginated preview with select-all/individual selection, original order and unavailable-entry status. Fetch detailed formats only for selected items. Start with 100 entries per page and a 1,000-item admission cap per batch; larger imports require another explicit batch rather than unbounded expansion.
6. Apply a preferred format/maximum quality to selected videos, resolve it per item and preview exceptions. Let the user accept a lower available quality or skip the item; do not silently change video to audio-only. Show estimated disk requirements, or Unknown, before confirmation.
7. Use one **Download** button after selecting output and destination; do not add a separate Review selection step. Show individual videos in Downloads with an optional playlist group, per-video progress and a completed/failed/skipped summary. Successful items remain successful when another item fails; retry only failed or selected entries.

### Ownership, persistence and controls

```text
Add YouTube link -> Go media adapter -> bounded metadata -> selection preview
    -> existing queue owner -> media job -> yt-dlp -> owned job workspace
    -> merge / convert -> ffprobe validation -> safe final publication
    -> existing Downloads rows, speed, status and playlist summary
```

- `media/` now provides the isolated subprocess adapter for metadata, explicit format selection, scratch transfers, structured progress, cancellation and ffprobe inspection. Stage 3 adds scheduler integration in `queue_media.go`, durable recovery in `queue_media_state.go` and desktop submission; the original HTTP transfer path stays separate.
- The existing queue owner remains the sole admission/persistence authority. Admit one selected video as one job, not an entire untracked playlist process. Persist backend type, original source URL/video ID, selected output policy, destination, tool compatibility information and playlist order/group. Deduplicate repeated submission using video ID plus output profile while allowing an explicit different-quality copy.
- Introduce a backed-up, versioned queue migration before media jobs can be saved; retain old direct-download records and v4 checkpoints unchanged. Media sidecars live in a private job workspace and use a separate versioned recovery contract. Older clients must reject unsupported media schemas clearly. Restored unfinished jobs remain paused.
- Use absolute tool paths and argument arrays, no shell command interpolation or user-provided extra flags. Isolate user tool configuration/plugins; disable implicit updates and unapproved remote component installation. Restrict intake to supported YouTube URLs, sanitize filenames and diagnostics, bound metadata/progress/log buffers, and retain TLS checks. Do not import browser cookies or expose signed stream URLs in logs.
- Treat media titles, thumbnails, metadata and tool-reported paths as untrusted. Stage inside an owned workspace on the destination filesystem; verify path containment, reject symlink/reparse escapes, reserve output names and never overwrite unrelated files. Delete only verified job-owned temporary files.
- Keep discovery, download, merge and conversion as distinct phases. Network speed is meaningful during transfer; merging/converting shows a separate status. Do not show yt-dlp fragments as FluxGet range connections or claim that a merge is another download. Completion requires successful post-processing, ffprobe checks for expected streams/container/duration and safe no-overwrite publication.
- Pause/cancel must stop the whole process tree, including FFmpeg and the JavaScript runtime, on both OSes. Attempt bounded graceful shutdown, then terminate if necessary; retain recoverable owned files. Test resumability rather than promising byte-exact continuation for every media format. Re-resolve expired URLs from the original video and validate source/format identity before reusing partial media; a changed selection starts a new workspace.
- Count media jobs against the global simultaneous-download ceiling. Start with one media transfer and one fragment at a time, and one post-processing task. Existing Go request leases, adaptive worker changes and limiter do not automatically govern external processes. Stage 1 must define explicit subprocess limits; defer adaptive media concurrency until measurements are reliable.
- Honor individual and default speed ceilings through the adapter. Until aggregate mixed-backend limiting is proven, a shared speed ceiling serializes all transfers while media is active and applies the shared ceiling to that media process. Reconfiguration must acknowledge an applied cap or safely pause/restart; never silently ignore it. Verify actual aggregate speed before claiming shared-limit support.
- Browser support starts with an explicit **Send video/playlist to FluxGet** action and the same preview/confirmation. Do not capture normal YouTube page navigation or individual playback segments as ordinary downloads. Reuse the existing browser consent, profile, disable and bypass controls.

### Staged implementation roadmap

| Stage | Task | Status | Required result / verification |
|---|---|---|---|
| 1 | Dependency and backend spike | 🔄 Current | Adapter, artifact lock, tools helper and probe command implemented; real Linux merge/audio/cap and fixture contracts pass; public metadata passed through v2ray; fresh native Windows CI remains open; public MP4 merge and four-item M4A playlist pass |
| 2 | Single-video selection UI | ✅ Done | Cancelable analysis, explicit proxy, compatible Video/Audio choices, quality/MP3 bitrate and destination; one Download button submits to the queue; Linux preview operator-approved 2026-11-13 |
| 3 | Persistent media jobs | ✅ Done | Linux preview installed and operator-approved 2026-11-13: schema-5 queue, progress phases, live speed ceilings, helper ownership, verified publication, pause/resume and restart-paused recovery; broader acceptance remains in stages 1/5 |
| 4 | Playlist selection and admission | ✅ Done | Normal-playlist preview operator-approved 2026-11-13; bounded pages/batches, per-item formats/consent and schema-6 replay receipts/summaries/retry implemented; broader transfer/native checks remain open |
| 5 | Recovery and resource acceptance | 🔄 Current | Isolated Linux recovery, one-hour media, mixed limits and bounded CPU/memory pressure pass; extended wall-clock/storage, native Windows and public acceptance remain open |
| 6 | Optional browser entry point | ⬜ Todo | Explicit send action for video/playlist pages, preview and consent; ordinary playback/navigation and current direct-link capture remain correct |
| 7 | Native packaging and release | ⬜ Todo | .deb/.exe include required tool setup, notices, checksums and diagnostics; clean-machine install/repair/uninstall and media acceptance pass; choose/bump version only then |

**Next acceptance step:** Publish the acceptance branch and pass its native Windows matrix before closing stage 1; public video/merge and four-entry M4A playlist transfers have passed on Linux. Public metadata and a full public audio-to-MP3 transfer have passed through the local proxy. Persistent single-video downloads are available as an installed development preview; normal-playlist selection is installed locally and operator-approved; broader public-transfer acceptance remains open. Do not replace the existing HTTP engine.

### Stage 1 implementation and evidence

- `media/` exposes sanitized video/format metadata, validates actual format IDs/container compatibility, and runs one operation per client with one native fragment. Progress contains scalar counters only; signed stream URLs and raw stderr are not returned. Phase records on stdout and stderr use separate bounded parsers. Unknown totals remain unknown; transfer counters can restart for the next audio/video component and are not yet aggregate queue progress.
- Transfers create an opaque private scratch workspace and inspect output streams/container/duration with ffprobe. They do not publish final user files or offer persistent resume. Scratch paths are returned on errors for inspection; cleanup/publication and crash recovery belong to later stages. The probe's MP4 choice is conservative H.264/AAC up to 1080p; other compatible selections are exposed by the adapter, not a finished UI.
- Linux/macOS cancellation targets the process group with a bounded interrupt/kill sequence. Windows starts suspended, attaches a kill-on-close Job Object before resuming, and terminates the entire job on cancellation; graceful Windows checkpoint recovery is not claimed. A generated child-writer test verifies cancellation and normal-exit cleanup, instead of assuming killing the parent kills FFmpeg.
- The cap is fixed for a run and measured against real fixture bytes. A cap change requires cancellation/restart until stage 3 supplies scheduler acknowledgment. External media requests are not covered by the current Go engine leases or shared limiter; mixed-backend scheduling is not implemented. All program arguments are generated internally, with no arbitrary tool flags or browser-cookie import.

| Evidence | Result | Limit |
|---|---|---|
| Native Linux tool probe | yt-dlp 2026.10.26, EJS 0.8.0, Deno 2.9.8 and FFmpeg/ffprobe N-128757-g4b42530309 verified | Tools are development-only; exact upstream archive checksums pinned |
| Generated local media | Real yt-dlp downloaded separate H.264/AAC streams and FFmpeg merged MP4; separate MP3 conversion passed ffprobe checks | Tests use a loopback server and explicit private fixture info, not public extraction |
| Rate ceiling | 76,550 transfer bytes over about 5.6 seconds with a 16,384 B/s ceiling; structured download and processing phases observed | Single operation; no shared/live limit claim |
| Regression checks | Root race suite, media fixtures/process-tree tests, vet, Windows test/probe cross-build and macOS package cross-build pass | Native Windows media matrix is prepared in ci.yml, not yet run |
| Public YouTube sample | Direct probe initially had no network; with operator-started v2rayN, installed adapter analyzed documented sample YE7VzlLtp-4 (597 seconds, 49 formats). Older clip BaW_jenozKc is unavailable | Public metadata extraction passed via socks5://127.0.0.1:10808; public MP4 merge/full decode and four-entry M4A playlist now pass on Linux; current Windows acceptance remains open; no cookies/login used |

Development-only commands (ordinary app users do not need these):

```bash
python3 packaging/setup_media_tools.py --directory "$PWD/dist/media-tools"
go run ./cmd/fluxget-media-spike -tools "$PWD/dist/media-tools/tools.json" -mode probe
FLUXGET_MEDIA_TOOLS="$PWD/dist/media-tools/tools.json" go test -race -v ./media
```

For an explicitly selected public sample, use the command's `-mode analyze -url "<PUBLIC_YOUTUBE_VIDEO_URL>"`; `-mode video` or `-mode mp3` also needs `-directory "<ABSOLUTE_SCRATCH_PARENT>"` and optionally `-limit <BYTES_PER_SECOND>`. Add `-proxy "<PROXY_ADDRESS>"` to analyze/download through a configured local proxy. `FLUXGET_MEDIA_TEST_URL` enables the opt-in public metadata test, with optional `FLUXGET_MEDIA_PROXY` for its route. Ordinary CI uses only generated local media and never needs YouTube access. The pinned development FFmpeg artifact is a GPL build; it is not bundled into production installers by this change, and its redistribution/source obligations must be resolved before stage 7.

### Stage 2 selection preview

- The operator approved building this UI while stage-1 native Windows and full public-transfer checks remain pending. Those acceptance gates are still open; passing UI fixture tests does not close them.
- The desktop now has Downloads, YouTube and Settings tabs. YouTube performs bounded metadata analysis through the selected Direct/proxy route, with cancel and shutdown waiting for owned subprocess cleanup. Editing the source, tools manifest or proxy choice invalidates stale selections; canceled/late results are discarded.
- Shared media selection validation supplies only compatible output formats. The default is available MP4/H.264/AAC up to 1080p; a missing MP4 option is explained explicitly. Available resolution/FPS/codecs and audio language/bitrate are shown. Muxed video keeps its included audio; audio-only M4A/Opus and MP3 with 128/192/256/320 kbps are offered only when supported.
- Destination review uses a sanitized editable filename and the chosen extension, rejects unsafe/reserved names and existing files, and distinguishes source-transfer estimates from final file size. Analyze creates no media files or queue requests and saves no draft after closing. The stage-3 Download button validates the selection, submits one persistent job and opens Downloads; there is no separate review step.
- Development tool discovery uses an explicit FLUXGET_MEDIA_TOOLS manifest, an adjacent media-tools/tools.json, or the user's selected manifest under the collapsed Media tools section. No PATH search, automatic install, remote update or production packaging change was introduced.
- Headless desktop tests cover tab integration, format/mode/track changes, proxy snapshots, single-video handling for watch URLs containing a playlist, explicit playlist routing, missing tools, redacted errors, cancel/close reaping, safe destinations, single-submit Download and processing progress. The native CI matrix includes these tests; local Windows verification remains cross-compilation only.


### Stage 3 persistent media preview — implemented 2026-11-12

- **Operator preview review: ✅ Done (2026-11-13).** The operator tested the locally installed Linux preview, reported that it is good and requested marking the review done. Native Windows, public video/merge and broader recovery/resource acceptance remain open.

- A single Download button submits a minimized, validated video/format selection with a captured proxy route to the existing queue owner. Media phases and aggregate component counters appear in Downloads; merging/conversion does not show a fabricated transfer speed or HTTP range parts.
- Queue schema 5 preserves older input schemas and creates `queue.json.before-media` before first media admission. Older v1 binaries cannot read schema 5; rollback must use a compatible queue backup rather than silently discard media jobs.
- Admit at most one media job at a time, counting it against the normal active-file ceiling. Media requests are outside Go HTTP request leases. With an overall speed cap, serialize HTTP and media transfers; live changes cancel/reap/save and restart media under the new effective ceiling. Automatic media connection adaptation is not implemented.
- A queue-owned helper watches stdin EOF, owns the job lock and reaps its tool tree on pause, queue death or limit changes. Clean recovery validates saved local file identity/hash, exact tool versions and fresh selected representation metadata before yt-dlp continuation. Unclean or changed scratch requires an explicit Restart; suspect files are preserved for inspection. This does not guarantee byte-exact resume after every upstream representation change.
- Recovery records use a separate `.media` sidecar and opaque owned workspace. ffprobe checks output streams, selected quality and duration before durable no-overwrite publication. Interrupted publication can recognize its own final file; unrelated files, symlinks and foreign hardlinks are refused.
- Verification: full root race suite with real tools passed (engine 134.539 seconds; media 21.262 seconds); full desktop headless race passed (45.140 seconds). Actual queue-driven generated MP4 merge and MP3 conversion passed. Regressions cover pause/resume, cap changes, mixed-backend draining, SIGKILL/EOF cleanup, migration, unsafe scratch and destination conflicts. Root/desktop vet and Windows test cross-builds passed; native Windows runtime and full public YouTube transfer remain open.
- Installed paired engine/GUI and verified media tools at `~/.local/opt/fluxget`; running GUI hash matches the new build. Existing idle queue and settings were retained. Preview backup: `~/.local/state/fluxget/backups/20261112-110219-youtube-download-preview`. No release/package/version change or commit/push.
- Initial stage-3 limitations included a 30-minute transfer timeout. Stage 5 below removes that cutoff and adds bounded Linux recovery/resource and one-hour media checks. Extended wall-clock operation, power loss, public expiry and paused progress display after abrupt queue death still need acceptance. Stage-4 normal-playlist preview is now operator-approved below.


### Stage 4 playlist selection — implemented 2026-11-13

- **Operator review: ✅ Done (2026-11-13).** Normal playlists work and the operator approved committing the implementation. Bounded Mix selection is explicitly out of scope; the source includes a specific Mix-rejection message and the This video alternative. The running preview was not replaced for that message-only follow-up.

- The YouTube tab offers **This video** or **Choose from playlist**. Watch/Shorts/short links default to one video; a playlist-only link selects playlist intake. Only finite public playlist/upload-list IDs are accepted; channels and radio mixes are rejected.
- Enumeration uses yt-dlp flat/lazy metadata, a 60-second timeout, a 2 MiB output cap and 100 entries per page plus one look-ahead entry, with playlist positions bounded to one million. Entries retain original indices; private/deleted/live/unavailable entries are visible and disabled. Selections survive paging and are capped at 1,000 per batch; another batch is explicit.
- **Load selected formats** analyzes only chosen videos through the captured proxy route. Preferred Video/Audio, container, maximum video quality and MP3 bitrate resolve per item. Actual format/track details and skipped entries are shown in their page; lower available resolutions require explicit approval. No video-to-audio fallback or upscaling occurs. Estimated source sizes, or Unknown, appear before the single **Download** action; temporary processing space can be additional.
- Each selected video becomes an ordinary queue job with its playlist batch/order. Generated names include order, sanitized title/video ID and resolution or MP3 bitrate; destination conflicts preserve existing files and skip that item. Existing queue duplicate checks allow separate output profiles and prevent duplicate video/format work.
- The queue owner saves jobs and a fingerprinted replay receipt in one schema-6 transaction after `queue.json.before-playlist` backup. Schema 1–5 remains readable; older clients reject schema 6. Receipts survive job removal, so a lost acknowledgment/restart can reconcile the same batch without recreating work. Retain paired binaries and a compatible queue backup for rollback.
- Downloads rows identify playlist/order; the summary counts completed, failed/canceled, pending, skipped and removed entries. Existing selected-item Retry handles failures without restarting completed items. The existing one-media-job scheduler and shared cap behavior remain unchanged.
- Resource bounds remain explicit: at most 128 retained batch receipts, a 900 KiB request budget and the existing 1 MiB saved queue limit. Large metadata or a nearly full queue can require fewer than 1,000 items. Receipt history is retained rather than evicted automatically, preserving replay protection; history-full errors require explicit follow-up.
- Verification: full engine race suite passed (146.320 seconds), real-tool media race suite passed (21.062 seconds), full headless desktop race suite passed (33.439 seconds), and both modules passed vet. Pinned yt-dlp offline pagination, UI paging/selection/cancel/quality checks, native fixture merge/conversion, queue partial success/retry and actual queue kill/reopen/removal replay checks passed. Native Linux engine/GUI builds, runtime library checks and Windows root/media/desktop test cross-builds passed; top/bottom playlist renders were inspected at 1100×720.
- Installed on 2026-11-13 under `~/.local/opt/fluxget` after a normal idle shutdown and hash-verified backup at `~/.local/state/fluxget/backups/20261113-143733-playlist-preview`. Native GUI PID 198872 and queue companion PID 198900 match current build hashes. The existing schema-5 queue and paused job, settings and all saved metadata remained byte-identical at verification; only a future real playlist admission will upgrade that queue to schema 6. Public enumeration of the pinned upstream four-entry sample `PL6IaIsEjSbf96XFRuNccS_RuEXwNdsoEu` passed through the saved proxy, with four selectable entries and no media transfer or queue admission. The actual installed engine passed isolated schema-6 admission/backup, SIGKILL/reopen/removal replay and two real-tool playlist jobs (MP4 merge and MP3 conversion), verified for expected streams/duration by ffprobe. Temporary fixtures were removed. The operator approved normal-playlist behavior on 2026-11-13. Broader public playlist transfers, native Windows runtime, recovery and production packaging remain open.

### Stage 5 recovery/resource acceptance — in progress 2026-11-13

The first acceptance pass uses isolated configuration, generated three-second H.264/AAC media, loopback HTTP and the pinned native tools. Only discovery and processing timing are fixtures; native transfer, FFmpeg, ffprobe, companion ownership, recovery and publication use the production path. No user download/settings or installed binaries are changed.

| Check | Linux evidence | Remaining limit |
|---|---|---|
| Pause/reopen and queue death | Existing companion pause/resume and SIGKILL/EOF tests pass; reopened jobs stay paused | Extended wall-clock and power-loss behavior remain unverified |
| Interrupted merge/conversion | Cancel while real FFmpeg has an output open; verified scratch resumes to validated MP4/MP3 | Short generated sources; native Windows pending |
| Refreshed media links | Pause within a stream, invalidate old paths, reanalyze and resume from new paths | Fixture URL rotation, not public CDN expiry acceptance |
| Missing tools or changed source/tool versions | Recovery refuses before modifying the previous record or verified scratch | Public representation changes need broader samples |
| Full destination/metadata storage | Private 1 MiB tmpfs produces real ENOSPC; no final output, verified scratch, retry after freeing space; failed record replacement retains prior bytes | No stress against user filesystems; power loss unverified |
| Mixed HTTP/media shared ceiling | Production-style shared HTTP limiter plus media cap; serialization and direct output integrity checked; average transfer stays below the cap | Small loopback workload; no instantaneous-burst or total-memory bound |
| Completed `.part` / HTTP 416 | Native regression preserves data and publishes nothing; explicit Restart completes | Automatic continuation is not guaranteed; a fixed redacted error now explains restarting |
| Long content and transfer lifetime | One-hour native MP4/MP3 outputs pass within one second of requested duration; a virtual 31-minute slot wait reproduces the former deadline and now stays cancelable | The one-hour content transfers faster than playback; no real >30-minute wall-clock run claimed |
| Bounded real CPU/memory pressure | Half-CPU native trial completes under 384/512 MiB soft/hard limits with memory.high events and no OOM; separate near-limit trial latches both guards and clears after healthy observations | Backend/test tree only; no full desktop, forced OOM, or broad hardware matrix |

Latest verification: full root race suite passed after the lifetime change (engine 241.561 seconds; real-tool media 21.158 seconds), including the one-hour cases and virtual deadline regression. Hardware/resource/queue guard races passed (1.657 seconds), affected desktop YouTube races passed (6.958 seconds), root vet and Windows root/media test cross-builds passed. The opt-in real pressure trial passed separately under its dedicated service; it is intentionally skipped in the ordinary suite. CPU/RSS observations for the mixed fixture are recorded in [BENCHMARKS.md](BENCHMARKS.md). The additional constrained one-hour and live pressure trials are recorded there too. Cgroup peaks cover the isolated backend/test/tool tree, not a general memory bound or the GUI; extended wall-clock/slow-storage, forced-OOM, public-transfer and Windows acceptance remain open. Stage 5 remains Current. The HTTP 416 diagnostic and removal of the transfer-only 30-minute cutoff are source changes; the installed preview is unchanged. Metadata/tool-probe/inspection deadlines, network inactivity limits and caller cancellation remain.

### Public transfers and Windows CI handoff — 2026-11-13

- Current Go adapter plus unchanged local proxy completed `YE7VzlLtp-4`: 104,130,578-byte MP4, H.264 720p/24 fps plus AAC, duration 596.544 seconds. Production ffprobe checks and a full FFmpeg decode passed. Earlier public audio-to-MP3 evidence remains valid.
- Opt-in `TestMediaQueuePublicPlaylist` enumerated all four entries of `PL6IaIsEjSbf96XFRuNccS_RuEXwNdsoEu`, loaded real formats, atomically admitted schema-6 jobs and completed all four as M4A through the actual queue/helper/tools. Durable summary: four completed, zero failed/pending/skipped. Test time 257.211 seconds. Existing settings, queue and installed binaries were not used; temporary outputs/metadata were removed.
- This establishes these Linux samples and profiles, not every public video, Shorts/container combination, playlist video quality or public-network recovery behavior. See [BENCHMARKS.md](BENCHMARKS.md) for measured outputs and reproduction.
- [Published CI at 49fe4fd](https://github.com/Parsania80/FluxGet/actions/runs/43423680849) passed Linux and both native media/proxy steps, but [Windows desktop controls failed](https://github.com/Parsania80/FluxGet/actions/runs/43423680849/job/131879620107). Public annotations provide only exit code 1; logs require authentication, so no root cause or fix is claimed. The new CI wrapper preserves failures and emits bounded escaped test details, reusing the release-reporting pattern; success/failure exit codes and escaping were self-checked locally.
- HTTPS Git is unauthenticated on this host. The operator chose a prepared branch for GitHub Desktop. `codex/stage5-media-acceptance` contains the accumulated acceptance work; publishing it triggers the existing native matrix without releasing packages. Windows acceptance remains open until that current-source run passes. No tag, version or published asset changes.

### Public media routing investigation — 2026-11-12

- The failed job used the same loopback proxy host/port as the system's manual HTTP/HTTPS/SOCKS proxy configuration. Actual Chrome/Firefox playback requests were unavailable to the UI tools, so browser routing/session equivalence was not proven.
- Fresh requests reached YouTube via both SOCKS and HTTP CONNECT. The same selected audio URL returned HTTP 206 and 16,384 bytes with curl through either protocol; yt-dlp downloaded all 4,629,623 source bytes in 4.38 seconds. A different audio format returned a redirect; this was not counted as successful transfer.
- The actual Go media backend then analyzed the original video, confirmed the saved selection still matched, downloaded selected audio format 251 and converted to MP3 at 320 kbps through the unchanged saved proxy. ffprobe validation passed: 267.006271 seconds, one MP3 audio stream, 10,682,925 output bytes, 14.15 seconds for download/conversion. Temporary diagnostic media and signed metadata were removed; the user's failed queue job and proxy settings were unchanged.
- The earlier 15/45-second timeouts are no longer reproducing; no definitive underlying cause was established. Both previously failing and currently selected CDN hosts now respond. Do not label the proxy generally broken or claim a routing fix. Public video/merge, native Windows and broader recovery acceptance remain open.

### Packaging, maintenance and release acceptance

- Plan application-managed, pinned standalone yt-dlp and Deno artifacts with matching local EJS. On Debian/Ubuntu declare FFmpeg/ffprobe package requirements; on Windows include tested executables. Verify Linux glibc/architecture and Windows runtime compatibility against the existing release baselines. Users should not need pip, a terminal or manual tool downloads for normal installation.
- Record artifact provenance, hashes, exact versions, component licenses and notices in the build manifest; inspect the selected FFmpeg build configuration and redistribution obligations before shipping. FFmpeg's license depends on its enabled components; use its [official licensing reference](https://ffmpeg.org/legal.html), not an assumed blanket license.
- Ship dependency changes through tested FluxGet releases initially. YouTube can change independently; surface an actionable unsupported-extractor/update error and provide a maintenance-release path. Do not run the extractor's self-updater or fetch executable components silently. A future in-app tool updater needs its own verified-download/rollback design.
- Deterministic CI uses fake tool processes, captured metadata and generated local media fixtures for selection, fragmented progress, process errors, malformed/oversized output, post-processing and publication. Do not make ordinary CI depend on YouTube availability.
- Native opt-in acceptance uses a small, documented public sample set with permission to download: single video, Shorts, finite playlist, mixed qualities, MP4/WebM/MKV where available, native audio and MP3 conversion. Compare inspected streams/duration and playback, not just subprocess exit codes; record tool versions and unavailable combinations.
- Include unavailable/deleted/private playlist entries, login-required results, rate limits, network loss, destination conflicts, Unicode/long paths, low disk, stalled subprocesses, kill/reopen and orphan cleanup. Preserve completed items and existing direct-download behavior in every failure path.
- Initial scope excludes DRM-protected content, paid/private/login-required videos, browser-cookie import, restriction bypass and live/upcoming recording. Support downloads the user is authorized to save; other extractors stay disabled even if the bundled tool supports them. No claim of universal YouTube availability or guaranteed resume.

## Multi-interface downloading — later

First prove explicit single-interface selection on one OS, then combine independent paths without weakening range/checkpoint validation.

- Select platform, privileges, routing assumptions, usable interfaces/addresses, and native adapter boundaries.
- Verify actual egress; selecting a source IP is insufficient. Isolate connection pools by interface; define DNS, proxies, redirects, and IPv4/IPv6 behavior.
- Assign bounded independent range requests, accommodate different speeds, and retry unfinished work when an interface disappears.
- Report per-interface bytes/speed/failures and test servers that reject changing public IPs.
- Compare against single-interface baselines using independent real uplinks; record unsupported configurations before extending adapters to other OSes.

This uses multiple ordinary connections, not migration of one TCP connection. Wi-Fi and Ethernet behind the same upstream bottleneck do not imply extra bandwidth. Acceptance requires correct egress/output/failover, measured benefit, and a native capability/privilege matrix.

## Verification and release work

Recorded evidence establishes scoped behavior, not a general performance or release guarantee:

| Area | Evidence | Still open |
|---|---|---|
| Engine | Local HTTP tests for sequential/parallel integrity, invalid responses, fallback, checksum, cancellation, recovery, exclusive locks, crashes, ENOSPC, permissions, publication | Broader resource bounds, useful parser fuzzing, extended recovery retry window, native platform/release coverage |
| Desktop | Controller/HTTP integration, batch/deletion safety, migration/settings transactions, units, live speed, rendered views | Detailed interactive acceptance, accessibility, packaging/signing/distribution |
| Automatic 1–3 | Fake-clock policy and real HTTP trials; HTTP/2 retirement, exact suffix offsets, rollback/draining, Manual restoration, limits/cooldowns, runtime-only state | Representative native performance and fairness comparisons |
| Automatic 4 | Hysteresis/missing/stale fixtures; quota/event parsing; native 0.50-CPU probe and bounded memory reads; real separate-filesystem operations; buffer exhaustion and HTTP integrity | Full low-memory stress and representative HDD/SSD/checkpoint benchmarks |
| Builds and CI | Local CLI/desktop race suites, vet, Linux builds, Windows/macOS CLI cross-builds; hosted CI evidence recorded historically | Current release-target native evidence and final packaged artifacts |

The stage-4 full CLI/desktop race suites passed, alongside vet/builds/cross-builds and rendered inspection. Its initial memory.high stress probe stalled about 165 seconds and exposed lost event evidence across sampling gaps; the fix has regressions, and the replacement native check is bounded/read-only. It does not substitute for low-memory stress acceptance. Earlier transient crash-test behavior and verification details remain in the session log.

Recorded 2026-11-05 measurements include loopback workloads, ext4/tmpfs, throttled servers, cancellation, and an independently compared 30.9 MB internet download. See BENCHMARKS for conditions; these do not establish publisher authenticity, real-network acceleration, or total memory bounds. Historical local release candidates are not the current published release.

### Remaining engine acceptance (carried into v1)

- [ ] Reconcile requirement evidence with current behavior; finish explicit lifecycle/error/diagnostic contracts where incomplete.
- [ ] Complete native packaged smoke tests, including pause/recovery/cancel/output and the supported filesystem/environment matrix.
- [ ] Review resource leaks/bounds, fault scenarios, and useful fuzz coverage. ENOSPC tests may skip without namespaces; permission tests may skip as root.
- [ ] Demonstrate interruption/recovery and record toolchain, dependencies, benchmark conditions, and limitations.
- [ ] Package current artifacts with checksums/reproducible build instructions; review license/dependency obligations and contribution guidance before publication.
- [ ] Verify native Windows/macOS behavior before claiming those targets. Cross-builds and Linux/WSL tests are insufficient.

### Development commands

Run in the repository root with the desktop build dependencies available. Separate modules need separate checks:

```bash
gofmt -l .
go test -race ./... && go vet ./...
(cd desktop && go test -race ./... && go vet ./...)
mkdir -p dist
go build -o dist/fluxget .
(cd desktop && go build -o ../dist/fluxget-gui .)
GOOS=windows GOARCH=amd64 go build -o dist/fluxget.exe .
GOOS=darwin GOARCH=amd64 go build -o dist/fluxget-darwin .
```

The race detector needs a supported platform and C compiler; desktop builds need Fyne graphics development dependencies. Format changed source files rather than rewriting unrelated files. CI covers Linux formatting/vet/race tests, desktop tests/build, and CLI cross-builds. Tests use isolated local fixtures; never test deletion or fault injection against user downloads. VS Code debug configuration is in `.vscode/launch.json`; native Delve acceptance remains separate.

### CLI reference

| Option | Meaning |
|---|---|
| `-url URL` | New public HTTP/HTTPS download; safe server/URL filename and Downloads folder by default |
| `-out FILE` | Explicit output path; cannot combine with `-dir` or `-name`; existing files refused |
| `-dir DIR`, `-name NAME` | Override output folder/name |
| `-sha256 HEX` | Optional trusted expected checksum for standalone download |
| `-resume -out FILE` | Resume/reconcile a saved job; no new URL |
| `-list` | Read-only saved-job discovery; completed queue history is separate |
| `-queue` | Persistent Linux/Windows queue; explicit `start` begins eligible work |
| `-max-active N` | Queue file ceiling, 1–64, default 5 |
| `-connections N` | Worker ceiling, 1–16, default 4 |
| `-speed-limit N` | KiB/s, zero unlimited; shared across queue jobs |
| `-h` | Full command help |

Queue commands include `add URL OUTPUT`, `import OUTPUT`, `list`, `inspect ID`, `start`, `pause ID`, `resume ID`, `cancel ID`, `retry ID`, `restart ID`, `discard ID`, `remove ID`, and `quit`. Import restores paused; restart/discard perform explicit owned-partial cleanup; CLI remove keeps files and requires inactivity. Saved-job listings show checkpoint snapshots, not live byte guarantees; resume revalidates them. Structured desktop operations provide additional batch/deletion/configuration behavior.

### Local preview record

On 2026-11-10, refreshed `~/.local/opt/fluxget` with the verified current engine/desktop pair (including stage 4 and the uncommitted browser preview), three companion native executables, and generated extension folders. Backup: `~/.local/state/fluxget/backups/20261110-131848-browser-integration`. Normal close/reopen preserved saved checkpoint/settings hashes; the existing completed job stayed inactive. Native-host registrations were installed for Chrome and Firefox; Opera is absent. A framed request through the installed Chrome host reached the running desktop and returned disabled/ask, confirming the default opt-in state. Installed hashes match the tested builds. No extension was loaded into the operator's normal profiles and no download was started. The real queue remains schema 2 until its first browser handoff; that transaction backs up and upgrades it to schema 3. Refreshed the installed pair/hosts/extensions again for approved query-string/CDN support at 14:51; backup `~/.local/state/fluxget/backups/20261110-145136-browser-integration`. Installed hash checks and a synthetic query-bearing request reaching desktop policy validation passed; settings/checkpoints and existing downloads stayed unchanged.

## References

- HTTP validation: [ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-14), [conditional ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.5), [partial responses](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.7), [429](https://www.rfc-editor.org/rfc/rfc6585.html#section-4), [HTTP/2 streams](https://www.rfc-editor.org/rfc/rfc9113.html#section-5.1.2).
- Linux resource observations: [cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [PSI](https://docs.kernel.org/accounting/psi.html). FluxGet thresholds are application hypotheses, not kernel-prescribed values.
- Browser spike: [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging), [Chrome downloads](https://developer.chrome.com/docs/extensions/reference/api/downloads), [Firefox native messaging](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging), [Firefox download events](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/downloads/onCreated), [Firefox backgrounds](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/manifest.json/background), [Opera extensions](https://help.opera.com/en/extensions/). Verify installed versions/packages rather than inferring compatibility.
- Development: [Fyne setup](https://docs.fyne.io/started/quick/), [Go build constraints](https://pkg.go.dev/cmd/go#hdr-Build_constraints), [cgo](https://pkg.go.dev/cmd/cgo), [dialer](https://pkg.go.dev/net#Dialer), [file writes](https://pkg.go.dev/os#File.WriteAt), [rename](https://pkg.go.dev/os#Rename), [Go debugger setup](https://github.com/golang/vscode-go/wiki/debugging).
