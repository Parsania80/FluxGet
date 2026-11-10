# FluxGet — Project Roadmap

FluxGet is a Go download manager inspired by IDM. The priorities are reliable transfers, understandable desktop controls, optional browser integration, and later experimental multi-interface downloading. It is also a learning and portfolio project for Go, concurrency, networking, persistence, and recovery.

**Status — 2026-11-10:** Linux CLI and Fyne desktop prototypes are implemented. Automatic stages 1–4 are committed in `956a9e8`; stage 5 acceptance is next. Browser integration and multi-interface downloading are not implemented. The v0.1 engine release and detailed interactive desktop acceptance remain open.

This file describes current decisions and remaining work. [README.md](README.md) is the simple user guide, [BENCHMARKS.md](BENCHMARKS.md) records measured workloads, and [SESSION-LOG.md](SESSION-LOG.md) preserves development history. Implementation does not by itself mean release acceptance has passed.

## Roadmap

Keep these priorities while continuing engine release verification alongside product development.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1 | CLI and desktop prototype baseline | ✅ Done | Sequential/parallel downloads, persistent queue, recovery, progress, and live limits |
| 2 | Unified Downloads experience | 🔄 Current | Implemented and preview approved; detailed interactive acceptance remains |
| 3 | Automatic download management | 🔄 Current | Stages 1–4 implemented; stage 5 compares Automatic with Manual |
| 4 | Browser capability spike and bridge contract | ⬜ Todo | Verify Chrome, Firefox, and Opera behavior and package constraints |
| 5 | Shared intake, native host, and manual Send to FluxGet | ⬜ Todo | One owner, private IPC, versioned protocol, idempotent requests |
| 6 | Chrome capture and integration settings | ⬜ Todo | Public downloads, per-browser controls, safe handoff, fallback |
| 7 | Firefox and Opera rollout | ⬜ Todo | Separate manifests, registration, and native compatibility evidence |
| 8 | Browser packaging and acceptance | ⬜ Todo | Stable extension IDs, distribution, repair/uninstall, privacy, crash tests |
| 9 | Remaining v0.1 engine release checks | ⬜ Todo | Resource bounds, packaged artifacts, native environments, evidence review |
| 10 | Multi-interface experiment | ⬜ Todo | Prove routing, correct output, failover, and benefit on independent uplinks |
| 11 | Authenticated browser downloads | ⬜ Todo | Separate consent, credential, request-replay, and recovery design |

### ➡️ Current: #3 — Automatic download management

### ⏭️ Next: #3 stage 5 — Full adaptive acceptance

**Next concrete work:** Run repeatable Automatic-versus-Manual comparisons across useful and unhelpful parallelism, changing bandwidth, user limits, origin restrictions, CPU quotas, low memory, and slow destination/checkpoint storage. Measure throughput, requests, variability, resource cost, fairness, and recovery; complete native desktop review before claiming the default experience accepted.

## Product scope and release boundaries

The first target is **Linux/amd64**. Ubuntu 26.04.1 with Go 1.26.8 has local runtime evidence. Windows/macOS CLI cross-builds pass, but native behavior is not verified; ARM64 is also pending. Keep shared code portable and OS behavior in small adapters. WSL tests count as Linux evidence.

| Milestone | Scope | Release gate |
|---|---|---|
| v0.1: Reliable Download Engine | CLI, HTTP/HTTPS, queue, parallel ranges, conditional resume, recovery, optional SHA-256, resource limits | Reproducible correctness/failure tests and packaged native smoke tests |
| Desktop, parallel track | One Downloads list, shared settings, Automatic/Manual, migration, batch actions | Preserved user data and interactive acceptance |
| Browser integration, parallel track | Optional Chrome/Firefox/Opera extensions and local native host | Tested handoff, browser fallback, per-browser consent, installation matrix |
| v0.2: Multi-interface experiment | Explicit network paths on one selected OS | Correct egress, output, and failover on independent real uplinks |
| v0.3: Broader interface support | Remaining OS adapters | Native capability and privilege matrix |
| Later | Authenticated browser downloads and adaptive interface scheduling | Separate security, replay, and routing designs |

Desktop and browser work precede multi-interface development without expanding the v0.1 engine gate. There are no fixed release dates or claims of complete IDM parity, guaranteed acceleration, or uninterrupted browsing.

Outside current scope: website login automation, cookies/credential storage, secret-bearing links, media extraction, streaming assembly, torrents, FTP/SFTP, cloud sync, remote administration, and automatic updates. No hosted service, database server, account system, or public control API is required. Scheduled queues, mirrors, notifications, metered-network controls, and a background service remain optional future work with a demonstrated need.

## Tech Stack Lock

| Area | Selected technology | Boundary |
|---|---|---|
| Engine and CLI | Go 1.26 baseline; standard library | Root module `github.com/Parsania80/FluxGet` |
| Desktop | Go 1.26.8; Fyne v2.8.2 and pinned dependencies | Separate `desktop/go.mod` and `desktop/go.sum`; no replacement UI framework |
| Persistence | Versioned local JSON | Queue schema 2, current checkpoint version 4; no database needed |
| Browser extensions, planned | Plain JavaScript, HTML, CSS, JSON; official WebExtensions APIs | Manifest V3 where supported, browser-specific background/manifest adapters; no initial framework |
| Native bridge, planned | Go standard library, native messaging, private local IPC | Browser-specific registration and explicit extension-ID allowlists |

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
| `queue*.go`, `cleanup*.go` | Persistent queue, desktop protocol, transitions, deletion, snapshots |
| `adaptive.go`, `automatic_queue.go`, `request_budget.go`, `worker_control.go` | Pure policy, live coordination, request leases, safe worker targets |
| `measurements.go`, `hardware*.go`, `resource_guard.go`, `storage_observations*.go`, `transfer_buffers.go` | Runtime observations and resource controls |
| `desktop/` | Fyne Downloads/Settings, companion client, migration, units, history, batch controls |
| `desktop/assets/`, `packaging/linux/` | Embedded app icon and Linux launcher template |
| `.github/workflows/ci.yml` | Formatting, tests, vet, desktop/Linux builds, CLI cross-builds |

The engine currently lives in the root `main` package. A future `cmd/` and `internal/` split is optional, not implemented architecture. Proposed browser source placement must be finalized during bridge design.

## Current behavior and correctness contracts

### Transfers and output

- Accept public direct HTTP/HTTPS URLs. Verify TLS, allow up to nine redirects, and reject HTTPS-to-HTTP downgrade. Treat headers and filenames as untrusted; explicit names take precedence over safe server names and URL fallbacks.
- Stream to an exclusively created `.part` file. Unknown-length responses have byte progress without invented percentages. Reject unexpected statuses, truncated bodies, invalid ranges, changed representations, and unsupported range transformations.
- Eligible Linux files of at least 1 MiB can use parallel ranges after a strong-ETag/range probe. Validate each actual response's range, total, validator, and body length; never write a full response blindly at a range offset. Unsupported servers use a fresh sequential stream.
- Publish only after required synchronization, close, and verification. Final publication uses a same-directory hard link and does not overwrite an existing destination. Failure keeps the partial file; hard-link support is required.
- Standalone downloads accept an optional trusted expected SHA-256 checksum. Streaming/prefix digests and server validators do not replace that trust check. Queue checksum input remains unimplemented.
- HTTP 408, 429, 500, 502, 503, 504 and selected transient network failures may retry, subject to safe resume. Permanent validation/storage failures follow their error path rather than being tuned around.

### Persistence and recovery

- Queue schema 2 reads version-one queues. Checkpoint v4 records verified offsets/hashes within unfinished parts; versions 1–3 remain readable. Older executables cannot read all upgraded formats. Keep formats versioned and validate paths, identity, ranges, sizes, and partial availability before reuse.
- Synchronize partial data before publishing metadata: write temporary JSON in the metadata directory, sync and close it, then replace the record. Failed updates must not claim unsaved progress. Locks prevent duplicate FluxGet writers; unrelated programs can still race filesystem changes.
- Parallel recovery requires a strong validator and validated ranges. Sequential recovery can instead compare the saved prefix with one fresh known-length response before appending; this redownloads the prefix and does not authenticate the source.
- Prefix checkpoints occur after roughly 1 MiB or two seconds while data arrives, plus clean interruption. A crash may lose work since the last safe checkpoint. Unrecorded prefixes in older checkpoints cannot be recovered retroactively.
- Explicit `-resume` can verify/reconcile interrupted final publication using its completed-file digest and identity. Older records without publication state require manual inspection. Startup-wide reconciliation is not complete.
- A crash before the first record can leave an undiscovered partial file. Process-kill tests do not establish power-loss durability; that guarantee remains out of scope until separately specified and tested.
- Queue/history metadata contains source URLs. Do not store authentication secrets or use credential-bearing links; future authenticated workflows need a separate design. Never execute downloaded content or shell commands derived from it.

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

Automatic partial cleanup requires the recorded Linux inode identity, valid saved hashes where applicable, and a random user extended-attribute ownership marker. Legacy/unmarked data requires manual inspection. Completed deletion checks the confirmed path/device/inode/size/mtime and rejects symlinks and conflicts. Replacement downloads a separate copy and verifies the original before replacement; it is not permission to overwrite an arbitrary file.

Persisted queue states include queued, running, paused, failed, completed, canceled, and resetting. Runtime snapshots distinguish connecting, downloading, verification, retry/budget waits, and retirement. Future state-model changes must document mutable-field ownership and migration.

## Desktop experience

Implemented: Downloads/Settings tabs; one list with All, Downloading, Waiting, Paused, Completed, and Failed filters; automatic scheduler startup; Add download with now/later; contextual and batch actions; safe duplicate choices; folder/file actions; and default/shared/individual live speed limits. Canceled entries remain in All/history.

Rows show filename, current aggregate speed, progress, useful remaining time, and actual part states. Sequential fallback has one part. Received counters are runtime observations, not proof of durable saved bytes. A displayed 0% part can be below 1%; Parts details show amounts and ranges. Fixed saved parts remain distinct from active connections.

Display defaults to decimal MB/MB/s with KB/MB/GB options. The engine stores whole KiB/s limits; short editor values round-trip to the same cap instead of exposing digits such as 0.999424. Remove limit disables the individual override; default and shared limits still apply. Recent speed excludes saved bytes, becomes zero after a stalled progress interval, and is a dash for inactive rows.

Legacy queue/history/settings migration backs up originals, maps stable queue IDs and speed keys, and imports eligible checkpoints paused. Unimportable entries remain visible. Existing completed history and partial files are preserved; new browser intake will reuse the same scheduler.

Still required: native interactive review of add/pause/resume/open-folder, migration, competing ownership, duplicate names, shared actions, units/limits, long filenames, unknown totals, keyboard navigation, accessibility, packaging, signing, and distribution. The operator approved the preview on 2026-11-09; this does not close every acceptance item.

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

## Browser integration — planned

**Goal:** Capture supported browser downloads into the same Downloads list when enabled, with independent Chrome/Firefox/Opera controls and a reliable browser fallback. Begin on Linux/amd64 after the selected Automatic foundations and desktop acceptance.

**Selected architecture:** WebExtension -> browser native messaging -> small Go host -> private local IPC -> single desktop/scheduler owner. Linux IPC is planned as a user-owned Unix socket with restrictive permissions and peer checks; other OS adapters require verification. The extension owns browser download operations; the desktop owns jobs and integration policy. Native hosts must not start competing queue owners.

Native messaging avoids a public listener and gives explicit extension identities, but needs per-browser/package registration. Localhost HTTP is an alternative only if a documented environment makes native messaging unsuitable; it adds port/authentication/origin/lifecycle work. A custom URL scheme can support explicit links but cannot alone provide general capture.

### Planned settings

| Control | Behavior |
|---|---|
| Master integration switch | Off until opt-in; disabling stops new extension intake, not existing FluxGet jobs |
| Chrome / Firefox / Opera | Independent switches and per-profile consent/readiness; selecting one never enables others |
| Status and setup | Not installed, Extension missing, Disconnected, Ready, Disabled, Unsupported; installation/test/repair actions |
| Capture mode | Ask first by default; optional automatic Download now after setup; Manual only for context-menu sending |
| Extension popup | Effective policy, settings shortcut, Use browser once, temporary capture pause; local disable wins |
| Exceptions | Per-site/file-type rules; exclusions win; distinguish initiating site from download host |
| Private browsing | Off; separate permission/consent and tested privacy behavior required before support |

Effective readiness requires master, browser, profile consent, and extension readiness. The host also validates policy; stale/unknown/disconnected state leaves new downloads in the browser. Define changes during pending handoff explicitly. Manual sending works when only automatic capture mode is off; master/browser disable blocks all extension intake.

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

Leave POST/form bodies, blob/data URLs, generated files, authenticated/cookie-dependent downloads, expiring or single-use secret links, protected downloads, and streaming media in the browser. Initial permissions cover only demonstrated needs such as downloads, native messaging, storage, and context menus. No cookies, arbitrary headers, broad request interception, or browsing-history transfer; additional site permissions require justification and consent. Redact query strings and sensitive URL components; a public-looking URL is not proof of no secret.

Do not bypass browser malware/security blocks, enterprise policy, or certificate failures. If a security decision cannot be preserved, leave that download class in the browser. FluxGet must not imply it inherits browser scanning guarantees. Later authenticated support must design consent, scoped headers/cookies, redirect-origin restrictions, signed URLs, secret storage/redaction/expiry, and recovery first.

Browser acceptance remains open:

- [ ] Matching output without duplicate FluxGet jobs or completed browser copies.
- [ ] Independent master/browser/profile/extension disable, exclusions, and temporary bypass across restarts; existing FluxGet jobs continue.
- [ ] Missing host/extension, incompatible protocol, locked queue, startup failure, rejection, and full disk leave a browser download or explicit recoverable pending job.
- [ ] Instant downloads, simultaneous/duplicate events, disable during handoff, extension suspension, browser exit, and every handoff crash boundary.
- [ ] Unsupported, private-without-consent, unsafe, and security-blocked inputs follow the fallback policy.
- [ ] Manual install, approval, connection test, capture, repair, update, and uninstall on each claimed browser/package combination.

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

### Remaining v0.1 release gate

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
| `-queue` | Persistent Linux queue; explicit `start` begins eligible work |
| `-max-active N` | Queue file ceiling, 1–64, default 5 |
| `-connections N` | Worker ceiling, 1–16, default 4 |
| `-speed-limit N` | KiB/s, zero unlimited; shared across queue jobs |
| `-h` | Full command help |

Queue commands include `add URL OUTPUT`, `import OUTPUT`, `list`, `inspect ID`, `start`, `pause ID`, `resume ID`, `cancel ID`, `retry ID`, `restart ID`, `discard ID`, `remove ID`, and `quit`. Import restores paused; restart/discard perform explicit owned-partial cleanup; CLI remove keeps files and requires inactivity. Saved-job listings show checkpoint snapshots, not live byte guarantees; resume revalidates them. Structured desktop operations provide additional batch/deletion/configuration behavior.

### Local preview record

At the last installation check on 2026-11-10, `~/.local/opt/fluxget` contained the paired stage-3 executables corresponding to `a641a49`, opened through `fluxget-preview`. Backup: `~/.local/state/fluxget/backups/20261110-091846-live-speed-pacing`. The stage-4 refresh stopped at preflight because a real download was active, before changing any runtime state. Stage-4 builds were prepared in `/tmp`, which is temporary; rebuild from the committed source for a later paired refresh. Do not describe stage 4 as installed from the source commit alone.

## References

- HTTP validation: [ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-14), [conditional ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.5), [partial responses](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.7), [429](https://www.rfc-editor.org/rfc/rfc6585.html#section-4), [HTTP/2 streams](https://www.rfc-editor.org/rfc/rfc9113.html#section-5.1.2).
- Linux resource observations: [cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [PSI](https://docs.kernel.org/accounting/psi.html). FluxGet thresholds are application hypotheses, not kernel-prescribed values.
- Browser spike: [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging), [Chrome downloads](https://developer.chrome.com/docs/extensions/reference/api/downloads), [Firefox native messaging](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging), [Firefox download events](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/downloads/onCreated), [Firefox backgrounds](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/manifest.json/background), [Opera extensions](https://help.opera.com/en/extensions/). Verify installed versions/packages rather than inferring compatibility.
- Development: [Fyne setup](https://docs.fyne.io/started/quick/), [Go build constraints](https://pkg.go.dev/cmd/go#hdr-Build_constraints), [cgo](https://pkg.go.dev/cmd/cgo), [dialer](https://pkg.go.dev/net#Dialer), [file writes](https://pkg.go.dev/os#File.WriteAt), [rename](https://pkg.go.dev/os#Rename), [Go debugger setup](https://github.com/golang/vscode-go/wiki/debugging).
