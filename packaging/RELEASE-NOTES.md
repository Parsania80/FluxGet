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
