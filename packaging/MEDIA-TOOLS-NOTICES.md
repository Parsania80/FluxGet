# YouTube tool notices

FluxGet installers contain FluxGet and a checksum-locked setup helper. After you confirm **YouTube → Media tools → Set up YouTube tools**, the helper downloads the following original upstream executables directly from their publishers. They are separate programs with their own licenses; FluxGet's MIT license does not replace those licenses.

| Component | Pinned version | Upstream licensing and source |
|---|---|---|
| yt-dlp, including local EJS | 2026.10.26; EJS 0.8.0 | [yt-dlp licensing](https://github.com/yt-dlp/yt-dlp#licensing): source is Unlicense; standalone executables include dependencies under GPLv3+ and other licenses. [Release and source](https://github.com/yt-dlp/yt-dlp/releases/tag/2026.10.26). |
| Deno | 2.9.8 | [MIT license and dependency notices](https://github.com/denoland/deno/blob/v2.9.8/LICENSE.md). [Release and source](https://github.com/denoland/deno/releases/tag/v2.9.8). |
| FFmpeg and ffprobe | N-128757-g4b42530309, GPL build | [FFmpeg licensing](https://ffmpeg.org/legal.html); the selected upstream artifacts are GPL builds, including enabled third-party components. [Build configuration and sources](https://github.com/yt-dlp/FFmpeg-Builds), [pinned release](https://github.com/yt-dlp/FFmpeg-Builds/releases/tag/autobuild-2026-11-10-20-59). |

Exact URLs and SHA-256 values for Linux/amd64 and Windows/amd64 are recorded in [media/tools.lock.json](https://github.com/Parsania80/FluxGet/blob/main/media/tools.lock.json). These executables are downloaded from upstream, not redistributed inside FluxGet installers. Anyone redistributing them separately must review the selected artifacts' licenses, source requirements and notices.

Setup requires internet access and about 700 MB of free space. It uses the selected proxy or an explicit direct connection. Incomplete downloads are discarded; tools become available together after their checksums pass. Setup never invokes a tool's self-updater. New pinned tool versions require a tested FluxGet release.

Tools live under your operating system's user cache directory, in `fluxget/media-tools/<tool-set-id>`. Existing tool sets are retained so saved downloads can still use them. Uninstalling FluxGet keeps this cache, your settings and your downloads. Removing a tool set or clearing the cache can require setup again and can prevent saved media jobs from resuming with their original tools.
