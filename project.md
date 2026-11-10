# FluxGet - Project Roadmap

A Go download manager inspired by IDM, with reliable transfers first and multi-interface downloading as a later distinguishing feature.

**Status (2026-11-09):** Linux CLI and Fyne desktop prototypes implement sequential and parallel transfers, persistence, recovery, queue management, progress, and live speed limits. The unified Downloads redesign is implemented in the working tree and installed as the local preview: one list, automatic scheduling, Download now/later, shared settings, and legacy history migration. CLI/desktop race tests, vet, Linux builds, CLI cross-builds, and rendered UI checks passed. Interactive operator acceptance and remaining v0.1 release checks are open. Optional Chrome, Firefox, and Opera integration follows the desktop acceptance gate; browser integration is planned, not implemented.

**Project purpose:** Learn Go through implementation and publish a GitHub project that demonstrates backend and systems engineering: concurrency, networking, persistence, recovery, testing, and clear architecture.

**How to use this roadmap:** Follow dependencies and the current priorities in section 18. Engine phases retain their reliability gates; desktop and browser work can proceed alongside release verification. Check a task only when code, tests, or documentation demonstrate its completion. Resolve open decisions before implementing the affected feature. Update this file with small, reviewable edits as the project develops. Older unchecked engine tasks require an evidence review before marking them complete; an unchecked item does not necessarily mean no implementation exists.

**Learning workflow:** Use VibeWise Learn while developing. The learner proposes and reasons through meaningful design choices; the assistant explains concepts, gives feedback, and writes the agreed implementation. Local learning notes live in .vibe-wise/. Roadmap proposals are not confirmed architecture decisions.

## 1. Product definition

Build a download manager that accepts direct HTTP/HTTPS file URLs, downloads reliably, and preserves recoverable progress after interruption.
The long-term product goal is to approach IDM's practical download-management capabilities as closely as feasible, with Linux support and a portable codebase. Feature selection and behavior should be checked against real requirements and platform constraints; the release boundaries below remain in effect.

The long-term target is Windows, macOS, and Linux. Development may move between Linux and Windows using one shared Go codebase. Platform-specific features belong in small adapters.

The first release is **v0.1: Reliable Download Engine**, targeting Linux/amd64 first. Desktop development now proceeds alongside the CLI. Keep shared behavior portable so ARM64, Windows, and macOS can be tested and supported later.

**Product direction (2026-11-09):** Present one understandable Downloads list, with scheduling handled automatically. Let users opt into sending supported browser downloads to FluxGet and enable or disable Chrome, Firefox, and Opera independently. Aim for the familiar IDM interaction on supported downloads; do not promise identical behavior on every website or download type.

### First demonstration

1. Add a direct file URL and choose a destination.
2. Watch progress while the engine downloads the file.
3. Pause and resume a supported download.
4. Interrupt the application and reopen it.
5. Recover saved work and finish downloading.
6. Verify that the result matches the source.
7. Repeat against a server that does not support parallel ranges and demonstrate safe fallback.

The application is local software. A hosted server, user accounts, PostgreSQL cluster, and public control API are not requirements for v0.1.

### Current implementation

- The learner approved the first four files: go.mod, main.go, download.go, and download_test.go.
- The project is FluxGet and the Go module is github.com/Parsania80/FluxGet, matching the GitHub repository. The Go baseline is 1.26; the operator has verified Go 1.26.8 on linux/amd64.
- Build the CLI as fluxget with go build -o fluxget . (use fluxget.exe on Windows). New standalone downloads default to the Linux Downloads directory and a safe server-supplied filename or URL fallback; users can override the directory, name, or full output path.
- The CLI accepts -url and -out plus optional -sha256. It streams a sequential response or validated parallel ranges to an exclusively created destination.part file, reports progress, supports Ctrl+C cancellation, rejects invalid responses, and preserves partial bytes on transfer failure or checksum mismatch.
- Successful output is published through a same-directory hard link, then the partial name is removed. Existing destinations and partial files are refused. Filesystems without hard-link support fail safely with the partial file retained; no portable rename-overwrite guarantee is assumed.
- Automated tests cover binary and empty files, unknown length, redirects and TLS, progress, checksums, HTTP errors, truncated responses, partial writes, disk-full and permission failures, checkpoint update failure and recovery, existing files, destination-creation races, and cancellation. They pass on Linux with Go 1.26.8.
- Eligible Linux sequential transfers save versioned JSON checkpoints during transfer and on interruption. The `-resume` command locks the job and partial file, verifies the saved prefix and remote representation, and validates the actual resumed response before writing.
- The `-list` command discovers saved Linux jobs without changing them. Version-two records persist sequential states; version-four records track saved offsets and SHA-256 prefix hashes within independent parallel chunks. Versions one through three remain readable; older parallel checkpoints upgrade on resumed work without trusting unrecorded prefixes. Live locks distinguish active work from interrupted records. Completed standalone jobs are removed from the saved-job listing; completed queue jobs remain in the queue history.
- An interactive Linux queue persists queued, running, paused, failed, completed, and canceled jobs in an atomically replaced JSON file under an exclusive lock. It schedules up to five active downloads by default (configurable from 1 to 64), imports existing checkpoints, and restores previously active jobs paused. The queue loop owns lifecycle changes; workers report results to it.
- On Linux, new downloads of at least 1 MiB use up to four range workers by default (configurable from 1 to 16 per CLI or queue session) after a successful strong-ETag range probe. Each response is validated before offset writes; unsupported servers fall back to sequential. Each worker maintains a streaming SHA-256 digest and sends bounded prefix checkpoints to the transfer owner after about 1 MiB or two seconds when receiving data, plus a final prefix on interruption. The owner synchronizes data and atomically publishes metadata before acknowledging a checkpoint. Recovery validates each saved prefix and the remote representation, then requests each remaining suffix with the configured connection limit. Only a range with validated response completion is marked complete; final publication still verifies the whole file.
- Queue workers send bounded, nonblocking live progress updates to the queue loop; `inspect ID` reports local checkpoint status and recovery guidance without contacting the server. Progress updates can be dropped when the display is busy.
- Desktop downloads expose actual per-range counters and states through runtime-only snapshots, with independent part progress bars and a live details dialog. Sequential fallback shows one part. Reopened transfers restore the real checkpoint layout; saved ranges retain their boundaries independently of the current connection limit. Runtime received counts do not replace verified recovery checkpoints.
- Queue `restart ID` and `discard ID` explicitly remove verified partials under job ownership; cleanup intent is persisted before deletion so the same command can finish after interruption. New partials record a Linux inode identity and random user extended-attribute marker; older or unmarked files are not deleted automatically. Expected checksums survive fresh restart.
- Standalone and queue downloads use connect, TLS, response-header, and body-read inactivity timeouts. Selected transient HTTP and network failures get up to three cancellable retries; a valid Retry-After up to one minute is honored, and a longer value stops automatic retries. Partial data is retried only through validated resume.
- Linux parallel storage tests exercise real data and checkpoint ENOSPC and permission errors during fresh transfers and recovery. They check saved chunks and retry successfully after restoring space or permissions, including through the queue.
- Queue integration tests kill and restart the queue twice while chunks are saved out of order, inject transient probe and range failures, and verify saved chunks, limits, explicit restart, and backoff cancellation.
- Linux process-kill tests cover checkpoint data write, sync and publication, plus the points before and after final linking and partial-file removal. A completed-publication checkpoint allows explicit `-resume` to verify and reconcile these final-publication states without overwriting conflicting files.
- Resume uses validated ranges with a strong ETag, or verifies the saved prefix again against a single known-length response when no strong validator is available. The latter rereads the prefix and does not authenticate content without a trusted checksum. Recovery-window retries and additional platform verification remain future steps.
- README.md documents the actual prototype, usage, verification commands, and limitations. Local HEAD and the remote main branch matched commit `12635b4` when checked on 2026-11-09. The current .gitignore covers generated binaries and test downloads. Release publication remains separate from source publication.
- .vscode/launch.json adds an optional Go/Delve launch profile that prompts for a URL and destination. README.md includes breakpoint and stepping instructions. Debugger binaries and the default debug download are ignored; a live debugger session remains unverified.

## 2. Terms used in this document

| Term | Simple meaning |
| --- | --- |
| Job | One requested file download, including its settings and saved state. |
| Chunk | A defined byte range within a file. |
| Worker | A bounded unit of concurrent work fetching a chunk or stream. |
| Scheduler | The component deciding which jobs and chunks may run. |
| Validator | Server metadata used to check whether the selected file representation is unchanged. |
| Checkpoint | Saved metadata describing work that is safe to reuse after restart. |
| Partial file | Incomplete output kept separate from the final destination filename. |
| Interface | A network adapter, such as Wi-Fi, Ethernet, or phone tethering. |
| Failover | Retrying unfinished work through another usable connection. |

A downloaded byte count is not proof of a correct file. A server validator is not a cryptographic integrity check. A trusted expected checksum, when supplied, provides a separate verification step.

## 3. Release boundaries

| Milestone | Included | Completion evidence |
| --- | --- | --- |
| v0.1 | Reliable engine, CLI, queue, parallel ranges, conditional resume, recovery, checksums, and resource limits | Automated failure tests and native release smoke tests on the supported OS/architecture combinations |
| v0.2 | Experimental multi-interface support on one selected OS | Measured transfers and failover across real independent network paths |
| v0.3 | Multi-interface support extended to the other target operating systems | A documented capability matrix and real platform tests |
| Parallel track: desktop usability | One Downloads view with automatic scheduling and consistent controls | Migration preserves existing jobs, history, checkpoints, and speed settings; interactive Linux acceptance passes |
| Parallel track: browser integration | Optional Chrome, Firefox, and Opera extensions plus a local native-messaging host | Manual handoff first, then tested automatic capture of supported public downloads with per-browser settings and browser fallback |
| Later | Authenticated browser downloads and adaptive network scheduling | Separate designs for credentials, request replay, and routing; no automatic expansion of public-download support |

Release labels express order, not fixed dates. Desktop usability and browser integration are now prioritized before multi-interface development, without expanding the v0.1 engine release requirements. Browser integration receives its own release gate after the unified Downloads foundation is ready.

The first release targets Linux/amd64; ARM64, Windows, and macOS support requires later native testing. Cross-platform download support does not imply cross-platform multi-interface support. Report exactly which capabilities have been verified on each platform.

## 4. v0.1 features

| Feature | Required behavior |
| --- | --- |
| Direct downloads | Accept public HTTP/HTTPS file URLs and handle bounded redirects. |
| Progress | Show downloaded bytes, transfer speed, and state; show percentage and estimated time when meaningful. |
| Parallel ranges | Fetch non-overlapping file ranges with a configurable worker limit when the server and representation allow it. |
| Sequential fallback | Download through one stream when range support or safe representation checks are unavailable. |
| Pause/resume | Stop cleanly and reuse saved work only when the remote representation can be checked safely. |
| Restart recovery | Reload saved jobs and reuse only validated, safely checkpointed work. |
| Queue | Manage several jobs with separate limits on active jobs and workers per job. |
| Retries | Retry selected temporary failures with backoff, a retry budget, and cancellation support. |
| Cancellation | Stop workers promptly and offer explicit retention or removal of partial data. |
| Output safety | Use partial files and prevent accidental replacement of existing output. |
| Optional verification | Compare the completed file with a user-supplied expected SHA-256 checksum. |
| Diagnostics | Explain network, server, permission, storage, and verification failures without exposing secrets. |

The first CLI is one running application managing its queue. It must support adding, inspecting, pausing, resuming, canceling, and retrying jobs without requiring a background daemon or remote API.

### Outside v0.1

- Native desktop GUI, system tray, and browser extensions.
- Automatic interception of browser downloads.
- Simultaneous multi-interface downloading and automatic interface switching.
- Website login automation, browser cookies, and authenticated download sessions.
- Secret-bearing download URLs and credential storage workflows.
- Video extraction, streaming-media assembly, torrents, FTP, and SFTP.
- Automatic application updates, cloud synchronization, and remote administration.
- Calendar scheduling, mirror discovery, and automatic bandwidth optimization.

These are outside the v0.1 engine release. Desktop work is already underway and browser integration is a planned parallel milestone; neither is required to declare the engine release complete. Authenticated capture and other advanced browser cases remain deferred. Do not claim complete IDM feature parity or guaranteed download acceleration.

## 5. Architecture

```text
CLI
 |
 v
Download engine
 |-- Job scheduler and state transitions
 |-- HTTP transfer workers
 |-- Progress events and cancellation
 |-- File output and verification
 |-- Checkpoint persistence
 |
 v
Platform adapters
 |-- Local paths and file behavior
 |-- Future network-interface binding

Remote HTTP/HTTPS servers <---- transfer workers
```

The Fyne desktop app is a separate Go module and starts the CLI as a companion process. Its unified Downloads view connects automatically to one exclusively owned persistent scheduler. New desktop jobs use structured requests; the queue loop owns state changes, filename reservations, progress snapshots, and configuration updates. On desktop startup, restored active/waiting work becomes paused; new Download now jobs schedule automatically. The standalone CLI remains available separately. Desktop settings use a private JSON file read during transfers for per-download, individual, and shared overall speed ceilings.

Queue schema version 2 adds optional filename-selection, output-path, progress, and diagnostic fields while retaining version-one reads. Existing queue/history/settings metadata is backed up before migration. Legacy desktop history maps to stable queue IDs, retaining replacement intents and speed ceilings; unimportable records remain visible. Transfer/checkpoint logic stays in the CLI. A completed replacement retains its transfer destination identity and records its final output path separately. Interactive acceptance remains required before closing the desktop milestone.

**Planned desktop and browser architecture:** Route new desktop and browser jobs into the same persistent scheduler and Downloads view. A browser extension talks over native messaging to a small Go host, which starts or contacts the single desktop/queue owner through a private local IPC channel. On Linux, use a user-owned Unix socket with restrictive permissions and peer-identity checks; other platforms need verified adapters. Native hosts must not start competing queue owners or bypass job locks. The desktop owns job state and integration policy; the extension owns browser-side download operations. No public HTTP listener or hosted service is required. Existing standalone CLI behavior remains available.

```text
Chrome / Firefox / Opera extension
    -> native messaging (browser-managed stdio)
    -> Go host -> private local IPC -> single desktop/scheduler owner
                                         -> existing transfer/checkpoint logic
```

### Tech Stack Lock

| Area | Allowed technology | Boundary |
| --- | --- | --- |
| Engine and CLI | Go 1.26 baseline, Go standard library | Retain existing portable engine code and platform adapters |
| Desktop | Go, pinned Fyne v2 dependencies in desktop/go.mod and desktop/go.sum | No new UI framework for this redesign |
| Persistence | Versioned local JSON, current checkpoint and queue formats | Migrate safely; no database introduced for browser capture |
| Browser extensions (planned) | Plain JavaScript, HTML, CSS, JSON, official WebExtensions APIs | Manifest V3 where supported, browser-specific manifests/background adapters; no framework or third-party runtime initially |
| Native bridge (planned) | Go standard library, native messaging, platform-specific local IPC | Browser-specific host registration and explicit extension-ID allowlists |

New dependencies require a documented need and comparison before adoption. Browser source directories such as `extensions/` and a native-host command location are proposed; finalize their placement during the bridge design without prematurely restructuring the engine.

### Responsibilities

- **CLI:** Accept input, display snapshots, and translate user actions into engine operations.
- **Engine:** Own job state and coordinate downloading without terminal-specific behavior.
- **Scheduler:** Enforce job and worker limits and avoid launching duplicate work.
- **Transfer component:** Perform requests and validate responses before writing their bodies.
- **Storage component:** Save versioned metadata and recover it safely.
- **Output component:** Own partial files, write offsets, verification, and final publication.
- **Platform adapters:** Contain behavior that cannot be expressed portably.

Begin with one process and a small package structure. Introduce interfaces at real boundaries where they enable platform variation or useful failure testing.

### Suggested package layout

This future package layout is illustrative. The CLI name is fluxget and the module is github.com/Parsania80/FluxGet; the current implementation remains in the repository root.

```text
cmd/fluxget/          CLI entry point
internal/cli/         Terminal input and rendering
internal/engine/      Jobs, scheduling, lifecycle, and events
internal/transfer/    HTTP requests, range validation, and retries
internal/storage/     Metadata and checkpoints
internal/output/      Partial files, writes, and verification
internal/platform/    OS-specific helpers when needed
docs/                 Design decisions and user instructions
```

Avoid adding frameworks or services unless a demonstrated requirement needs them.

## 6. Correctness and safety contracts

These contracts are proposed release requirements. Record their detailed implementation before adding parallelism and recovery.

### HTTP and representation handling

- A metadata request is a hint; verify actual download responses.
- Parallel transfer and range resume require a suitable strong validator. Sequential recovery without one verifies the saved prefix against a single fresh response before appending.
- Request identity content encoding for range transfers and reject unsupported transformations.
- Validate status, returned range, total size, validator, and actual byte count before accepting chunk data.
- A full response must never be written blindly into a chunk offset.
- If a remote representation changes, stop affected workers and require a clean restart.
- If identity cannot be established, fall back to a fresh sequential transfer.
- Bound redirects and define behavior for HTTPS-to-HTTP redirects.
- Unknown-size responses use streaming progress without invented percentages.

HTTP defines range requests and conditional retrieval, and permits a server to ignore a range request. These behaviors must be tested rather than inferred from a header alone. See [HTTP ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-14) and [conditional ranges](https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.5).

### Persistence and recovery

- Checkpoint metadata must never claim that unwritten or unsafely persisted bytes are complete.
- Define and test the ordering between data writes, synchronization, and metadata updates.
- Recovery may redownload uncheckpointed work; it must not skip missing bytes.
- Validate metadata versions, chunk boundaries, output identity, and partial-file availability before reuse.
- Refuse conflicting writers to the same job and partial file.
- Restore interrupted jobs as paused after restart, as specified by the lifecycle behavior below.
- Test application crashes. Document power-loss guarantees separately; do not imply them from process-crash tests.

### File output and integrity

- Stream through bounded buffers rather than loading the entire file into memory.
- Restrict server-suggested filenames to safe names within the chosen output directory.
- Do not silently overwrite existing files or delete user files.
- Close workers and file handles before final publication.
- Mark completion only after required writes and optional checksum verification succeed.
- Handle failures between final publication and saving completed state during recovery.
- Test rename and replacement behavior on each OS instead of assuming identical semantics.

Go offers offset-based writes through [File.WriteAt](https://pkg.go.dev/os#File.WriteAt); the engine remains responsible for non-overlapping ownership and complete writes. Review platform differences in [os.Rename](https://pkg.go.dev/os#Rename) when designing final publication.

### Security and resource limits

- Verify HTTPS certificates and server names.
- Keep operator-supplied credentials out of source, configuration examples, metadata, and logs.
- v0.1 handles public links; future credential-bearing links need an explicit secret-handling design.
- Treat remote headers and filenames as untrusted input.
- Define finite worker counts, buffer sizes, timeouts, redirect limits, and retry budgets.
- Never run shell commands based on downloaded content.
- Preserve incomplete files after failure unless the user explicitly chooses removal.

## 7. Phase 0 - Resolve initial design decisions

**Goal:** Make the first implementation small and unambiguous.

- [x] Set the project name to FluxGet and module path to github.com/Parsania80/FluxGet.
- [x] Establish the Go 1.26 baseline; operator toolchain is Go 1.26.8.
- [x] Choose the MIT License; see LICENSE for the license text.
- [x] Select Linux/amd64 as the first release target; consider ARM64 and other operating systems after native testing.
- [x] Use Linux as the primary development OS while keeping shared code portable for Windows and macOS.
- [ ] Finalize CLI interaction, startup recovery, pause, cancel, retry, and overwrite behavior.
- [x] Select versioned JSON job metadata and define checkpoint update ordering; implement and test the design during persistence work.
- [ ] Define the full job lifecycle and the owner of each mutable state field. Version-two checkpoints persist sequential states and version-four checkpoints persist parallel chunks and unfinished-prefix offsets/hashes; the queue loop owns the basic persisted queue states, while additional lifecycle details remain open.
- [ ] Set initial active-job, worker, buffer, timeout, and retry limits; implement and test the chosen concurrency controls and resolve the remaining proposed values in the relevant phases.
- [ ] Define redirect, unsupported-validator, and unsupported-range policies. Reject HTTPS-to-HTTP redirects; the current prototype allows up to nine redirects. Unsupported-validator and unsupported-range details remain open.
- [ ] Record application-crash guarantees and exclusions.
- [ ] Write the first acceptance scenarios before implementing their features.

### Metadata storage options

| Option | Advantages | Costs |
| --- | --- | --- |
| Versioned JSON metadata | Human-readable and a small dependency footprint | Requires careful replacement, locking, validation, and recovery design |
| Embedded SQLite | Transactions and structured queries | Requires schema management and a Go driver choice with packaging implications |
| Another local store | May satisfy a particular demonstrated requirement | Requires justification and a comparison against the simpler options |

Versioned JSON is selected for persistence; Fyne v2 is selected for the separate desktop module. The CLI continues to use the Go standard library.

### Selected metadata and checkpoint design

- Keep one versioned JSON record per job in a FluxGet-managed directory under the operating system's user configuration directory. Store job identity, source URL, destination and partial-file identity, state, representation validator, total size when known, and validated completed ranges. Do not store authentication secrets; credential-bearing workflows are outside v0.1.
- Allow only one active owner of a job and its partial file. Validate the format version, paths, file identity, saved ranges, and remote representation before trusting saved work. Unsupported or corrupt records require attention rather than silent reuse or deletion.
- Before recording completed bytes, stop or coordinate the relevant writers and synchronize the partial file. Write the new JSON to a separate temporary file in the metadata directory, synchronize and close it, then atomically replace the previous record using a mechanism verified on the target platform. If an update fails, report it and keep the last valid checkpoint when possible.
- On restart, recover only complete, validated records and safely saved bytes; ignore incomplete temporary updates. A crash may lose progress since the last checkpoint, which can be downloaded again. Do not claim power-loss durability until file and directory synchronization guarantees are specified and tested.
- The sequential Linux flow saves an initial job record, progress checkpoints approximately every 1 MiB when applicable, and interruption state, validating the local prefix and both the remote probe and the actual resumed range response. Before final publication, it saves a completed-file digest and state; explicit `-resume` verifies and reconciles the file and any same-inode partial name. A crash before the initial record can leave an undiscovered partial file, and a crash before the first progress checkpoint can require a fresh download. Older checkpoints without the completed state cannot be reconciled automatically. Parallel chunk checkpoints are synchronized and atomically published as workers finish; process-kill tests cover their recovery. Non-Linux locking and metadata replacement, startup-wide reconciliation, and power-loss durability remain to be implemented and tested.

**Done when:** Initial decisions are recorded and the sequential transfer implementation has a clear contract.

### Initial resource limits

These are design targets, not guarantees of current implementation. The concurrency controls below are selected; other numeric values are initial proposals to revisit with measurements and user feedback.

| Resource | Initial default | Scope |
| --- | --- | --- |
| Active downloads | 5 | User-configurable simultaneous job limit; additional jobs wait in the queue. Users can choose 10 or another supported value. |
| Workers | 4 per active job | Configurable from 1 to 16 per CLI or queue session. Sequential jobs and legacy sequential resumes use one worker; parallel resumes honor the configured limit. Per-job queue overrides remain planned. |
| Transfer buffer | 32 KiB per worker | Bound per-worker copy buffering; this is not a cap on total process memory. |
| Connection timeout | 10 seconds | Time allowed to establish a network connection. |
| TLS handshake timeout | 10 seconds | Time allowed for each TLS handshake. |
| Response-header timeout | 30 seconds | Wait for response headers after the request is sent. |
| Read inactivity timeout | 60 seconds | Abort a transfer if no response-body bytes arrive for this interval; no fixed limit on the total download duration. |
| Retries | 3 retries after the initial attempt | Retry transient failures only, with cancelable exponential delays of 1, 2, and 4 seconds; permanent failures are not retried. |

The active-job limit, timeout defaults, and bounded retries are implemented. The per-file parallel worker limit is implemented for each CLI or queue session; per-job queue overrides remain future work. Retrying partial work still requires representation validation and the applicable resume safety rules; no retry may silently append bytes from an unverified representation. Retry-After values above one minute stop automatic retries; remaining redirect policy details are separate Phase 0 decisions.

The active-download limit and per-file worker limit are independent: changing one does not change the other. Both accept validated positive values; their implementation must enforce finite safety bounds, with exact bounds and server-specific connection policy to be decided before implementing the controls. Unsupported ranges or unsafe representation identity fall back to a single fresh sequential transfer.

### Pause, resume, cancel, and restart behavior

These are v0.1 design decisions. The Linux queue implements explicit fresh restart and verified partial-file removal for marked files.

| Action | Intended behavior |
| --- | --- |
| Pause | Stop scheduling work for the job, cancel active requests, wait for writers to finish, then save a consistent checkpoint. Show paused only after this succeeds; report checkpoint failures without claiming a safe pause. Preserve the partial file. |
| Resume | Revalidate the remote representation and local partial file before reusing saved bytes. Queue the job subject to concurrency limits. Parallel recovery requires a strong ETag and validated ranges; sequential recovery can instead verify the saved prefix against one fresh known-length response. If neither path is safe, explain that a fresh download is needed. |
| Cancel | Stop requests and wait for writers. Keep the partial file by default; provide an explicit option to remove it. Remove only partial data known to belong to the job, never an existing final destination. A canceled job is not automatically resumed. |
| Restart application | Reload saved jobs without automatically starting transfers. Restore jobs interrupted while active as paused or as needing attention if recovery validation fails; keep completed, failed, and canceled states visible. The user explicitly resumes or starts queued work. |
| Fresh restart of a job | If saved bytes cannot be safely reused, require an explicit fresh-start action. Preserve existing partial data unless the user explicitly chooses its removal, and never overwrite an existing destination. |

After an application crash, recovery may re-download work since the last safe checkpoint but must never treat incomplete bytes as complete. Power-loss durability is not promised until its separate contract and tests are defined. The queue uses checkpoint validation on resume and persists explicit cleanup intent before removing owned partial files.

## 8. Phase 1 - Establish the Go foundation

**Goal:** Create a portable, testable skeleton.

- [ ] Initialize the module and a minimal CLI entry point.
- [ ] Establish the engine, transfer, output, and storage boundaries.
- [ ] Add validated configuration and consistent error types.
- [ ] Use cancellation contexts for jobs and application shutdown.
- [ ] Establish structured diagnostic logging and a separate progress output path.
- [x] Add formatting, static checks, and automated tests in CI. The first hosted GitHub Actions run passed on 2026-11-05 for commit `c972c54`.
- [ ] Create a deterministic local HTTP test server.
- [ ] Document development setup on Linux and Windows.

**Done when:** The application builds on the target platforms, configuration errors are clear, and the initial checks pass.

## 9. Phase 2 - Implement sequential downloads

**Goal:** Complete the smallest useful download before adding concurrency.

- [x] Accept a public URL and a safe output destination.
- [x] Stream the response into a partial file with bounded memory use.
- [x] Handle redirects, TLS errors, unsuccessful statuses, and network cancellation.
- [x] Support known and unknown response lengths.
- [x] Report downloaded bytes, speed, and useful progress.
- [x] Handle partial writes, full disks, permissions, and existing destinations; Linux failure scenarios are tested, with additional platforms and crash points still pending.
- [x] Add optional expected SHA-256 verification.
- [x] Publish the final file only after successful completion and required verification.
- [x] Test empty files, binary files, interrupted bodies, and incorrect declared lengths.

**Done when:** Successful output matches the source exactly and failed transfers cannot appear complete.

## 10. Phase 3 - Add parallel range transfers

**Goal:** Download independent chunks without corrupting the output.

- [x] Probe range behavior and capture the selected representation's identity.
- [x] Define chunk boundaries with no gaps or overlaps, including the last chunk.
- [x] Implement a bounded worker pool and offset-based file writes.
- [x] Validate each response before accepting its bytes.
- [x] Keep concurrency ownership clear and progress aggregation race-free.
- [x] Stop sibling workers when a job becomes invalid or is canceled.
- [x] Implement safe fallback when a server ignores or rejects range requests.
- [x] Handle changed validators, truncated chunks, inconsistent sizes, and invalid ranges.
- [x] Run relevant concurrent tests with Go's race detector.

**Done when:** Sequential and parallel downloads produce identical output, and unsupported servers fall back without mixed or duplicated content.

## 11. Phase 4 - Add persistence, pause, and restart recovery

**Goal:** Preserve useful work across interruption.

- [ ] Implement the Phase 0 metadata format and versioning.
- [ ] Persist job settings, representation identity, chunk completion, and partial-file identity.
- [ ] Implement checkpoint ordering under the documented durability contract.
- [ ] Pause by canceling active work, waiting for writers, and saving a consistent checkpoint.
- [ ] Revalidate the remote representation before resuming.
- [ ] Detect missing, truncated, replaced, or incompatible partial files and metadata.
- [ ] Reject simultaneous ownership of the same saved job.
- [ ] Recover safely when termination occurs during writing, checkpointing, verification, or final publication.
- [x] Define explicit keep/remove behavior for canceled partial downloads.

**Done when:** Process interruption preserves a recoverable checkpoint, and no recovery path silently skips incomplete data.

## 12. Phase 5 - Complete queue management and retries

**Goal:** Make the engine usable for more than one file.

- [x] Implement basic queue operations through the running CLI, including import of saved jobs.
- [ ] Enforce separate limits for active jobs and workers per job.
- [ ] Define states such as queued, probing, downloading, paused, verifying, completed, failed, and canceled.
- [x] Make invalid state transitions fail predictably.
- [x] Retry transient failures with capped backoff and a finite retry budget.
- [x] Respect bounded Retry-After guidance and avoid retrying permanent failures indefinitely.
- [x] Make backoff waits cancelable and shutdown responsive.
- [x] Keep progress rendering from blocking transfer workers.
- [x] Expose clear error details and allow explicit retry of failed jobs.
- [ ] Explain when resume is unavailable and a fresh download is required.

**Done when:** Several queued jobs remain responsive under failure, and limits and lifecycle transitions hold during pause, cancel, retry, and shutdown.

## 13. Phase 6 - Verify cross-platform behavior

**Goal:** Verify Linux for the first release, then establish the same product behavior on Windows and macOS through native testing.

- [ ] Use the same repository, Go toolchain version, and dependency versions on development machines.
- [ ] Commit go.mod and go.sum; keep local state and generated downloads out of Git.
- [ ] Use portable path and configuration-directory APIs.
- [ ] Isolate unavoidable OS behavior behind small adapters.
- [ ] Test permissions, path names, file locking, destination conflicts, and rename behavior.
- [ ] Run unit and integration tests on native CI runners for the target operating systems.
- [ ] Run the race detector on supported configurations and document coverage.
- [ ] Perform native smoke tests for pause, recovery, cancellation, and final output.
- [ ] Publish only OS/architecture combinations whose stated support has been verified.

Go recognizes platform-specific source names such as bind_linux.go, bind_windows.go, and bind_darwin.go. See [Go build constraints](https://pkg.go.dev/cmd/go#hdr-Build_constraints).

Cross-compilation creates a target executable; it does not verify runtime behavior. WSL-based tests exercise Linux behavior and do not replace native Windows tests. Dependencies using cgo may need additional target build tools; evaluate this before selecting storage or GUI libraries. See [cgo documentation](https://pkg.go.dev/cmd/cgo).

**Done when:** Linux release targets have native evidence and the support matrix distinguishes later Windows and macOS targets from verified support.

## 14. Phase 7 - Validate and release v0.1

**Goal:** Make the engine's reliability visible and reproducible.

| Scenario | Evidence and status |
| --- | --- |
| Ordinary transfer | Automated: `TestDownloadSuccess` and `TestAcceptanceBuiltCLIDownload` compare the built CLI output with a local fixture. |
| Parallel transfer | Automated: `TestParallelRangeWorkerCountsAndRemainders` checks 2, 3, 4, and 16 workers with different remainder sizes. |
| Range unsupported | Automated: `TestParallelFallback` checks ignored ranges, weak validators, and small files. |
| Remote file changes | Automated: `TestResumeInterruptedDownload`, `TestChunkRecoveryRejectsChangedRemote`, and `TestParallelRejectsInvalidWorkerResponses`. |
| Pause and restart | Automated: `TestQueuePauseWaitsForCheckpoint`, `TestParallelCancellationResumesContiguousPrefix`, and chunk-recovery tests. |
| Process crash | Automated: checkpoint and publication crash tests, `TestParallelChunkCrashRecovery`, and `TestQueueRepeatedCrashAndTransientRecovery`. These do not establish power-loss durability. |
| Network loss and slow responses | Automated: retry, timeout, repeated transient recovery, and cancellation tests; extended recovery-window behavior remains open. |
| Disk and permission failures | Automated on Linux: sequential and parallel ENOSPC, permission, and checkpoint-failure tests. The tests can skip when namespaces are unavailable or when run as root. |
| Checksum mismatch | Automated: `TestDownloadChecksumAndProgress` and `TestParallelChecksumMismatchDoesNotPublish`. |
| Conflicting writers | Automated: `TestJobLockExclusiveAcrossProcesses` and destination-conflict tests. |
| Unknown-size transfer | Automated: `TestDownloadUnknownLength` verifies output and progress without a percentage. |
| Resource limits | Partial: queue and connection limits are tested; [recorded measurements](BENCHMARKS.md) cover individual and queued jobs, ext4/tmpfs, and a throttled local server, but do not prove general bounds. |
| Native platforms | Partial: built CLI and extracted release-candidate smoke tests passed locally on Ubuntu 26.04.1 Linux/amd64 with Go 1.26.8, including a real internet download to ext4; other environment tests remain open. Windows and macOS cross-builds do not verify runtime behavior. |

The local Linux acceptance suite passed on 2026-11-05 with no skips. On the same date, a locally built `go build -trimpath` Linux/amd64 binary downloaded the Go 1.24.0 source archive from GitHub to ext4 (30,902,662 bytes, 14.63 seconds); its SHA-256 `4f47402534b970bcf9f92a7af40de20f77ed7c1a295c5aec650b0f1e52c5e026` matched an independent curl download. This is a consistency check, not a publisher-signed checksum or a packaged-release test; the transfer does not establish parallel range or resume behavior. The first hosted CI run passed for commit `c972c54`. Full v0.1 acceptance remains open, including broader resource bounds, packaged release testing, and the remaining roadmap requirements.

### Release tasks

- [ ] Complete the acceptance scenarios and investigate races or resource leaks.
- [ ] Fuzz untrusted range and metadata parsing where useful.
- [x] Measure throughput, memory, and cancellation behavior under a recorded workload; see [BENCHMARKS.md](BENCHMARKS.md).
- [x] Include sequential comparisons; see the scoped results in [BENCHMARKS.md](BENCHMARKS.md).
- [ ] Write a README with setup, usage, architecture, limitations, and recovery instructions.
- [ ] Record tested environments, dependency versions, and benchmark conditions.
- [ ] Create a short demonstration of interruption and successful recovery.
- [ ] Package release artifacts and document checksums and build instructions. A local Linux/amd64 candidate for `c972c54` was packaged and smoke-tested; publication and final release artifacts remain open.
- [ ] Review license, dependency obligations, and contribution guidance.

**Done when:** A new user can run the documented workflow, reproduce the key tests, and understand the first release's limitations.

## 15. Phase 8 - Prove multi-interface downloading

**Target:** v0.2, experimental on one selected operating system.

**Goal:** Use explicit network paths without weakening download correctness.

- [ ] Select the first platform and document required privileges and routing assumptions.
- [ ] Enumerate candidate interfaces and detect usable addresses.
- [ ] Implement explicit single-interface selection before combining networks.
- [ ] Verify actual egress paths; selecting a local source IP alone is insufficient evidence.
- [ ] Keep connection pools isolated by interface so reused connections cannot defeat selection.
- [ ] Define how DNS resolution, proxies, redirects, and IPv4/IPv6 interact with the chosen path.
- [ ] Assign independent chunk requests across selected interfaces.
- [ ] Handle different speeds through bounded scheduling.
- [ ] Retry unfinished chunks when an interface disappears.
- [ ] Report bytes, speed, and failures per interface.
- [ ] Test server restrictions when requests arrive from different public IP addresses.
- [ ] Compare against single-interface transfers using real independent uplinks.
- [ ] Document tested configurations and unsupported cases.

Go provides dialer source-address selection and socket hooks, but interface enforcement requires platform-specific behavior and routing tests. See [net.Dialer](https://pkg.go.dev/net#Dialer).

This feature uses multiple ordinary connections. It does not require one TCP connection to migrate between networks. Wi-Fi and Ethernet sharing one upstream internet bottleneck do not imply additional available bandwidth.

**Done when:** A controlled demonstration proves intended egress, correct output, and recovery from interface failure. Report measured benefit without promising combined link speeds.

## 16. Additional platforms, desktop usability, and browser integration

### v0.3: Broader interface support

- [ ] Implement the remaining OS adapters without duplicating the engine.
- [ ] Test interface binding, address changes, and failover on native systems.
- [ ] Publish a per-platform capability and privilege matrix.

### Desktop GUI (parallel track)

The operator selected a Go desktop app using Fyne on 2026-11-05. The current prototype separates standalone Downloads from a Queue tab. On 2026-11-09, the operator reported that the queue interaction is confusing. The selected direction is one Downloads view backed by the persistent scheduler, followed by browser integration.

- [x] Select Fyne after a working desktop proof of concept and operator review.
- [x] Add prototype history, checkpoint resume, duplicate-file choices, queue controls, row progress, and live speed limits; see the recorded tests in SESSION-LOG.md.
- [ ] Evaluate accessibility, packaging, native dependencies, and maintenance on the installed Linux build.
- [ ] Complete the unified Downloads and browser milestones below.
- [ ] Plan native packaging, signing, and distribution separately.

### Desktop usability milestone - One Downloads view

**Problem:** Users currently have to understand two download lists, queue ownership, and separate Open/Add/Start actions. The standalone list also bypasses the queue's simultaneous-download limit.

**Target interaction:** Add a URL or accept a browser download, choose **Download now** or **Download later**, and see it in one list. Newly approved Download now jobs start automatically when a slot is free. Extra jobs show **Waiting** and the reason, such as "Waiting for a download slot." Download later jobs remain paused until explicitly started. Restored interrupted jobs still reopen paused; opening the application must not silently resume them.

- [x] Map the current Downloads and Queue behavior and select a screen with left-side state filters, one list, contextual actions, and Downloads/Settings tabs under the operator's design-and-implement request.
- [x] Replace the separate Queue tab with All downloads, Downloading, Waiting, Paused, Completed, and Failed filters. Keep canceled entries in All/history and explain waiting without exposing scheduler commands.
- [x] Start the scheduler automatically. Remove Open queue, Close queue, and Start queue from normal use; retain exclusive ownership and show a competing CLI queue conflict.
- [x] Use one Add download dialog for pasted links, with filename, folder, Download now, Download later, and safe duplicate-file choices. Browser intake will reuse this behavior when that milestone is implemented.
- [x] Route all new desktop jobs through one scheduler and one simultaneous-download limit. Put concurrency, connection limits, default folder, and per-download/overall speed settings in Settings.
- [ ] Show filename, progress, speed, useful remaining time, state, and a short failure explanation in each row; support long filenames, unknown totals, keyboard navigation, and readable labels.
- [x] Offer contextual Pause, Resume, Retry, Open file, Show in folder, and Cancel actions; separate remove-from-history from confirmed deletion of verified partial data.
- [x] Add stable row checkboxes, visible-list Select all/Clear selection, and batch Start/Resume, Pause, Retry, Cancel, list-only removal, and confirmed disk deletion. The queue owner stops writers before removal, verifies completed-file identity against confirmation, and reuses owned partial cleanup. Failures remain visible, hidden selections clear on filtering, and archived entries prevent rediscovery after list-only removal. Real controller/HTTP tests and rendered list/confirmation checks passed; installed preview updated on 2026-11-09.
- [x] Resolve safe server-provided filenames in the queue owner before writing body data, reserving paths against other jobs. Preserve copy/replacement choices and existing output protections.
- [x] Define single-owner startup/shutdown and migrate history, queue IDs, speed-limit keys, and imported checkpoints with metadata backups. Repeated adoption matches both destination and source identity.
- [x] Preserve completed history, saved progress, and partial files during migration without starting network transfers. Keep unimportable history visible.
- [ ] Complete acceptance for limits, transitions, pause/resume, recovery, ownership conflicts, duplicate filenames, settings, and migration. Automated public-URL and controller coverage is implemented in desktop/unified_test.go and desktop/controller_test.go; mixed browser intake follows the browser milestone, and interactive review remains open.
- [ ] Run an interactive Linux acceptance session: a user can add, pause, resume, and locate a completed download without learning queue terminology.

**Done when:** One list and one scheduler manage desktop jobs, normal use has no manual queue setup, and migration plus interactive acceptance pass.

### Automatic mode design (2026-11-09; stages 1–4 implemented)

**Stage 1 implementation:** `request_budget.go` is the queue-owned, mutex-protected global/origin lease broker; `measurements.go` provides atomic transfer counters and bounded 10-second observations; `adaptive.go` is a pure policy, initially exercised by fake-clock simulations and now connected to the queue in stage 3. The transport acquires capacity separately for every redirect hop and releases it on body completion, close, error, or cancellation. Measurements separate accepted file bytes, raw response bytes (including probes), request/header/network waits, intentional limiter waits, file writes, data sync, checkpoint work, local verification, and retry waits. Runtime counters reset on each transfer session and are excluded from queue/checkpoint JSON. Durations sum across workers and can exceed wall time; local verification includes saved-prefix and final-file reads, not a CPU profiler of streaming hashes. Configuration/job-set changes invalidate comparison baselines; retries/errors and interrupted sampling invalidate affected windows.

**Stage 2 implementation:** `worker_control.go` carries queue-owned, runtime-only ceilings and targets through bounded notifications. The transfer owner launches only selected parts and retires excess workers independently, waits for their final prefix checkpoint acknowledgments, and rehashes saved prefixes before assigning their remaining suffixes. Existing version-four part boundaries and queue schema 2 are unchanged. New Automatic transfers start one worker and choose fixed parts up to the connection maximum with roughly 256 KiB minimum parts. Manual uses the configured maximum; live configuration changes resize active parallel work, and switching to Automatic resets its target to one. Raising a maximum cannot split existing parts; snapshots explain saved-layout limits and retirement. The structured `workers` request exercises bounded per-job targets for this foundation; stage 3 now connects the measured throughput policy. Sequential streams keep one worker. Worker changes invalidate measurement windows, intentional cancellation is distinguished from HTTP/2 body-close errors, and real storage/checkpoint errors still stop the job. Tests cover repeated 1→2→4→1 changes and HTTP offsets, HTTP/2 streams and released leases, permission-failure recovery, local/remote changes, process kills during/after retirement, settings/mode changes, paused restart, sequential fallback, matching output, and runtime-only state.

**Stage 3 implementation (2026-11-10):** `automatic_queue.go` is the queue-owned runtime controller connecting real accepted-byte measurements, the pure policy, file admission, and existing worker controls. Automatic begins with one file/worker, requires two usable non-overlapping 10-second windows, changes one variable, waits for stable transfer ownership plus five seconds, and compares two further windows. Extra workers need at least 10% aggregate improvement, no affected-file regression below 90% of its baseline, and no doubled header-delay estimate where fresh measurements exist. Baseline/trial windows differing by a factor greater than 1.5 are discarded; generation changes, retries/errors, worker transitions, unavailable speed settings, and interrupted sampling cannot establish a gain. Successful trials wait at least 30 seconds; failed trials wait at least a minute before reconsideration. Only the one expected file admission can preserve a trial baseline; unexpected additional membership discards it. File trials have priority when useful capacity exists; worker candidates favor fewer workers and need at least 16 MiB remaining. Failed worker trials retire safely; failed file trials lower the desired target and drain already-admitted files without cancellation. Draining files are never counted as a new admission experiment.

The same global/origin broker enforces 429/503 cooldowns before returning response leases, including redirect hops. Retry-After beyond one minute stops automatic retry and blocks that origin until an explicit user retry/resume; normal timed cooldowns wake pending requests without holding capacity. Server feedback halves affected live worker targets and reduces aggregate origin request capacity, so several one-worker files cannot bypass backoff. After a quiet minute, at most one origin per tick recovers its configured capacity; this invalidates the comparison baseline. Repeated measured timeouts reduce the affected file. Dormant retry/cooldown sessions may leave bounded opportunities for healthy origins, never exceeding the configured session maximum. Shared/default/per-file speed ceilings freeze relevant growth when measured throughput and intentional limiter waits confirm the limit. Settings/job-set/speed changes discard unproven trials; Manual restores configured worker/file concurrency. Snapshots expose runtime-only target, draining, activity, state, and reason; checking, retry/budget waits, and verification are distinct from active downloading. The desktop keeps Automatic as default and customization collapsed, shows an explanation line, and retains complete part reasons in Parts details. Queue schema 2, checkpoint v4, dependencies, saved part boundaries, and restart-paused behavior are unchanged. Real HTTP tests with an accelerated owner clock cover beneficial/unhelpful worker and file trials, byte-identical output, Manual restoration, paused jobs, runtime-only state, and cooldown isolation; deterministic tests cover limits, stability, disruption, and canceled/woken grants. Stage 4 adds resource feedback below; native adaptive benchmarks remain stage 5.

**Stage 4 implementation (2026-11-10):** `hardware.go` samples asynchronously once a second, keeping the queue owner free of proc/sys filesystem reads. `hardware_linux.go` uses process CPU time, affinity, and the minimum visible cgroup-v2 ancestor quota; utilization is relative to that capacity. Capacity/scope changes invalidate learning, and observations older than 3.5 seconds are Unknown. Cgroup v1, inaccessible limits, and unsupported platforms do not fabricate CPU capacity. Memory observations include host availability, engine-only RSS, host/cgroup PSI, ancestor memory.high/max usage ratios, and local memory-event counter deltas. Local events avoid attributing a sibling's private limit to FluxGet; long sampling gaps preserve event evidence. Invisible constraints outside a cgroup namespace cannot be inferred.

`resource_guard.go` holds new file admission and upward worker trials during sustained pressure, reduces one parallel worker at a time through existing durable retirement, and lets sequential streams drain. CPU uses >85% for 10 seconds, recovering below 70% for 30 seconds; observed quota throttling >=10% also triggers pressure (recovery <5%). Initial memory hypotheses are PSI avg10 some >=10% or full >=1%, cgroup usage >=90% of a finite high/max limit, or host availability below max(64 MiB, 5% of RAM), sustained for 10 seconds. New local high/max/OOM events trigger immediate backoff. Recovery needs 30 seconds with some <5%, full <0.5%, ratio <80%, host availability >max(128 MiB, 10%), and the signal that caused pressure still available. These are conservative control hypotheses, not measured optimal settings or an OOM-prevention guarantee. Worker reductions occur no more than once per 10 seconds; recovery restarts normal measured learning.

`storage_observations*.go` maintains bounded latency histograms for destination writes/data sync and checkpoint writes/sync/publication separately. Linux device IDs identify filesystem roles, not physical disk type. Two usable 10-second windows above 500 ms p95 or twice a healthy per-job/operation/filesystem baseline hold growth. Only one storage reduction trial runs across the queue; two valid post-change windows must show >=20% latency improvement to retain it, otherwise the earlier target is restored where current settings permit. Trials time out after a minute, then wait a minute before reconsideration. Error/retry/retirement windows cannot establish improvement; absent operations remain Unknown. Quantiles are bucket upper-bound estimates (the overflow estimate is capped at 60 seconds), not exact percentiles. No disk-type inference or synthetic benchmark traffic occurs in normal operation.

`transfer_buffers.go` reserves a 32 KiB streaming slice and 32 KiB hashing slice before each queue transfer/worker starts, allocates lazily, and releases once after ownership ends. Aggregate reservations cannot exceed 128 MiB streaming or 32 MiB explicit hashing; exhausted workers wait cancelably. Copy fast paths cannot allocate replacement untracked slices. This bounds these owned slices, not HTTP/TLS/Go/Fyne allocations, total engine RSS, GUI RSS, or OS cache. All observations, histograms, targets, and filesystem identities remain runtime-only; no new settings, dependency, queue schema, or checkpoint-format change.

Stage-4 verification includes deterministic hysteresis and pressure traces, stale/missing observations, ancestor quotas and sibling-event isolation, real HTTP safe retirement/Manual restoration/output integrity, and buffer exhaustion/cancellation. A native 50% CPU quota probe and bounded memory-limit/RSS reads succeeded; destination and checkpoint operations were observed on distinct real filesystems. An initial induced memory.high pressure probe stalled about 165 seconds and failed its assertion, revealing lost event evidence across long gaps; that defect is fixed and regression-tested. The replacement native memory check is bounded and read-only. Full low-memory stress acceptance and representative HDD/SSD/checkpoint performance comparisons are still stage 5, not claimed by these probes. Linux semantics follow [cgroup v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html) and [PSI documentation](https://docs.kernel.org/accounting/psi.html).

**Operator default preference:** On 2026-11-09 the operator selected Automatic as the default, with optional customization. Stages 1–4 provide shared budgets, with 8 total and 4 per origin by default, safe live worker controls, throughput-driven targets, and Linux hardware guardrails. Existing settings without a mode migrate to Automatic while retaining file, worker, speed, and folder values. An explicit Manual selection is preserved. Customize download management is collapsed by default. Settings report success only after scheduler acknowledgment and persisted settings; a settings-write failure restores the prior scheduler policy. Standalone CLI sessions remain Manual unless configured through the desktop protocol.

**Goal:** Choose useful file and range-worker concurrency from observed results while keeping user ceilings, speed limits, server restrictions, and verified recovery authoritative. Automatic mode does not promise maximum internet speed or uninterrupted browsing. These are application control rules, not a replacement for the transport's congestion control. The numerical thresholds below are initial hypotheses to validate in benchmarks, not measured optimal settings.

#### Approach comparison

| Option | Advantages | Costs / limits | Selected direction |
| --- | --- | --- | --- |
| Manual settings only | Predictable and already implemented | Requires users to tune two limits | Retain as Manual mode |
| Choose counts from CPU cores, RAM, or advertised link speed | Simple startup heuristic | Cannot establish server performance, shared bandwidth, disk pressure, or current contention | Do not use as the main controller |
| Measure real downloads and adjust within ceilings | Responds to changing conditions and reports why it changes | Needs instrumentation, controlled experiments, and recovery tests | Selected; default Automatic per operator request |

#### User controls and visible behavior

- Default to **Automatic** for new settings and existing settings without an explicit mode. Put **Mode: Automatic / Manual** and the concurrency controls under collapsed **Customize download management**. An explicit Manual choice persists. Changing mode never starts a user-paused or Download later job. Settings explains measured adaptation and Linux pressure backoff; runtime CPU, engine-memory, and reserved-buffer observations appear inside customization.
- Keep **Simultaneous downloads** and **Connections per download**. In Automatic, label them **Maximum simultaneous downloads** and **Maximum connections per download**, with help text: "FluxGet may use fewer when extra workers do not help." Retain the existing user's values; proposed fresh-install ceilings remain five files and four workers per file.
- Put **Maximum total connections** (initial 8) and **Maximum connections to one server** (initial 4) under Advanced. These count in-flight HTTP requests, including metadata probes and retries; they do not claim to count TCP sockets. Keep application hard bounds: 1–64 files, 1–16 workers per file, 1–64 total requests, 1–16 requests per server. The smallest applicable ceiling wins. A total budget smaller than the file maximum is valid: some files wait. These new global/origin budgets apply in Automatic; Manual keeps the current file/worker settings. On a mode change, already issued leases remain tracked until returned, and the owner installs the new policy atomically before granting new work.
- "Server" means a normalized origin (scheme, lowercase hostname, effective port), not a full URL. Separate hostnames can still share infrastructure; do not promise an aggregate limit across a CDN. Redirect hops release/reacquire grants for their destination origin before dispatch. The HTTP transport continues to enforce protocol-level stream limits.
- Example summary: **Automatic · 2 downloading · 3 waiting**. Example row: **4 parts · 2 active · up to 4 connections**, with **Why?** details such as "Extra connections did not improve speed," "Your speed limit is reached," "Waiting for a server slot," or "Reducing work while storage catches up." Keep observed states distinct from configured maximums.
- Use clear **Learning**, **Stable**, **Backing off**, and **Limited by your settings** status messages. Do not display unavailable measurements as healthy or label a server-delay estimate as actual network RTT.
- Speed settings retain their current meaning. A custom speed ceiling can be removed with Remove limit; default and shared overall ceilings continue to apply. Automatic never increases or disables a speed ceiling.
- A reduced maximum prevents new grants immediately. Existing over-limit work is marked **Reducing active connections…** until extra workers stop safely; counts cannot fall instantaneously. Returning to Manual restores configured concurrency within the same drain rules, rather than restarting downloads. No surprise file deletion, restart, or final publication.

#### Ownership, budgets, and persistence

The queue owner remains the only global scheduling authority. It owns immutable observations, desired budgets, per-origin cooldowns, and a bounded lease broker. Each transfer owner owns its part/checkpoint state and applies its desired worker count; workers never edit the queue or checkpoint JSON directly.

```text
Settings ceilings + transfer/resource observations
    -> queue owner + adaptive controller
    -> global/origin request leases + per-file worker targets
    -> transfer owners -> validated ranges -> prefix checkpoints
    -> structured snapshots -> Downloads UI
```

- A pure policy component takes a timestamped observation and returns a bounded decision plus reason. It performs no network, filesystem, or UI calls. Implemented files are `adaptive.go` (pure measured policy), `automatic_queue.go` (live coordination/admission), `request_budget.go` (leases/cooldowns), and `measurements.go` (runtime counters and observations); resource adapters remain planned. Tests use a fake clock. Go standard library and the pinned Fyne stack remain sufficient.
- Request admission grants global and origin capacity atomically. Never hold a global token while waiting for an origin token, and never hold any request token during retry backoff. Bounded queues, cancellation, EOF/error, redirects, and shutdown must all return leases exactly once. The queue event loop must remain responsive while workers wait.
- Keep per-file transfer sessions bounded by the user file ceiling. A file counts as Downloading only when it has active transfer work; internal initialization, waiting for capacity, verification, and server cooldown must have honest reason/state snapshots. Do not instantiate an unbounded parked transfer for every queued file.
- Give admitted files one useful request opportunity before granting extra workers to another file, using round-robin grants. Queue admission uses FIFO among currently eligible origins; an origin cooldown must not block unrelated origins. Waiting files can still wait for an active file to finish when file slots are full. Initial scope does not promise bounded wait times for huge files or periodically pause files solely for rotation.
- Persist the mode and user ceilings as optional fields in desktop settings and in explicit structured scheduler configuration. Missing mode/budget fields use Automatic with total 8 and per-origin 4; explicitly invalid values are rejected. Keep learned speeds, transient cooldowns, grants, and reason snapshots runtime-only; no URL-based performance history or hardware fingerprint is stored. Do not add speculative fields to queue records or change queue schema 2 solely for Automatic.
- The scheduler must acknowledge effective configuration before the UI reports it applied. On failure, retain its last accepted policy and show the error. Auto/Manual changes use one coherent settings transaction; speed-file polling must not create a second adaptive owner. Standalone CLI remains Manual initially; later Auto CLI support must reuse this controller and budgets.

#### Measurements and decision rules

Start with one eligible file and one worker. Sample existing download traffic once per second. Build 10-second windows of fresh accepted body bytes; saved bytes do not count as newly transferred. Attribute time separately to network reads, intentional rate-limiter waits, file writes, synchronization/checkpointing, validation, and retry delays. Windows spanning a job completion, settings change, reconnect, or worker retirement are not valid before/after performance comparisons.

1. **Observe before growing:** Require two usable windows with no current pressure or cooldown. Use total throughput and the affected file/origin throughput, not a single fast request. Freeze upward experiments when bytes/time observations are missing or unstable. Near-complete files and sequential-only responses are not candidates for extra range workers.
2. **Change one variable:** Run one performance experiment at a time across the queue: either add one worker to an admitted file or admit one additional eligible file. Skip worker trials if less than about 16 MiB remains for that file. Limits, checkpoint correctness, and normal user actions take precedence over an experiment.
3. **Compare:** After settling for five seconds, compare two 10-second windows against the prior baseline. Provisionally keep a worker increase only if median aggregate throughput improves by at least 10%, without a sustained latency/pressure regression. Otherwise restore the earlier target and wait at least 30 seconds before another upward trial. Sudden external changes invalidate the comparison instead of claiming causation. Re-test conservatively after a minute of stable conditions so the controller can discover changed bandwidth.
4. **File admission differs from worker trials:** Try another eligible file if queued work exists and there is budget headroom, especially when current files hit their individual speed ceilings or a different origin may help. A started file is not canceled just because the throughput trial was inconclusive. Lower the desired admission target, stop adding files, and let excess active files complete. This avoids repeated file restarts; stronger file preemption requires a separate acceptance gate. Show that a target is draining rather than claiming it is already enforced.
5. **Recognize intentional limits:** When throughput is within about 5% of an applicable speed ceiling and limiter waits confirm it, do not add workers to defeat that ceiling. A file ceiling does not forbid starting another file if the shared ceiling and all budgets allow it. Never infer the internet link's maximum speed solely from current download throughput.
6. **Back off selectively:** On 429 or transient 503 responses, reduce the affected origin's target (halve it, minimum one) and stop new requests to that origin during the retry cooldown. Reuse the existing retry bounds and Retry-After parsing; a delay beyond the current one-minute automatic retry allowance stops automatic retry and requires user action. Other jobs to that origin must not bypass its cooldown; unrelated origins can continue. Repeated timeouts reduce the affected file/origin target; they are not automatically proof of global congestion. Invalid range/validator responses trigger the existing correctness failure path, not an adaptive retry that silently accepts different content.
7. **Pressure overrides growth:** Persistent CPU, memory, or storage pressure stops new grants and reduces desired workers gradually. Do not classify high write latency alone as proof of a slow disk: include sync cost, queueing, quota limits, and measurement availability. ENOSPC or failed checkpoint publication is a storage error; preserve the prior valid checkpoint and report failure rather than tuning around it.
8. **Network changes:** If downloads lose connectivity or observations shift sharply, abort experiments and discard throughput baselines. Start conservatively after connectivity returns; respect retry and origin cooldowns. Restarts restore jobs paused and discard all learned targets.

#### Hardware and network guardrails

Stage 4 adds Linux adapters on top of stages 1–3. Unsupported or unavailable CPU/memory observations return Unknown and retain the conservative request budgets; missing observations never count as healthy recovery. Hardware observations do not raise configured request ceilings. Manual ignores hardware-driven target adjustments; bounded owned buffers apply in both modes.

- Evaluate CPU pressure using sustained utilization relative to the process's available CPU quota, not the raw core count. Initial test thresholds: above 85% for 10 seconds blocks growth; below 70% for 30 seconds permits reconsideration. They are tunable hypotheses and must work in cgroups as well as a normal desktop.
- Keep streaming buffers bounded. Initial resource limits: at most 128 MiB of transfer read/write buffers across the app and 32 MiB of explicitly allocated hashing buffers; the grant broker accounts for them before launching a worker. This is not a total-process RSS guarantee, since Fyne, Go, and HTTP transports also allocate memory. Add OS low-memory pressure and process memory observations separately; do not read the whole file into RAM for hashing.
- Track the destination filesystem and checkpoint filesystem separately. Implemented initial storage guardrail: p95 write/sync duration over 500 ms in two windows blocks growth; a value over twice that filesystem's stable baseline also triggers observation, not an unqualified device diagnosis. Reduce one worker, measure again, and keep the reduction only when pressure improves. Benchmark HDD, SSD, slow checkpoint storage, and constrained filesystems.
- Response-header delay from real download requests can detect a regression relative to the same origin's baseline, but is not network RTT and cannot measure another app's responsiveness. Use it only as a supporting signal, alongside throughput and errors. Do not generate external speed-test traffic, ping third-party hosts, or inspect other apps' browsing. A dedicated "keep browsing responsive" promise would require its own measurable design and acceptance.

#### Dynamic workers and saved parts

- Separate **saved file parts** from **currently active workers**. Preserve the version-four checkpoint's existing coverage, prefix lengths, and hashes. New Automatic transfers choose fixed boundaries up to the configured per-file maximum (no more than 16, with at least roughly 256 KiB per part); start only one worker, leaving other parts Waiting. Small/unsupported responses use one sequential worker.
- An existing four-part checkpoint remains four parts. Raising the maximum to eight does not invent four new saved ranges. Effective worker count is also bounded by remaining eligible parts; show "Limited by saved part layout" when relevant. Live splitting/work stealing is explicitly deferred; it needs a separate recovery-format design.
- To lower a worker target, the transfer owner requests retirement of selected workers, stops their HTTP work, waits for their writes to finish, and publishes their final valid prefixes before releasing their part ownership. Siblings keep working. Retirement is an internal scheduling outcome, not a job failure, whole-job pause, or retry-budget charge. Never assign a suffix to a replacement worker while the prior writer can still write there.
- Increasing a target starts an unowned waiting suffix at `part.Start + savedLength`, subject to fresh request validation and atomic global/origin admission. Hash the saved prefix before reuse; compare it to the recorded digest and verify the strong ETag/remote representation. Retain the existing fully validated EOF rule before marking a part Complete.
- A sequential response without safe prefix/range recovery is not repeatedly canceled for tuning. Let it drain when targets shrink; explicit user pause/cancel and real failures retain existing semantics. A checkpoint-save error stops the affected transfer safely; the controller must not report retired bytes as saved or continue spawning replacements.

#### Delivery stages and acceptance gates

| Stage | Deliverable | Evidence required |
| --- | --- | --- |
| 1 | ✅ Implemented: structured measurements, Manual regression baseline, pure policy simulation, atomic global/origin broker, default Automatic controls | Race-tested fake-clock policy, bounded/cancelable grants, fair admission, redirect and HTTP/2 accounting, real multi-download budgets/restart, runtime-only metrics, settings migration and acknowledgment/rollback; initial policy simulations; connected in stage 3 |
| 2 | ✅ Implemented: resize workers within existing saved part boundaries | Race-tested real HTTP and HTTP/2 1→2→4→1 transitions, exact suffix offsets, repeated retirement/reuse, file/remote validation, checkpoint-error recovery, retirement process kills, settings/mode changes, sequential fallback, paused restart, and byte-identical output |
| 3 | ✅ Implemented: measured adaptive decisions and file admission; default Automatic UI | Race-tested pure policy and real HTTP worker/file trials with accelerated owner clock, matching output, failed-trial rollback/draining, Manual restoration, paused jobs, limit/headroom checks, cooldown isolation, settings acknowledgment/rollback, runtime-only state, and rendered explanations |
| 4 | ✅ Implemented: Linux hardware observations and tuning | Quota/affinity, cgroup/PSI fixtures, native constrained CPU and memory reads, stale/missing signals, storage latency replays, separate real filesystem roles, buffer exhaustion, HTTP retirement/recovery and Manual restoration; representative disk/low-memory stress benchmarks remain stage 5 |
| 5 | Full adaptive acceptance for the default Automatic experience | Replayable synthetic and native trials compare Manual and Automatic on throughput, request counts, variability, CPU/memory/storage cost, and recovery; operator review passes |

Required scenarios include one fast stream that gains nothing from more workers; a server that benefits from several ranges; changing bandwidth; user speed ceilings; one throttled origin beside a healthy origin; 429/503 and Retry-After (including the over-one-minute stop rule); redirects crossing origin budgets; unknown-length sequential responses; HTTP/2 request multiplexing; FIFO/grant fairness; simultaneous settings changes; and a worker retired during a checkpoint failure. Kill/reopen at retirement and checkpoint publication boundaries and verify there are no duplicate writers, missing bytes, falsely complete ranges, stale leases, or silently resumed paused jobs.

**Protocol references:** Range validation remains governed by [HTTP range responses](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.7). A 429 response can include Retry-After as described in [RFC 6585](https://www.rfc-editor.org/rfc/rfc6585.html#section-4). HTTP/2 stream limits apply independently of app request budgets; see [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html#section-5.1.2). Controller thresholds and resource budgets above are FluxGet design proposals, not values prescribed by these standards.

### Browser integration milestone - Chrome, Firefox, and Opera

**Goal:** When capture is enabled, clicking a supported download in a selected browser brings it into FluxGet's unified Downloads experience. Users can turn the feature off globally or for individual browsers. This is a separate product milestone after the unified desktop foundation, starting on Linux/amd64.

#### Integration choice

| Option | Advantages | Costs / limits | Decision |
| --- | --- | --- | --- |
| WebExtension plus native messaging | Browser-supported app communication, explicit extension identities, no listening web port | Requires an extension and native-host registration for each supported browser/package | Selected architecture |
| WebExtension plus localhost HTTP service | Familiar request/response protocol | Requires port management, authentication, origin protections, and service lifecycle; still requires an extension for capture | Alternative only if native messaging proves unsuitable in a documented environment |
| Custom URL scheme such as fluxget:// | Useful for explicit Send to FluxGet links | Does not provide general automatic capture or browser download lifecycle control; requires OS registration and input validation | Optional manual integration, insufficient for the requested feature alone |

Native messaging and download operations are documented by [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging), [Chrome downloads](https://developer.chrome.com/docs/extensions/reference/api/downloads), [Firefox native messaging](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging), and [Firefox download-created events](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/downloads/onCreated). Firefox has different host allowlist/registration details and [background-script support](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/manifest.json/background). Opera documents [Chromium extension compatibility](https://help.opera.com/en/extensions/); its installed-version native-host registration and API behavior must be proven, not inferred from Chrome.

#### Settings and expected behavior

| Control | Planned behavior |
| --- | --- |
| Enable browser integration | Master switch, off until the user opts in during setup. Disabling it stops new capture without canceling existing FluxGet jobs. |
| Chrome / Firefox / Opera | Independent enabled switches with per-profile connection status. Selecting a browser never enables the others. The effective policy requires master, browser, profile consent, and extension readiness. |
| Connection status | Show Not installed, Extension missing, Disconnected, Ready, Disabled, or Unsupported with Install extension, Test connection, and Repair actions as appropriate. Detection alone never means capture is ready. |
| Capture mode | Ask before downloading by default; optional automatic Download now mode after successful setup, or Manual only for context-menu sending without automatic capture. All modes use the same scheduling and destination rules. |
| Browser extension popup | Show the effective policy, provide a shortcut to settings, and offer Use browser for this download and a temporary capture pause. Local extension disable always wins. |
| Exceptions | Per-site exclusions and supported file-type rules; an exclusion takes priority over capture. Clearly distinguish the download host from the initiating website when applying rules. |
| Private browsing | Off by default, requires separate explicit consent and browser permission; defer support until privacy and storage behavior are tested. |

The desktop policy is authoritative and persisted by one owner. Extensions obtain a versioned policy/readiness response before taking over a download. A disconnected host or stale/unknown policy leaves new downloads in the browser. The host also enforces master/browser/profile restrictions, so stale extension state cannot bypass a disabled browser. Changes affect new handoffs immediately at validation; define and test what happens to a handoff already paused or pending.

#### Implementation order and gates

1. **Capability spike and acceptance contract:** Test actual downloads in each installed browser and profile. Record versions, package format (native, Snap, or Flatpak), download-event timing, available request metadata, pause/resume/cancel behavior, extension background lifecycle, and native-host discovery. Browser sandbox restrictions need tested packaging support; do not disable the sandbox. Set a finite handoff timeout and document the supported input matrix before automatic capture.
2. **Shared desktop intake:** Add validated structured add/status requests, stable request IDs, pending handoff records, and idempotent job creation to the single scheduler owner. Specify private IPC access controls, app activation, concurrent browser requests, single-instance locking, disconnects, and protocol-version compatibility. Keep transfer and persistence logic out of extension/UI handlers.
3. **Go native host:** Implement bounded length-prefixed JSON messages over stdin/stdout, reserve stdout for protocol messages, and send redacted diagnostics to stderr. Validate extension origin/identity, request schema, message sizes, URL schemes, filenames, and policy. Launch binaries directly with arguments, never via a shell. Permit only known extension IDs in browser host manifests.
4. **Manual handoff first:** Add Send link to FluxGet to the extension context menu and a connection test. Use a shared JavaScript core with separate Chrome, Firefox, and Opera packaging/configuration. Prove successful enqueueing, desktop activation, and concurrent requests before adding interception. Manual link sending must remain useful when automatic capture is disabled only by the user's capture-mode preference; a master/browser disable blocks all extension intake.
5. **Safe automatic capture:** Use supported downloads events and lifecycle APIs. These events occur when a browser download begins, so zero browser traffic or zero temporary bytes is not a guarantee. Verify policy/readiness, classify eligibility, and pause the browser item before preparing the transfer. Persist a pending FluxGet request without starting network work; cancel the browser item only after that request and destination acceptance are acknowledged, then activate the same request once cancellation is confirmed. Do not start two transfers in the normal handoff path.
6. **Failure and crash recovery:** If the item already completed, pause fails, the user chooses Use browser, or eligibility/desktop acceptance fails, abandon the pending FluxGet request and leave or resume the browser item. Confirm browser state before retrying cancellation or activation. Persist request IDs and handoff stages so extension suspension, lost acknowledgments, and host/app crashes cannot create duplicate jobs. Pending requests must not auto-start without the transfer-of-ownership confirmation. After browser cancellation, reconcile the accepted request or show an explicit recovery action; do not promise a canceled browser item can always resume. Fallback downloads must bypass capture once to prevent loops. Never erase unrelated browser history or files.
7. **Settings and setup:** Implement the controls above, synchronize effective policy, test disable changes during a handoff, and provide user-driven install/repair/uninstall registration. The desktop can link to extension installation and verify it; do not silently install browser extensions or edit browser profile databases.
8. **Browser rollout:** Prove Chrome on Linux first, then Firefox with its manifest/host differences, then Opera with independently verified registration and behavior. Keep the same protocol and UI. Publish a capability matrix by browser, version, OS, and package format; unsupported combinations retain normal browser downloads.
9. **Distribution:** Package the host with FluxGet, register it for selected browsers, establish stable extension IDs, and document extension store submission/signing, required permissions, privacy disclosures, updates, and uninstall cleanup. Development-only unpacked extensions are not the release installation experience.

#### Initial supported inputs and later authenticated downloads

- Start with replayable public HTTP/HTTPS GET file downloads and explicit user-selected public links. Do not assume an HTTP URL or an onCreated event proves the original method, public accessibility, or replayability. During the spike, determine how each browser can establish eligibility with minimal permissions; unknown cases remain in the browser.
- Leave POST/form-body downloads, blob/data URLs, browser-generated files, authenticated/cookie-dependent downloads, expiring or single-use secret links, streaming media, and protected downloads in the browser until separately supported. Browser item URL/filename metadata alone is insufficient to reproduce these requests reliably.
- Request only the permissions needed by each stage, such as downloads, native messaging, storage, and context menus. Add site/host permissions only with a documented need and explicit consent. Do not request cookies or broad request interception for the initial public-link feature.
- Do not transfer browser cookies, authorization headers, complete browsing history, or arbitrary request headers in the initial bridge. Redact query strings and sensitive URL components from diagnostics and reject credential-bearing workflows unsupported by the current metadata policy. Define detection limits; a public-looking URL is not proof it contains no secret.
- Never use FluxGet to bypass a browser security block, malware warning, enterprise download policy, or certificate failure. If the browser's security decision is unknown or cannot be preserved for a download class, leave that class in the browser; FluxGet must not imply it inherits browser scanning guarantees.
- A later authenticated-download milestone must design consent, scoped cookie/header access, redirect-origin restrictions, signed-URL handling, secret storage/redaction, expiration, and crash recovery before expanding capture. Do not copy browser profile cookies or weaken TLS checks as a shortcut.

#### Acceptance requirements

- [ ] In each supported browser, a supported download reaches the one Downloads list and produces matching bytes, with no duplicate FluxGet job or completed browser copy in the tested handoff scenarios.
- [ ] Master disable, per-browser disable, profile consent, extension disable, exclusions, and temporary bypass work independently and survive restarts as intended; existing FluxGet jobs continue unaffected.
- [ ] Missing extension/host, outdated protocol, locked queue, desktop startup failure, user rejection, and full disk preserve a browser download or offer a specific recoverable pending job without silent loss.
- [ ] Race tests cover tiny/instant downloads, parallel browser events, duplicate delivery, disable during handoff, extension background suspension, browser exit, and crashes before/after browser cancellation and desktop activation.
- [ ] Unsupported URLs, POST, authentication, private browsing without consent, unsafe filenames, redirects, and browser-blocked downloads follow the documented fallback policy.
- [ ] Installation, extension approval, connection testing, capture, repair, update, and uninstall pass manually on each claimed browser/package combination. Record evidence before claiming support.

**Done when:** Users can opt in per browser, capture supported downloads without understanding queue setup, disable capture reliably, and retain a clear browser fallback for unsupported cases. Exact IDM parity is not a release claim.

### Optional extensions

- Adaptive interface scheduling and user-defined metered-network limits.
- Bandwidth caps, scheduled queues, and completion notifications.
- Additional browser families after Chrome, Firefox, and Opera have verified support.
- Authenticated downloads with a reviewed secret-handling design.
- A background service or local control API if a concrete workflow requires it.

## 17. Completion order and portfolio evidence

| Step | Visible result | Engineering skill demonstrated |
| --- | --- | --- |
| 1 | Correct sequential downloader | HTTP, streaming I/O, error handling, and cancellation |
| 2 | Safe parallel downloader | Goroutines, synchronization, worker pools, and backpressure |
| 3 | Pause and crash recovery | Persistence, lifecycle design, and durability reasoning |
| 4 | Usable queue and CLI | Component boundaries, scheduling, and understandable behavior |
| 5 | Unified desktop Downloads experience | Consistent state ownership, scheduling, migration, and usable controls |
| 6 | Optional browser capture | Native messaging, browser compatibility, reliable handoff, and policy enforcement |
| 7 | Verified v0.1 engine release, checked alongside desktop work | Platform testing, fault injection, documentation, and delivery |
| 8 | Multi-interface prototype | Socket programming, routing, platform adapters, and measurement |

Prefer evidence over broad claims. Show reproducible scenarios, failure handling, design tradeoffs, and measured behavior. Keep the engine understandable enough that another developer can review its correctness.

## 18. Roadmap and immediate next work

The following priority order reflects the operator's 2026-11-09 feedback. Browser integration depends on unified intake and scheduler ownership; multi-interface work follows these usability milestones. Continue the remaining v0.1 release verification alongside product development without claiming an engine release is already complete.

| # | Task | Status | Notes |
| --- | --- | --- | --- |
| 1 | CLI and desktop prototype baseline | ✅ Done | Commit 12635b4 installed locally; recorded automated checks cover the prototype, not full release acceptance |
| 2 | Simplify Downloads and queue interaction | 🔄 Current | Implementation, automated coverage, and local preview installation complete; operator approved the installed preview on 2026-11-09, with detailed interactive acceptance still open |
| 3 | Automatic download management | 🔄 Current | Stages 1–4 implemented: measurements, budgets, live resizing, measured decisions/file admission, and Linux resource guards; next is full adaptive acceptance |
| 4 | Browser capability spike and native-bridge contract | ⬜ Todo | After the selected Automatic foundations; check Chrome, Firefox, and Opera capabilities and package constraints |
| 5 | Shared intake, native host, and manual Send to FluxGet | ⬜ Todo | Versioned protocol, one owner, idempotent pending requests, extension connection tests |
| 6 | Chrome automatic capture and integration settings | ⬜ Todo | Master/per-browser controls, safe handoff, supported public downloads, fallback |
| 7 | Firefox and Opera rollout | ⬜ Todo | Browser-specific manifests/registration; native package testing and compatibility matrix |
| 8 | Browser integration packaging and acceptance | ⬜ Todo | Store distribution, stable IDs, repair/uninstall, crash tests, privacy and permission review |
| 9 | Remaining v0.1 engine release checks | ⬜ Todo | Continue alongside desktop work: resource bounds, release environment, evidence review, final artifacts |
| 10 | Multi-interface experiment | ⬜ Todo | Later; keep platform and routing evidence requirements from Phase 8 |
| 11 | Authenticated browser downloads | ⬜ Todo | Separate credential and request-replay design; unsupported in the initial integration |

### ➡️ Current: #3 - Automatic download management
### ⏭️ Next: #3 stage 5 - Full adaptive acceptance

**Next concrete action:** Stage 5: compare Automatic with fixed Manual baselines on representative networks and hardware, including CPU quota, low-memory, HDD/SSD and slow checkpoint storage, fairness, user limits, and restart recovery. Stages 1–4 are implemented; native adaptive acceptance and representative throughput benchmarks remain open. Browser extensions and a native host remain unimplemented.
