# FluxGet - Project Roadmap

A Go download manager inspired by IDM, with reliable transfers first and multi-interface downloading as a later distinguishing feature.

**Status:** First sequential-download implementation written. The operator reports one successful live download of 22,898 bytes. Formatting, automated tests, race checks, and vetting passed on Linux with Go 1.26.8 on 2026-11-02; broader platform verification remains pending. Other roadmap features remain planned.

**Project purpose:** Learn Go through implementation and publish a GitHub project that demonstrates backend and systems engineering: concurrency, networking, persistence, recovery, testing, and clear architecture.

**How to use this roadmap:** Complete phases in order. Check a task only when code, tests, or documentation demonstrate its completion. Resolve open decisions before implementing the affected feature. Update this file with small, reviewable edits as the project develops.

**Learning workflow:** Use VibeWise Learn while developing. The learner proposes and reasons through meaningful design choices; the assistant explains concepts, gives feedback, and writes the agreed implementation. Local learning notes live in .vibe-wise/. Roadmap proposals are not confirmed architecture decisions.

## 1. Product definition

Build a download manager that accepts direct HTTP/HTTPS file URLs, downloads reliably, and preserves recoverable progress after interruption.
The long-term product goal is to approach IDM's practical download-management capabilities as closely as feasible, with Linux support and a portable codebase. Feature selection and behavior should be checked against real requirements and platform constraints; the release boundaries below remain in effect.

The long-term target is Windows, macOS, and Linux. Development may move between Linux and Windows using one shared Go codebase. Platform-specific features belong in small adapters.

The first release is **v0.1: Reliable Download Engine**, targeting Linux/amd64 first. Desktop development now proceeds alongside the CLI. Keep shared behavior portable so ARM64, Windows, and macOS can be tested and supported later.

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
- The `-list` command discovers saved Linux jobs without changing them. Version-two records persist sequential states; version-three records track independent parallel chunks, and version-one checkpoints remain readable. Live locks distinguish active work from interrupted records. Completed standalone jobs are removed from the saved-job listing; completed queue jobs remain in the queue history.
- An interactive Linux queue persists queued, running, paused, failed, completed, and canceled jobs in an atomically replaced JSON file under an exclusive lock. It schedules up to five active downloads by default (configurable from 1 to 64), imports existing checkpoints, and restores previously active jobs paused. The queue loop owns lifecycle changes; workers report results to it.
- On Linux, new downloads of at least 1 MiB use up to four range workers by default (configurable from 1 to 16 per CLI or queue session) after a successful strong-ETag range probe. Each response is validated before offset writes; unsupported servers fall back to sequential. Completed chunks are hashed, synchronized, and checkpointed independently. Recovery validates each saved chunk and the remote representation, then downloads missing ranges with the configured connection limit.
- Queue workers send bounded, nonblocking live progress updates to the queue loop; `inspect ID` reports local checkpoint status and recovery guidance without contacting the server. Progress updates can be dropped when the display is busy.
- Queue `restart ID` and `discard ID` explicitly remove verified partials under job ownership; cleanup intent is persisted before deletion so the same command can finish after interruption. New partials record a Linux inode identity and random user extended-attribute marker; older or unmarked files are not deleted automatically. Expected checksums survive fresh restart.
- Standalone and queue downloads use connect, TLS, response-header, and body-read inactivity timeouts. Selected transient HTTP and network failures get up to three cancellable retries; a valid Retry-After up to one minute is honored, and a longer value stops automatic retries. Partial data is retried only through validated resume.
- Linux parallel storage tests exercise real data and checkpoint ENOSPC and permission errors during fresh transfers and recovery. They check saved chunks and retry successfully after restoring space or permissions, including through the queue.
- Queue integration tests kill and restart the queue twice while chunks are saved out of order, inject transient probe and range failures, and verify saved chunks, limits, explicit restart, and backoff cancellation.
- Linux process-kill tests cover checkpoint data write, sync and publication, plus the points before and after final linking and partial-file removal. A completed-publication checkpoint allows explicit `-resume` to verify and reconcile these final-publication states without overwriting conflicting files.
- Resume uses validated ranges with a strong ETag, or verifies the saved prefix again against a single known-length response when no strong validator is available. The latter rereads the prefix and does not authenticate content without a trusted checksum. Recovery-window retries and additional platform verification remain future steps.
- README.md documents the actual prototype, usage, verification commands, and limitations. Local Git metadata now contains commit 195d06bef804. Remote publication has not been verified. The current .gitignore covers debugger binaries and the default debug download.
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
| Parallel track | Desktop GUI | Fyne prototype uses the existing CLI; evolve shared engine access and queue controls incrementally |
| Later | Browser integration and adaptive network scheduling | Separate feature plans built around the existing engine |

Release labels express order, not fixed dates. Estimate implementation effort after the initial decisions and sequential download prototype.

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

These are deferred features. Do not claim complete IDM feature parity or guaranteed download acceleration.

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

The Fyne desktop app is a separate Go module and starts the existing CLI as a companion process. Its Queue tab connects to the same exclusively owned persistent CLI queue; the separate Downloads tab uses standalone transfers. The CLI remains the sole owner of transfer, checkpoint, and queue behavior; a future shared engine package can replace the process boundary when needed. Desktop speed settings are persisted in a private JSON file that companion processes read during transfers; the default applies per download, individual ceilings may be lower, and the separate queue-wide cap remains available.

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
- [ ] Define the full job lifecycle and the owner of each mutable state field. Version-two checkpoints persist sequential states and version-three checkpoints persist parallel chunks; the queue loop owns the basic persisted queue states, while additional lifecycle details remain open.
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
| Resume | Revalidate the remote representation and local partial file before reusing saved bytes. Queue the job subject to concurrency limits. Resume only with supported ranges and a suitable strong validator; otherwise explain that a fresh download is needed. |
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

## 16. Later work - Additional platforms and desktop integration

### v0.3: Broader interface support

- [ ] Implement the remaining OS adapters without duplicating the engine.
- [ ] Test interface binding, address changes, and failover on native systems.
- [ ] Publish a per-platform capability and privilege matrix.

### Desktop GUI (parallel track)

The operator selected a Go desktop app using Fyne on 2026-11-05. The single-download UI includes persistent history, checkpoint resume, and explicit duplicate-file choices. A separate Queue tab manages the persistent CLI queue with configurable concurrency and per-job controls.

- [ ] Compare a Go GUI toolkit, a web-based desktop shell, and a local browser UI.
- [ ] Evaluate accessibility, packaging, native dependencies, and maintenance.
- [ ] Select a framework after a small proof of concept.
- [ ] Reuse engine operations and progress events rather than moving logic into UI callbacks.
- [ ] Add queue controls, destination selection, progress, errors, and recovery views.
- [ ] Plan native packaging, signing, and distribution separately.

### Optional extensions

- Adaptive interface scheduling and user-defined metered-network limits.
- Bandwidth caps, scheduled queues, and completion notifications.
- Browser integration.
- Authenticated downloads with a reviewed secret-handling design.
- A background service or local control API if a concrete workflow requires it.

## 17. Completion order and portfolio evidence

| Step | Visible result | Engineering skill demonstrated |
| --- | --- | --- |
| 1 | Correct sequential downloader | HTTP, streaming I/O, error handling, and cancellation |
| 2 | Safe parallel downloader | Goroutines, synchronization, worker pools, and backpressure |
| 3 | Pause and crash recovery | Persistence, lifecycle design, and durability reasoning |
| 4 | Usable queue and CLI | Component boundaries, scheduling, and understandable behavior |
| 5 | Verified v0.1 release | Cross-platform testing, fault injection, documentation, and delivery |
| 6 | Multi-interface prototype | Socket programming, routing, platform adapters, and measurement |
| 7 | Desktop application | Reuse of the engine through another interface |

Prefer evidence over broad claims. Show reproducible scenarios, failure handling, design tradeoffs, and measured behavior. Keep the engine understandable enough that another developer can review its correctness.

## 18. Immediate next work

Complete the remaining v0.1 release checks, including release-environment verification and broader resource-bound testing.

Develop the Fyne desktop interface alongside the CLI, starting on Linux/amd64; keep multi-interface work and native support claims gated by their own tests.
