# FluxGet 1.0.0

FluxGet's first numbered desktop release for Linux and Windows on 64-bit Intel/AMD computers.

- One Downloads view with pause, resume, retry, speed limits, and progress for each file part.
- Automatic mode adjusts file admission and connections; Manual mode is available in Settings.
- Browser integration for public GET downloads, query/CDN links, exclusions, and Ctrl+left-click browser bypass.
- Windows file ownership, checkpoint recovery, parallel transfers, and private named-pipe browser bridge.

## Install

**Windows 10/11 (64-bit):** Download the Windows ZIP, extract the whole folder, close any running FluxGet instance, and double-click `Install.cmd`. This installs for your user without administrator access and creates FluxGet shortcuts. Files and partial downloads must be on NTFS. Windows packages are unsigned.

**Linux (64-bit):** Download and extract the Linux TAR.GZ, close FluxGet, then run `sh install.sh` inside the extracted folder. Python 3 is needed for setup. Open FluxGet from the applications menu. Packages are built on Ubuntu 22.04; other distributions need compatible glibc and OpenGL/X11 or Wayland libraries. Headless systems can use the CLI without desktop graphics libraries.

Both packages keep the engine, desktop app, and native hosts together. Uninstall helpers remove owned browser registrations/shortcuts and keep downloads and settings.

## Connect a browser

The installer registers Chrome and Firefox native hosts. Extensions are **development packages**, not published store extensions: load the matching folder under the installed `extensions` directory and approve its permissions. See its README for browser-specific instructions. Firefox temporary add-ons disappear when Firefox closes; normal persistent Firefox installation needs a signed add-on. Store distribution and a graphical browser setup wizard are future work.

Enable **Settings → Browser downloads** in FluxGet, enable the browser, then enable the profile in the extension popup. Grant website access once and reload the website. Normal clicks use your configured capture mode; Ctrl+left-click keeps a supported download in the browser.

## Testing boundaries

Only explicit version tags such as v1.0.0 build and publish packages, after both native jobs pass. Branch pushes do not publish packages. Existing releases are never overwritten. The release workflow requires Linux suites and native Windows engine/IPC/desktop companion regressions before publishing either package. Windows browser capture, Firefox Snap/Flatpak portals, Opera, and other Linux distributions still require hands-on acceptance. Opera files are included for development but Windows setup does not register Opera until native-host discovery is verified. Chrome capture has native Linux evidence.

Automatic CPU/memory/storage observations are Linux-specific; unavailable Windows observations remain unknown. Automatic still uses transfer measurements and request budgets on Windows.

Only public direct HTTP/HTTPS downloads are supported. Login cookies, authorization headers, POST downloads, private browsing and uncertain requests stay in the browser. Resume requires valid saved progress and server support. Metadata retains complete source URLs, including query parameters; diagnostics redact URLs. Power-loss recovery and expired CDN links are not guaranteed.
