# FluxGet

A Go download manager focused on reliable file transfers, with parallel downloads and multi-interface support planned for later milestones.
The long-term goal is an IDM-like download manager, starting with a reliable Linux download engine.

**Status:** Early CLI prototype. Sequential downloads are implemented. One live download has been reported successful on Ubuntu; automated checks passed on Linux with Go 1.26.8. Native testing on other operating systems is still pending.

The next development step is to verify remaining storage failure cases and complete the recovery and queue design before adding parallel transfers.
Initial limits for future concurrency, timeouts, and retries are documented in the roadmap; the current prototype does not yet enforce them.
An active-download limit concerns simultaneous transfers, not the number of files a user can add to a queue.
The planned queue will default to five simultaneous downloads, with user controls for that limit and for connections per file; users will be able to choose ten simultaneous downloads.
The planned recovery behavior preserves partial files by default and requires an explicit resume after restarting the application.
On Linux, interrupted sequential downloads can be resumed when the server provides a strong ETag, a known length, and safe byte ranges. Checkpoints are written during eligible transfers and on interruption.
Phase 0 decisions include the license, supported platforms, and resource limits.
Linux tests cover full-disk, permission, checkpoint-replacement, and final-publication process crashes; other platforms and power loss still need verification.
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
- Show bytes and average speed during transfers, plus a percentage when the size is known.
- Optionally verify a supplied SHA-256 checksum before publishing the output.
- Resume eligible interrupted sequential downloads on Linux after validating the saved prefix and remote responses.
- Publish completed output without replacing a destination created during transfer.

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

Both flags are required:

| Flag | Meaning |
| --- | --- |
| -url | A direct file URL with an explicit http:// or https:// scheme |
| -out | The output path; neither that path nor its .part companion may already exist |
| -sha256 | Optional expected SHA-256 checksum as 64 hexadecimal characters |
| -resume | Resume a checkpointed download using `-out`; do not provide `-url` |
| -h | Show help |

Progress is written to standard error as data arrives; successful execution reports the number of bytes written and the destination on standard output. Ctrl+C cancels an active request. A checksum mismatch leaves the `.part` file for inspection and does not publish the destination.
Use a checksum from a trusted source: it verifies that the downloaded bytes match the expected file.
Most downloads can be started without a checksum; the `-sha256` flag is optional.

To resume an eligible interrupted download from the same output path:

```bash
go run . -resume -out "go-releases.json"
```

Linux checkpoints are stored in the FluxGet jobs directory under the user configuration directory and contain the source URL. Do not use secret-bearing URLs. Resume requires the saved partial file, an unchanged resolved URL and strong ETag, a known size, and valid range responses. The original `-sha256`, if supplied, is retained. If these checks fail, the partial file is kept and no final file is published.

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

The tests cover binary and empty files, known and unknown lengths, redirects and TLS, checksums, progress, rejected HTTP statuses, truncated responses, partial writes, existing files, competing destination creation, cancellation, disk-full and permission errors, and failed checkpoint updates. On Linux the disk-full test uses a private 1 MiB tmpfs and skips if user or mount namespaces are unavailable; permission tests skip when run as root. The race detector needs a supported platform and a suitable C compiler. Test requests use local servers rather than internet services.

## Current limitations

- No interactive pause, recovery retry window, automatic retries, or persistent queue yet. Resume is limited to eligible sequential Linux downloads.
- Abrupt termination before the first 1 MiB checkpoint may leave a partial file without a checkpoint; power-loss recovery is not guaranteed.
- Final-publication recovery requires an explicit `-resume`; legacy checkpoints without a completed-publication record require manual inspection.
- No parallel range requests or simultaneous network-interface use yet.
- No GUI or browser integration yet.
- Progress updates occur when data is written, so a stalled connection does not produce regular updates.
- Requests support cancellation but have no application-configured read-inactivity deadline.
- Public direct URLs are the intended input; credential-bearing links and authenticated website workflows are outside the current scope.
- Final publication requires hard-link support. Power-loss recovery is not guaranteed.

## Roadmap

See [project.md](project.md) for the full plan. The next milestones focus on safe parallel transfers, validated resume and recovery, queue management, and native platform testing. Multi-interface downloading and a desktop interface follow those foundations.

## License

FluxGet is open source under the [MIT License](LICENSE).
