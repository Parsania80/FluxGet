# Recorded workload measurements

These measurements describe one local experiment, not a performance guarantee or a release-wide resource bound.

## Environment and method

- Date: 2026-11-05; Ubuntu 26.04.1 LTS, Linux 7.0.0-38-generic, amd64; Go 1.26.8; `GOMAXPROCS=16`.
- CPU: AMD Ryzen 7 7800X3D; 16 logical CPUs visible. Approximately 29 GiB RAM was available to the host.
- An HTTP server and downloader ran in the same Go test process over loopback. Source and output were in `/tmp`, a tmpfs filesystem. The source was a deterministic 128 MiB file with a strong ETag and byte-range support.
- Five single-transfer samples were run for each connection count, in the order 1, 4, 8. Cache warming, process reuse, and other activity were not controlled. No artificial network latency or bandwidth limit was applied.
- Transfer time covers the download function, including checkpoint writes, synchronization, hashing and publication. Each output was checked against the source SHA-256 after timing. `ns/op` also includes benchmark setup and verification; use the reported `transfer-ms` and `MiB/s` for the transfer itself.
- RSS from `/proc/self/status`, Go heap allocation, and goroutine counts were sampled every 10 ms plus a baseline before the transfer. They include the test harness and local server. Sampling can miss peaks, and process RSS excludes tmpfs storage and other kernel memory.
- Cancellation used a separate stalled local server: each active connection sent 64 KiB and waited. Timing starts at context cancellation and ends when the download returns, including interruption-checkpoint work. Each sample verified no final file was published and an interrupted checkpoint remained.

Reproduce on Linux with sufficient free temporary space:

```bash
go test -run '^$' -bench '^BenchmarkRecorded(Workload|Cancellation)$' -benchtime=1x -count=5 -v ./internal/engine
```

## Results

Each value is the median of five samples. Peak columns are the median of the five sampled peaks, not a guaranteed maximum.

| Connections | Throughput (MiB/s) | Transfer time (ms) | Sampled peak RSS (MiB) | Sampled peak Go heap (MiB) | Sampled peak goroutines |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 934.0 | 137.0 | 14.89 | 2.497 | 9 |
| 4 | 752.2 | 170.2 | 15.99 | 2.720 | 26 |
| 8 | 755.5 | 169.4 | 17.85 | 3.183 | 46 |

The baseline was four goroutines for each sample. Sampled RSS baselines ranged from 11.52 to 15.19 MiB for one connection, 15.03 to 16.17 MiB for four, and 15.47 to 17.69 MiB for eight.

| Connections | Cancellation median (ms) | Cancellation range (ms) |
| --- | ---: | ---: |
| 1 | 0.121 | 0.099–0.145 |
| 4 | 0.124 | 0.094–0.199 |

One connection was faster in this local tmpfs experiment. This does not predict remote-server or real-network performance. The short cancellation measurements do not cover slow storage, a stalled filesystem, large checkpoints, or every failure path. This single workload does not establish asymptotic memory bounds, queue-wide resource use, or production latency.

## Queue, filesystem, and throttled-server experiment

Recorded on 2026-11-05 on the same Ubuntu 26.04.1/Linux amd64, Go 1.26.8, Ryzen 7 7800X3D host. Each run enqueues six 4 MiB files, with four range connections per active job. The HTTP server, queue, sampler, and a shared 4 MiB source buffer run in the same process. Three single-queue samples were run per configuration, in the table's order; caches and other host activity were not controlled.

The fast server uses loopback without artificial throttling. The slow server waits 40 ms before each response and uses a shared ticker to allow at most one 64 KiB data write every 15.625 ms across all requests, approximately 4 MiB/s aggregate. This is an application-level server simulation, not a shaped network link or a model of a particular internet server.

The tmpfs run stores outputs, queue metadata, and checkpoints under `/tmp`. The disk runs store them in a newly created temporary directory under the repository, on ext4 over `/dev/nvme1n1p2`. FluxGet's normal synchronization and checkpoint behavior was left enabled. The ext4 run measures this machine's filesystem and storage stack, including its caches; disk latency was not artificially controlled. The user's systemd I/O controller was not delegated in this environment, so a fixed disk bandwidth limit could not be verified.

Queue time starts before the six `add` commands and ends when polling observes all jobs completed; it includes queue and checkpoint persistence, but excludes the subsequent SHA-256 comparison of every output with the source. RSS, heap, and goroutines are sampled every 10 ms; queue states are polled every 5 ms. The server counts concurrent data-request handlers separately from queue states. Samples can miss short peaks, especially for fast runs. RSS includes the in-process server and source buffer but excludes kernel memory and tmpfs storage.

Reproduce on Linux, choosing a writable directory on the filesystem to measure:

```bash
TMPDIR=/tmp FLUXGET_WORKLOAD_STORAGE_DIR="$PWD" go test -run '^$' -bench '^BenchmarkRecordedQueueWorkload$' -benchtime=1x -count=3 -v ./internal/engine
```

The benchmark creates and removes uniquely named temporary directories in that directory. Confirm that `/tmp` is tmpfs and the chosen directory is on the intended filesystem before comparing them.

| Output and server | Active-job limit | Median queue time | Median aggregate throughput | Median sampled peak RSS | Median sampled peak Go heap | Median sampled peak goroutines |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| tmpfs, fast | 3 | 17.68 ms | 1,357 MiB/s | 24.40 MiB | 8.034 MiB | 31 |
| ext4, fast | 3 | 72.54 ms | 330.9 MiB/s | 23.92 MiB | 7.964 MiB | 41 |
| ext4, throttled | 1 | 6,386 ms | 3.758 MiB/s | 24.96 MiB | 9.005 MiB | 29 |
| ext4, throttled | 3 | 6,078 ms | 3.949 MiB/s | 26.07 MiB | 10.19 MiB | 73 |

All values are medians of three samples. Observed queue state reached the configured active-job limit in every sample, and server-side data requests reached four per job without exceeding either configured limit. Fast tmpfs sampled peak goroutines ranged from 31 to 66, illustrating that the sampler can miss brief peaks.

For a separate throttled/ext4 case, the benchmark requested `pause 1` after partial data appeared while up to three jobs were active. Time until the queue's persisted state was observed as paused had a median of 5.758 ms, range 5.555–5.798 ms. The measurement includes 5 ms polling and queue/checkpoint work; it does not measure a lower bound or every possible slow-filesystem condition. The paused checkpoint was verified and the queue was then stopped.

On this run ext4 was slower than tmpfs for the fast-server queue. Under the shared server cap, three active jobs improved median aggregate throughput by about 5% over one, while the median sampled peak rose from 29 to 73 goroutines. These results do not establish general memory bounds, actual disk bandwidth, or performance over real networks and devices.

## Stage-5 mixed HTTP/media acceptance — 2026-11-13

Initial Linux evidence, not a resource bound. Environment: Ubuntu 26.04.1/amd64, Linux 7.0.0-38-generic, Go 1.26.8; the pinned yt-dlp 2026.10.26/EJS 0.8.0, Deno 2.9.8 and FFmpeg/ffprobe N-128757-g4b42530309. Three samples ran without the race detector. Other verification was running on the host; caches and background load were not controlled.

The isolated queue has a generated three-second 160×90 H.264/AAC MP4 and a 98,304-byte direct HTTP file, with three active-file slots/four workers available and a shared 16,384 B/s ceiling. Discovery uses local metadata; actual yt-dlp, FFmpeg, ffprobe, companion and queue code execute. Both sources use loopback HTTP; output/configuration are temporary under `/tmp` (tmpfs). The test checks that direct requests start after media completion, validates the published media through the production ffprobe path and compares direct bytes exactly. No installed app or user queue is used.

| Sample | Source bytes | Queue time | Average source rate | Reaped child CPU | Cumulative largest-child max RSS |
|---|---:|---:|---:|---:|---:|
| 1 | 174,854 | 11.836 s | 14,773 B/s | 1.163 s | 69,604 KiB |
| 2 | 174,854 | 11.822 s | 14,790 B/s | 1.151 s | 69,828 KiB |
| 3 | 174,854 | 11.826 s | 14,786 B/s | 1.155 s | 70,408 KiB |

The median average rate is 14,786 B/s (90.2% of the ceiling), including extraction/probe/process startup, conversion, validation, persistence and shutdown. Source bytes are known file lengths, not packet accounting. Short-startup overhead makes this unsuitable as a throughput-efficiency benchmark or an instantaneous-burst guarantee.

CPU is the before/after `RUSAGE_CHILDREN` user+system time for reaped children, excluding the Go test/queue process and fixture generation. Max RSS is the cumulative largest-child statistic, not summed concurrent process-tree memory; fixture generation or earlier samples can supply that peak. It excludes the GUI, kernel cache and unobserved transient allocations. No CPU quota, low-memory, slow-storage, long-video or native Windows acceptance is established. The measured storage is tmpfs; no HDD/SSD conclusion follows.

Reproduce using an explicitly prepared pinned tool manifest:

```bash
FLUXGET_MEDIA_TOOLS="$PWD/dist/install-current/media-tools/tools.json" go test -count=3 -v -run '^TestMediaQueueNativeMixedSharedCeiling$' ./internal/engine
```

The other stage-5 native tests cover interrupted FFmpeg merge/MP3 conversion, refreshed links, a real private 1 MiB tmpfs ENOSPC failure/recovery, atomic metadata-save failure and completed-part HTTP 416 with explicit restart. See the evidence matrix in [project.md](project.md#stage-5-recoveryresource-acceptance--in-progress-2026-11-13) for scope and remaining gates.

## Stage-5 long duration and resource pressure — 2026-11-13

Same Linux/Go/pinned-tool versions as the earlier stage-5 experiment. These are bounded local acceptance trials, not a general throughput or memory guarantee. Each transient user service contains only its test process, loopback server and owned tools; native binaries are compiled before entering the cgroup. No installed app or user data is involved. Swap is disabled inside these scopes; service runtime/stop limits are enforced externally.

| Trial | Enforced limits | Result |
|---|---|---|
| One-hour media | CPU 0.5; memory.high 384 MiB / memory.max 512 MiB; 180-second service limit | MP4 3,600.200 s / 90,836,929 bytes; MP3 3,600.000 s / 57,601,088 bytes; both within a one-second duration tolerance |
| Sustained pressure and recovery | CPU 0.5; memory.high 128 MiB / memory.max 384 MiB; 90-second service limit | Real CPU peak 100.4% relative to quota; memory ratio peak 0.935; both guard latches activated and cleared after load release and 30 healthy seconds; total 42.057 s |

The one-hour source is repeated H.264 video plus continuously generated 44.1 kHz AAC sine audio. Looping a short AAC container was rejected during fixture validation: repeated encoder priming shortened decoded MP3 by about 27 seconds. Continuous audio fixed that fixture without relaxing the duration check. Discovery is local metadata; transfer, merge, MP3 conversion, ffprobe, recovery and publication use the real backend. Source/output/configuration are on temporary repository-filesystem directories (ext4), not the earlier tmpfs workload. Content duration is one hour; actual transfer/processing is much faster. MP4 took 5.203 s and MP3 26.082 s; total test time 94.47 s includes source generation.

The one-hour service used 46.988 CPU seconds including fixture generation, was throttled in 943 of 945 periods and recorded 229 memory.high events. Its cgroup memory peak was 403,173,376 bytes (384.50 MiB); memory.max/oom/oom_kill counters stayed zero. The pressure service peaked at 125,485,056 bytes (119.67 MiB) against its 128 MiB soft limit, with no memory.high/max/OOM events; it exercises the application's near-limit ratio trigger, not forced OOM. Its accounting recorded 5.615 CPU seconds and 111 throttled periods. These aggregate cgroup peaks include Go/server/tool memory and charged file cache, omit the GUI and can vary by environment. The services expose no io.stat controller, so no physical disk bandwidth/latency conclusion follows.

A separate virtual-time regression advances a blocked media-slot wait by 31 minutes. It failed with the previous context deadline; after removing the transfer-wide 30-minute cutoff it stays waiting until explicit cancellation. This is a deterministic lifetime regression, not evidence of a real transfer running longer than 30 wall-clock minutes. Probe, metadata, ffprobe and network inactivity timeouts remain bounded; subprocess cancellation remains covered by native tests.

Reproduce with prepared pinned tools and a user systemd manager supporting these controls:

```bash
mkdir -p dist/stage5-resource/storage
go test -c -o "$PWD/dist/stage5-resource/fluxget.test" ./internal/engine
systemd-run --user --wait --pipe --collect --unit=fluxget-stage5-long -p CPUQuota=50% -p MemoryHigh=384M -p MemoryMax=512M -p MemorySwapMax=0 -p RuntimeMaxSec=180 -p TimeoutStopSec=5 --setenv=FLUXGET_MEDIA_TOOLS="$PWD/dist/install-current/media-tools/tools.json" --setenv=TMPDIR="$PWD/dist/stage5-resource/storage" "$PWD/dist/stage5-resource/fluxget.test" -test.run='^TestMediaQueueNativeLongDuration$' -test.v
systemd-run --user --wait --pipe --collect --unit=fluxget-stage5-pressure -p CPUQuota=50% -p MemoryHigh=128M -p MemoryMax=384M -p MemorySwapMax=0 -p RuntimeMaxSec=90 -p TimeoutStopSec=5 --setenv=FLUXGET_MEDIA_PRESSURE=1 "$PWD/dist/stage5-resource/fluxget.test" -test.run='^TestMediaQueueNativeResourcePressure$' -test.v
go test -race -count=1 -run '^TestDownloadDoesNotExpireWhileWaitingForMediaSlot$' ./media
```

Ordinary tests skip the opt-in pressure allocation/busy-loop trial. Actual >30-minute wall-clock transfers, repeated long-session stability, slow checkpoint/destination storage, forced subprocess OOM, native Windows and public-network long-video behavior remain unverified.

## Stage-5 public transfers — 2026-11-13

Current Go source and pinned development tools, Linux/amd64, through the existing credential-free local SOCKS proxy. No cookies, login, user queue or browser state was used. The route was not changed. Network timings reflect this environment rather than a general performance guarantee.

| Sample | Requested profile | Inspected result |
|---|---|---|
| `YE7VzlLtp-4` (Big Buck Bunny) | Separate compatible H.264/AAC into MP4 | 104,130,578 bytes; 720p/24 fps plus AAC; 596.544 seconds; production ffprobe and full FFmpeg decode passed |
| Playlist entry 1, `yN-Q-tFFElc` | Native M4A/AAC | 8,348,225 output bytes; selected duration 516 seconds; queue completed and ffprobe validated |
| Playlist entry 2, `aVjGozRKnS0` | Native M4A/AAC | 5,862,767 output bytes; selected duration 362 seconds; queue completed and ffprobe validated |
| Playlist entry 3, `BdvQkoF6Jq4` | Native M4A/AAC | 2,631,024 output bytes; selected duration 163 seconds; queue completed and ffprobe validated |
| Playlist entry 4, `DpNKHkMbULM` | Native M4A/AAC | 2,492,833 output bytes; selected duration 154 seconds; queue completed and ffprobe validated |

The four-item upstream sample playlist is `PL6IaIsEjSbf96XFRuNccS_RuEXwNdsoEu`. Real enumeration/analysis precede one atomic batch admission; the scheduler runs each media helper normally, captures the configured route, safely publishes outputs and persists schema-6 completion. Final summary: four completed, zero failed/pending/skipped. The whole opt-in test took 257.211 seconds, including metadata and transfer work. Metadata from flat enumeration can round duration differently from fresh per-video analysis; the table records the selected per-video duration.

The MP4 SHA-256 was `400a519111f2f79077a91ed1d23b69e0b446ceaee7798bd82e7602d4dc03da2c` (an identifier of this produced file, not a publisher-provided checksum). Temporary media and private metadata were removed after validation; only a small ignored local acceptance summary remains. No public file was installed or executed.

Reproduce playlist acceptance with explicitly selected public input and prepared tools:

```bash
FLUXGET_MEDIA_TOOLS="$PWD/dist/install-current/media-tools/tools.json" FLUXGET_MEDIA_PROXY="<PROXY_ADDRESS>" FLUXGET_MEDIA_PUBLIC_PLAYLIST='https://www.youtube.com/playlist?list=PL6IaIsEjSbf96XFRuNccS_RuEXwNdsoEu' go test -count=1 -v -run '^TestMediaQueuePublicPlaylist$' ./internal/engine
```

The test accepts only an explicitly selected finite 2–10 item playlist with entries no longer than ten minutes each and uses an eight-minute context bound. It skips in ordinary CI; public-service availability does not gate deterministic CI. These samples do not close native Windows, the full public format/Shorts matrix, public recovery or prolonged wall-clock/storage acceptance.
