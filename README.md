# FluxGet

A Go download manager focused on reliable file transfers, with multi-interface support planned for a later milestone.
The long-term goal is an IDM-like download manager, starting with a reliable Linux download engine.

**Status:** Early CLI prototype. Sequential and parallel range downloads are implemented on Linux. One live download has been reported successful on Ubuntu; automated checks passed on Linux with Go 1.26.8. Native testing on other operating systems is still pending.

The next development step is to verify the first hosted CI run and complete the remaining v0.1 release checks. Local formatting, vet, race tests, and Linux/Windows/macOS builds passed. A local Linux/amd64 build has downloaded and verified a real 30.9 MB file over the internet to ext4; see the roadmap for the test's limits.
The queue enforces an active-download limit; downloads also use bounded timeouts and retries. Other planned resource limits are documented in the roadmap.
An active-download limit concerns simultaneous transfers, not the number of files a user can add to a queue.
The queue defaults to five simultaneous downloads and supports a configurable limit, including ten. Eligible new downloads use up to four connections by default.
The planned recovery behavior preserves partial files by default and requires an explicit resume after restarting the application.
The CLI can list saved jobs, resume a known output path, and manage a persistent queue. Transfers do not start automatically when the queue is opened.
On Linux, interrupted downloads can be resumed when the server provides a strong ETag, a known length, and safe byte ranges. Parallel checkpoints record each fully verified chunk independently.
Phase 0 decisions include the license, supported platforms, and resource limits.
Linux tests cover full-disk and permission failures, repeated queue process crashes and retries, checkpoint-replacement failures, and final-publication process crashes; other platforms and power loss still need verification.
Go 1.26.8 is installed for the current Linux user under `~/.local/share/go1.26.8`, with commands in `~/.local/bin`.
For learning-focused development, activate VibeWise with `$vibe-wise:learn` in Codex.

## Current features

- Download a direct HTTP or HTTPS URL to a user-selected destination.
- Stream response bytes to disk without holding the entire file in memory.
- Reject unsuccessful or unexpected HTTP responses.
- Follow up to nine redirects and reject redirects from HTTPS to HTTP.
- Preserve partial output when a transfer fails or is canceled with Ctrl+C.
- Refuse to overwrite an existing destination or partial file.
- Detect truncated responses when the server supplies a content length.
- Show transfer progress; sequential transfers also report average speed.
- Optionally verify a supplied SHA-256 checksum before publishing the output.
- Download eligible files with parallel ranges on Linux; fall back to sequential transfers when range support or a strong validator is unavailable.
- Resume eligible interrupted downloads on Linux after validating saved data and the remote representation.
- List saved Linux jobs and distinguish active, interrupted, and final-publication recovery states.
- Publish completed output without replacing a destination created during transfer.
- Manage a Linux queue with add, import, list, start, pause, resume, cancel, retry, restart, discard, and remove commands.
- Retry selected temporary HTTP and network failures, with cancellable backoff and safe checkpoint validation.

## Requirements

- Go 1.26 or later. The current development toolchain is Go 1.26.8 on Linux/amd64.
- A writable destination directory that already exists.
- A filesystem supporting hard links for final output publication.
- On Linux, a writable user configuration directory for job locks and checkpoints.

The first release targets Linux/amd64. The shared code should remain portable for later ARM64, Windows, and macOS support, but those targets require native testing before support is claimed. Current runtime evidence is limited to Ubuntu 26.04 on amd64.

## Quick start

Clone the repository, enter its directory, and inspect the CLI:

```bash
clear
git clone https://github.com/Parsania80/FluxGet.git &&
cd FluxGet &&
go run . -h
```

From the repository directory, download the public Go release index:

```bash
clear
go run . -url "https://go.dev/dl/?mode=json" -out "go-releases.json"
```

New downloads require `-url` and `-out`:

| Flag | Meaning |
| --- | --- |
| -url | A direct file URL with an explicit http:// or https:// scheme |
| -out | The output path; neither that path nor its .part companion may already exist |
| -sha256 | Optional expected SHA-256 checksum as 64 hexadecimal characters |
| -resume | Resume a checkpointed download using `-out`; do not provide `-url` |
| -list | List saved jobs; use without other flags |
| -queue | Open the interactive Linux queue |
| -max-active | Maximum simultaneous queue downloads, 1–64 (default 5); use with `-queue` |
| -connections | Maximum connections per download, 1–16 (default 4); applies to the current CLI or queue session |
| -h | Show help |

Progress is written to standard error as data arrives; successful execution reports the number of bytes written and the destination on standard output. Ctrl+C cancels an active request. A checksum mismatch leaves the `.part` file for inspection and does not publish the destination.
Use a checksum from a trusted source: it verifies that the downloaded bytes match the expected file.
Most downloads can be started without a checksum; the `-sha256` flag is optional.

On Linux, files of at least 1 MiB use parallel ranges when the server proves support with a strong ETag and a valid range response. Otherwise, FluxGet uses one sequential connection. Set `-connections 1` to disable parallel ranges. The limit is separate from `-max-active` and applies to new and resumed jobs in a queue session. Older sequential checkpoints resume with one connection.

To manage several downloads in one process:

```bash
go run . -queue -max-active 10
```

At the queue prompt, enter `add URL OUTPUT` for each file, then `start`. Enter `list` to see jobs and `inspect ID` to see local recovery details; `pause ID`, `resume ID`, `cancel ID`, and `retry ID` change a job, and `quit` stops active work before exiting. `import OUTPUT` adds an existing saved job in the paused state. `remove ID` removes an inactive job from the queue only; it leaves its files and checkpoints untouched. Output paths may contain spaces. Enter `help` for the command list. An active queue owns its metadata exclusively, so only one queue process can run at a time.

For a paused, failed, or canceled job, `restart ID` explicitly deletes its verified partial file and checkpoint, then queues a new download. `discard ID` deletes those files and leaves the job canceled. If the queue is already started, a restarted job can run immediately. Both commands preserve an existing final destination and reject active jobs. Any expected checksum saved with the job is retained.

Cleanup requires a matching checkpoint, file identity, any saved-data hashes, and a random ownership marker stored in a Linux user extended attribute. New downloads without extended-attribute or `/proc/self/fd` support can still download and resume, but cannot use automatic partial-file cleanup. Older checkpoints without the marker also require manual inspection. If cleanup is interrupted, the job shows `resetting`; repeat the same command to finish it. Locks coordinate FluxGet processes, but unrelated processes can still race filesystem changes.

Queued and completed jobs persist across sessions. After a crash, previously running jobs return paused; use `resume ID` and `start` to continue them. A paused job may need a fresh download if the server does not support safe resume. No files are removed automatically to make a retry possible. Queue metadata contains URLs, so do not use secret-bearing links. Queue transfers display live progress, and `inspect ID` reports saved checkpoint bytes, completed chunks, and recovery guidance. Progress updates may be dropped when the display is busy. Queue jobs do not yet have a checksum input; standalone downloads support one. A crash after publishing a file but before recording completion can require manual inspection.

To see saved jobs and their output paths:

```bash
go run . -list
```

The listing is a read-only snapshot. `active` means the job lock is held; `interrupted` means the saved job is not active; `ready-to-reconcile` means final publication needs review; and `needs-attention` identifies missing, invalid or conflicting local records. `interrupted (restart required)` means the saved checkpoint cannot be resumed under the current rules. Byte counts reflect the last checkpoint, not live progress. Resume still rechecks local and remote data before using any bytes. Completed jobs are removed from this listing.

To resume an eligible interrupted download from the same output path:

```bash
go run . -resume -out "go-releases.json"
```

Linux checkpoints are stored in the FluxGet jobs directory under the user configuration directory and contain the source URL. Do not use secret-bearing URLs. Resume checks the saved partial file and, when data remains to download, requires an unchanged resolved URL and strong ETag, a known size, and valid range responses. Parallel recovery verifies each completed chunk and downloads only missing chunks. The original `-sha256`, if supplied, is retained. If these checks fail, the partial file is kept and no final file is published.

Downloads and queue jobs retry selected temporary failures up to three times after the initial attempt, waiting 1, 2, and 4 seconds. HTTP 408, 429, 500, 502, 503, and 504 can be retried; a valid `Retry-After` of up to one minute is respected, and longer waits stop automatic retries. Connection and TLS handshake timeouts are 10 seconds, response headers have 30 seconds, and a stalled body read has 60 seconds. A transfer can run longer as long as it keeps receiving data. If saved bytes cannot be resumed safely, retries stop and preserve the partial file.

The same `-resume -out FILE` command can reconcile an interrupted final publication. FluxGet verifies the completed file against its saved publication checkpoint before finishing publication or removing leftover partial metadata. Conflicting or unverifiable files are left untouched. Older checkpoints without a completed-publication record cannot be reconciled automatically.
Completed-publication reconciliation checks local files and does not need to contact the download server.

## Build

On Linux or macOS:

```bash
clear
go build -o fluxget . &&
./fluxget -h
```

On Windows, using PowerShell:

```powershell
Clear-Host
go build -o fluxget.exe .
if ($LASTEXITCODE -eq 0) { .\fluxget.exe -h }
```

## How it works

```text
CLI arguments
    -> request validation
    -> HTTP response checks
    -> streamed writes to OUTPUT.part
    -> optional SHA-256 verification
    -> synchronization and close
    -> no-overwrite publication as OUTPUT
```

The output is first written to an exclusively created .part file. After transfer completion, FluxGet synchronizes and closes the file, creates a hard link at the final destination, and removes the partial name. If publication is unsupported or the final destination already exists, it reports an error and keeps the partial file.

The download function returns the number of bytes written and an error, if one occurred; a retained partial file does not mean the download can be resumed.
It uses Go's `defer` to schedule cleanup when the function returns.

Existing partial files are refused for new downloads; use `-resume` only when an eligible checkpoint exists. Servers without a strong ETag or safe range support cannot be resumed by this prototype.
Git ignores partial downloads, generated binaries, and local VibeWise learning notes.

| File | Responsibility |
| --- | --- |
| main.go | CLI input, cancellation, messages, and exit codes |
| download.go | Sequential transfer and file handling |
| download_test.go | Tests using local HTTP servers |
| checkpoint.go | Checkpoint validation and JSON file storage |
| checkpoint_test.go | Checkpoint storage and failure tests |
| job_lock_linux.go | Linux job and partial-file advisory locking |
| remote_validation.go | Remote representation validation for recovery |
| resume.go | Resume verification, checkpointing, and transfer |
| reconcile.go | Verification and cleanup after an interrupted final publication |
| go.mod | Module identity and Go baseline |
| project.md | Development roadmap and release boundaries |

## Debugging in VS Code

1. Open the repository folder in VS Code.
2. Install the Go extension published by the Go team (golang.go).
3. Open main.go and click beside line 15, on the os.Exit call, to set a breakpoint. A breakpoint pauses execution before that line runs.
4. Open Run and Debug and select **FluxGet: Debug download**.
5. Press F5. Enter a direct HTTP/HTTPS URL and a new output filename when prompted.
6. Allow the Go extension to install Delve if it prompts for the debugger, then start again if necessary.

The checked-in .vscode/launch.json builds the package in the workspace folder and passes the requested URL and output to the app. Relative output paths are resolved inside the repository. The default debug-download.json output and debugger binaries are ignored by Git.

| Key | Action |
| --- | --- |
| F5 | Start debugging, or continue to the next breakpoint |
| F10 | Execute the current line without stepping into its function calls |
| F11 | Step into a function call |
| Shift+F11 | Return from the current function to its caller |
| Shift+F5 | Stop the debug session |

From the first breakpoint, F11 enters run. Use F10 to follow flag setup and parsing. Inspect variables in the Variables panel or by hovering over them. After flags.Parse, the Watch expressions *rawURL and *destination show the supplied strings.

Use a fresh output path for each download. The app refuses an existing output or partial file. Debugger configuration has been checked as JSON; a live VS Code/Delve session still needs to be verified on your machine.

See the [Go extension debugging guide](https://github.com/golang/vscode-go/wiki/debugging) for tool installation and troubleshooting.

## Development checks

Run these from the repository directory:

```bash
clear
gofmt -w *.go &&
go test -race ./... &&
go vet ./...
```

The GitHub Actions workflow checks formatting, vet, race-tested Linux behavior, the Linux build, and Windows/macOS cross-builds on pushes and pull requests. It is configured locally; its first hosted run is not yet verified. Cross-builds do not establish native support.

The tests cover binary and empty files, known and unknown lengths, redirects and TLS, checksums, progress, rejected HTTP statuses, truncated responses, partial writes, existing files, competing destination creation, cancellation, disk-full and permission errors, and failed checkpoint updates. On Linux the disk-full tests use a private 1 MiB tmpfs for both data and checkpoint failures and skip if user or mount namespaces are unavailable; permission tests skip when run as root. The race detector needs a supported platform and a suitable C compiler. Test requests use local servers rather than internet services.

Repeatable local single-download and queue experiments are documented in [BENCHMARKS.md](BENCHMARKS.md), including throughput, memory, goroutines, cancellation, filesystem comparison, and a throttled server. These results are not predictions for real networks.

## Current limitations

- No extended recovery retry window yet. Recovery and verified partial-file cleanup are limited to eligible Linux checkpoints.
- A crash between creating the partial file and saving the initial job record can leave an undiscovered partial file. Parallel recovery may redownload work in an unfinished chunk. Power-loss recovery is not guaranteed.
- Final-publication recovery requires an explicit `-resume`; legacy checkpoints without a completed-publication record require manual inspection.
- No simultaneous network-interface use yet.
- No GUI or browser integration yet.
- Progress updates occur when data is written, so a stalled connection does not produce regular updates.
- Public direct URLs are the intended input; credential-bearing links and authenticated website workflows are outside the current scope.
- Final publication requires hard-link support. Power-loss recovery is not guaranteed.

## Roadmap

See [project.md](project.md) for the full plan. The next milestones focus on parallel-download recovery, queue improvements, and native platform testing. Multi-interface downloading and a desktop interface follow those foundations.

## License

FluxGet is open source under the [MIT License](LICENSE).
