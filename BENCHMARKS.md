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
go test -run '^$' -bench '^BenchmarkRecorded(Workload|Cancellation)$' -benchtime=1x -count=5 -v
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
TMPDIR=/tmp FLUXGET_WORKLOAD_STORAGE_DIR="$PWD" go test -run '^$' -bench '^BenchmarkRecordedQueueWorkload$' -benchtime=1x -count=3 -v
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
