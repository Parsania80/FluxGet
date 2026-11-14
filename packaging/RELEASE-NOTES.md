# FluxGet 2.0.1

- **Browser setup.** The setup guide shows where to load the bundled extension. Copy the folder path into the browser's file picker, or open it in Explorer on Windows or the file manager on Linux. Connected profile changes show up in FluxGet, and disconnected profiles can reconnect from the extension popup.
- **YouTube setup.** When tools are missing, choose **Install** to set them up and continue analyzing the same link, or **Later** to defer installation.
- **Appearance.** Choose **Light**, **Dark**, or **Follow system** in **Settings → General** and save your preference.

Close FluxGet before upgrading. On Windows, run `fluxget-2.0.1-windows-amd64-setup.exe`. On Debian or Ubuntu, run `sudo apt install ./fluxget_2.0.1_amd64.deb`. If you already loaded the Chrome extension from the installed folder, reload it at `chrome://extensions`.

Earlier versions and their release notes are available on the [releases page](https://github.com/Parsania80/FluxGet/releases).

---

# FluxGet 2.0.0

YouTube downloads, optional proxy routing and automatic download folders for Linux and Windows on 64-bit Intel/AMD computers.

- Download public YouTube videos, Shorts and selected entries from finite playlists. Choose available video/audio formats and quality; merge video/audio or convert audio to MP3. Jobs appear in Downloads with the usual pause, resume, cancel and speed controls.
- Sort new files into Music, Videos, Pictures, Documents, Archives, Programs and Other inside your chosen download folder. Change the default in General settings or turn it off per download. Existing files stay in place and are never silently overwritten.
- Use an HTTP, HTTPS or SOCKS5 proxy for selected downloads, YouTube analysis and first-use tool setup. Direct ignores proxy environment variables; a proxy failure never silently changes the route.
- Improved recovery guidance and no fixed 30-minute cutoff for media transfers. Network timeouts still apply.

## Install or upgrade

Close FluxGet first. Windows users run `fluxget-2.0.0-windows-amd64-setup.exe`; Debian/Ubuntu users install `fluxget_2.0.0_amd64.deb` with `sudo apt install ./fluxget_2.0.0_amd64.deb`. Settings, queued jobs and downloads are kept. Sorting defaults to enabled when no preference was saved; it applies only to new jobs.

Windows requires Windows 10 1809 or newer/11, amd64 and NTFS for downloads and saved progress. Packages are unsigned. Linux packages are built on Ubuntu 22.04, require glibc 2.35 or newer and the declared graphics libraries, and target Debian-family distributions. macOS and ARM64 are not included.

## First YouTube download

Open **YouTube → Media tools → Set up YouTube tools**, then confirm the download. Internet access and about 700 MB of free space are required. The selected proxy is used. Setup downloads pinned upstream yt-dlp, Deno and FFmpeg executables, verifies SHA-256 and makes the complete set available together. No Python or terminal is needed. A canceled or failed setup can be retried; existing tools and downloads are kept.

Third-party executables are downloaded directly from upstream after consent, rather than included in the installer. Their separate licenses apply; see the installed `MEDIA-TOOLS-NOTICES.md`. Tool sets are retained in your user cache, including after uninstall, for saved jobs. Clearing that cache can require setup again. Tool updates ship through tested FluxGet releases, with no extractor self-updater.

Paste a public link and choose **Analyze**. For playlists, choose **Choose from playlist**, select entries and add downloads. Unavailable formats, private/login-required videos, DRM and live streams are outside this release. No cookies or login credentials are imported. YouTube can change independently; availability and resumability are not guaranteed.

## Browser setup and acceptance boundaries

Installers register Chrome/Firefox native hosts and include development extension folders with stable IDs. Load the matching folder and approve permissions; Firefox persistent installation requires a signed extension. Store distribution, a browser setup wizard, native Windows capture, Firefox Snap/Flatpak and Opera acceptance remain open. Windows setup does not register Opera.

Both native installer jobs must pass before either release asset is published. Release-preparation pull requests rehearse packaging without publishing; publication requires the matching version tag. Checks include installed-engine tool setup, pinned versions/EJS, generated MP4 merge and MP3 conversion through direct/proxy routes, plus engine and desktop regressions. Public-media acceptance has Linux evidence; broader long-duration/storage-pressure acceptance remains open. Linux-specific Automatic resource observations are unavailable on Windows.

Resume requires valid saved progress and server support. Metadata stores full source URLs, including query parameters, while diagnostics redact them. Expired source links and power-loss recovery are not guaranteed. Uninstall keeps user downloads, settings and tools.

---

## Previous release notes

# FluxGet 1.0.0

FluxGet's first numbered desktop release for Linux and Windows on 64-bit Intel/AMD computers.

- One Downloads view with pause, resume, retry, speed limits, and progress for each file part.
- Automatic mode adjusts file admission and connections; Manual mode is available in Settings.
- Browser integration for public GET downloads, query/CDN links, exclusions, and Ctrl+left-click browser bypass.
- Windows file ownership, checkpoint recovery, parallel transfers, and private named-pipe browser bridge.

## Install

**Windows 10 (1809 or newer)/11 (64-bit):** Download `fluxget-1.0.0-windows-amd64-setup.exe`, close FluxGet, and run the setup wizard. It installs for your user without administrator access, creates shortcuts and adds an uninstall entry. Files and partial downloads must be on NTFS. Windows packages are unsigned.

**Debian/Ubuntu Linux (64-bit):** Download `fluxget_1.0.0_amd64.deb`, close FluxGet, then run `sudo dpkg -i fluxget_1.0.0_amd64.deb`. If dependencies are missing, use `sudo apt install ./fluxget_1.0.0_amd64.deb`. This installs system-wide under `/opt/fluxget` with application-menu and CLI launchers; installation needs administrator access. Packages are built on Ubuntu 22.04 and require glibc 2.35 or newer and the declared graphics libraries. This package is for Debian-family distributions; RPM/Arch packages are not included.

Both installers keep the engine, desktop app, and native hosts together. Windows can be removed through Installed apps; Linux uses `sudo dpkg -r fluxget`. Uninstall removes installed application files and owned registrations/shortcuts while keeping user downloads and settings.

## Connect a browser

The installers register Chrome and Firefox native hosts (system-wide on Debian/Ubuntu, for the current user on Windows). Extensions are **development packages**, not published store extensions: load the matching folder under `/opt/fluxget/extensions` on Linux or `%LOCALAPPDATA%\Programs\FluxGet\extensions` on Windows and approve its permissions. See its README for browser-specific instructions. Firefox temporary add-ons disappear when Firefox closes; normal persistent Firefox installation needs a signed add-on. Store distribution and a graphical browser setup wizard are future work.

If upgrading from a Linux development preview, remove its per-user native-host registrations first using its uninstall helper; otherwise those old registrations can override the system package.

Enable **Settings → Browser downloads** in FluxGet, enable the browser, then enable the profile in the extension popup. Grant website access once and reload the website. Normal clicks use your configured capture mode; Ctrl+left-click keeps a supported download in the browser.

## Testing boundaries

Only explicit version tags such as v1.0.0 build and publish packages, after both native jobs pass. Branch pushes do not publish packages. Existing releases are never overwritten. The release workflow requires Linux suites and native Windows engine/IPC/desktop companion regressions before publishing either package. Windows browser capture, Firefox Snap/Flatpak portals, Opera, and other Linux distributions still require hands-on acceptance. Opera files are included for development but Windows setup does not register Opera until native-host discovery is verified. Chrome capture has native Linux evidence.

Automatic CPU/memory/storage observations are Linux-specific; unavailable Windows observations remain unknown. Automatic still uses transfer measurements and request budgets on Windows.

Only public direct HTTP/HTTPS downloads are supported. Login cookies, authorization headers, POST downloads, private browsing and uncertain requests stay in the browser. Resume requires valid saved progress and server support. Metadata retains complete source URLs, including query parameters; diagnostics redact URLs. Power-loss recovery and expired CDN links are not guaranteed.
