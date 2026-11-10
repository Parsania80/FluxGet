# FluxGet

<img src="desktop/assets/fluxget.png" alt="FluxGet icon" width="96">

FluxGet is a download manager written in Go. It helps you manage several downloads, control their speed, and continue saved downloads after an interruption when the server allows it.

**Status:** Early Linux desktop and command-line prototypes. Linux/amd64 is the first target. Windows and macOS builds have not been tested natively.

## What you can do

- Add a direct HTTP or HTTPS file link and download now or later.
- Pause, resume, retry, and manage several downloads in one list.
- See each download's speed, progress, and individual file parts.
- Set speed limits and remove a custom limit with one click.
- Select several downloads for shared actions or confirmed file deletion.
- Let Automatic mode adjust downloads and connections within your limits.

On Linux, Automatic also watches CPU, memory, and storage pressure and reduces work when needed. Manual controls are available in Settings. Browser download capture and using Wi-Fi and Ethernet together are planned.

## Build and open the desktop app

You need **Go 1.26.8 or later**, a C compiler, and the Linux graphics development libraries required by [Fyne](https://docs.fyne.io/started/quick/).

From the repository folder:

```bash
mkdir -p dist
go build -o dist/fluxget .
(cd desktop && go build -o ../dist/fluxget-gui .)
./dist/fluxget-gui
```

Keep `fluxget` and `fluxget-gui` together in the same folder.

## Download a file

1. Choose **Add download** and paste a direct file link.
2. Choose a folder and filename if needed.
3. Select **Download now**, or **Download later** to leave it paused.
4. Select a download to pause, resume, retry, or open its folder.

Extra downloads wait for a free slot. After reopening FluxGet, unfinished downloads stay paused until you resume them.

Use **Settings** to change the download folder, speed limits, or display units. Sizes and speeds use **MB** by default; you can choose **KB**, **MB**, or **GB**. Open **Customize download management** to set the maximum downloads and connections, or switch to Manual.

Removing an item from the list keeps its files. Deleting files is a separate action that requires confirmation.

## Use the command line

Build the CLI with `go build -o dist/fluxget .`, then run:

```bash
./dist/fluxget -url "https://example.com/file.zip"
```

Replace the example link with a direct file URL. Files go to your Downloads folder unless you choose another location:

```bash
./dist/fluxget -url "https://example.com/file.zip" -out "/path/to/file.zip"
./dist/fluxget -resume -out "/path/to/file.zip"
./dist/fluxget -h
```

Resume uses the same output path and requires a valid saved checkpoint. The standalone CLI uses Manual concurrency by default.

## Before you use it

- Intended for public direct links. Website logins and authenticated browser downloads are not supported.
- Resume depends on the saved data and server response; some downloads need a fresh start.
- New downloads refuse existing output files. Incomplete data is kept after failures; final publication requires filesystem hard-link support.
- Download metadata contains source URLs. Avoid links containing passwords or private tokens.
- Power-loss recovery is not guaranteed. Full release and interactive desktop acceptance are still in progress.

## Development

The engine and desktop are separate Go modules. Run checks in both:

```bash
go test -race ./... && go vet ./...
(cd desktop && go test -race ./... && go vet ./...)
```

See [project.md](project.md) for architecture, technical details, and next steps; [BENCHMARKS.md](BENCHMARKS.md) for recorded measurements; and [SESSION-LOG.md](SESSION-LOG.md) for development history.

## License

[MIT](LICENSE).
