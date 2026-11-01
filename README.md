# FluxGet

A Go download manager focused on reliable file transfers, with parallel downloads and multi-interface support planned for later milestones.

**Status:** Early CLI prototype. Sequential downloads are implemented. One live download has been reported successful on Ubuntu; automated verification and native testing on other operating systems are still pending.

## Current features

- Download a direct HTTP or HTTPS URL to a user-selected destination.
- Stream response bytes to disk without holding the entire file in memory.
- Reject unsuccessful or unexpected HTTP responses.
- Preserve partial output when a transfer fails or is canceled with Ctrl+C.
- Refuse to overwrite an existing destination or partial file.
- Detect truncated responses when the server supplies a content length.
- Publish completed output without replacing a destination created during transfer.

## Requirements

- Go 1.26 or later. The current development toolchain is Go 1.26.8 on Linux/amd64.
- A writable destination directory that already exists.
- A filesystem supporting hard links for final output publication.

Windows, macOS, and Linux are the intended targets. Current runtime evidence is limited to Ubuntu 26.04 on amd64; cross-platform support has not yet been verified.

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
| -h | Show help |

Successful execution reports the number of bytes written and the destination. Ctrl+C cancels an active request.

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
    -> synchronization and close
    -> no-overwrite publication as OUTPUT
```

The output is first written to an exclusively created .part file. After transfer completion, FluxGet synchronizes and closes the file, creates a hard link at the final destination, and removes the partial name. If publication is unsupported or the final destination already exists, it reports an error and keeps the partial file.

Retained partial bytes are not yet resumable through the CLI. Existing partial files are refused so that a new invocation cannot silently truncate earlier work.

| File | Responsibility |
| --- | --- |
| main.go | CLI input, cancellation, messages, and exit codes |
| download.go | Sequential transfer and file handling |
| download_test.go | Tests using local HTTP servers |
| go.mod | Module identity and Go baseline |
| project.md | Development roadmap and release boundaries |

## Development checks

Run these from the repository directory:

```bash
clear
gofmt -w main.go download.go download_test.go &&
go test -race ./... &&
go vet ./...
```

The tests cover binary and empty files, rejected HTTP statuses, truncated responses, existing files, competing destination creation, and cancellation after data has been written. The race detector needs a supported platform and a suitable C compiler. Test requests use local servers rather than internet services.

## Current limitations

- No pause/resume, recovery retry window, automatic retries, or persistent queue yet.
- No parallel range requests or simultaneous network-interface use yet.
- No progress display, expected-checksum verification, GUI, or browser integration yet.
- Requests support cancellation but have no application-configured read-inactivity deadline.
- Public direct URLs are the intended input; credential-bearing links and authenticated website workflows are outside the current scope.
- Final publication requires hard-link support. Power-loss recovery is not guaranteed.

## Roadmap

See [project.md](project.md) for the full plan. The next milestones focus on safe parallel transfers, validated resume and recovery, queue management, and native platform testing. Multi-interface downloading and a desktop interface follow those foundations.

## License

A license has not yet been selected. License selection remains an open project decision.
