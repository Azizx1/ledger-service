# Local performance report

Measured on 2026-08-16 with the service's 64 KiB buffered JSON transaction log enabled.

## Environment

| Component | Value |
|---|---|
| Host | Apple M3 Pro, 18 GiB RAM |
| OS | macOS 26.4.1, arm64 |
| Go | 1.26.6 |
| TigerBeetle | 0.17.9 release binary, one development replica |
| Network | Service, load generator, and TigerBeetle on one host over loopback |
| Risk simulation | Fixed 200 ms delay |

The corporate and card-wallet accounts were created and funded before measurement. Each command
used a new TigerBeetle time-based ID. Each operation received 1,000 unmeasured warm-up requests,
followed by three measured runs of 10,000 requests through one reused HTTP transport. Posted
operations used 256 workers; risk-evaluated operations used 4,096 workers to hide the intentional
200 ms delay. No application queue or batch timer was used.

## Results

Every measured request completed successfully. Values below are medians across the three runs;
the range shows the minimum and maximum run result.

| Operation | Concurrency | Throughput, req/s | p50 | p95 | p99 | Worst request | Challenge SLA |
|---|---:|---:|---:|---:|---:|---:|---|
| Top-up | 256 | 12,531 (12,509–13,251) | 18.3 ms | 37.2 ms | 42.1 ms (36.3–42.2) | 45.1 ms | Pass: ≤100 ms |
| Withdrawal | 256 | 12,623 (11,696–12,945) | 18.1 ms | 46.5 ms | 49.1 ms (48.5–53.2) | 56.1 ms | Pass: ≤100 ms |
| Authorization | 4,096 | 13,443 (13,214–14,044) | 237.0 ms | 270.1 ms | 274.8 ms (262.9–473.9) | 481.6 ms | Pass: ≤500 ms |
| Increment | 4,096 | 10,543 (9,281–12,650) | 313.7 ms | 411.3 ms | 423.6 ms (411.7–457.9) | 479.5 ms | Pass: ≤500 ms |

The increment is slower than the initial authorization because it first looks up and validates
the original pending transfer, runs risk again, and then submits the additional hold. Every run
met its latency target. Median throughput exceeded 10,000 requests/s for every operation, but one
increment run reached 9,281 requests/s. The evidence therefore supports the median target, not a
claim of deterministic 10,000 requests/s in every local run.

## Longer-run observation

The same harness was run with 30,000 measured requests per run after the development data file had
accumulated several hundred thousand transfers:

| Operation | Median throughput | Median p99 | Range of p99 | Worst request |
|---|---:|---:|---:|---:|
| Top-up | 9,668 req/s | 116.6 ms | 32.3–149.6 ms | 162.7 ms |
| Withdrawal | 12,811 req/s | 42.8 ms | 35.3–56.9 ms | 61.7 ms |
| Authorization | 16,199 req/s | 263.0 ms | 255.2–395.7 ms | 459.1 ms |
| Increment | 13,681 req/s | 327.6 ms | 320.1–349.8 ms | 367.6 ms |

All 360,000 measured requests succeeded. During the broader local test session, the single-replica
log reported checkpoint/compaction tails as high as roughly 103 ms. Because top-up and withdrawal
use the same direct create-transfer path, the top-up outliers are an environment/timing observation
rather than evidence of a top-up-specific algorithmic difference. They do show why the
10,000-request challenge run must not be presented as a sustained production SLA.

## Reproduce

Start a local cluster, create and fund the accounts from the README, and build both binaries:

```sh
make up
make build
```

The load generator defaults match the example IDs, amount, run count, warm-up, and the
operation-specific concurrency above. Run:

```sh
./bin/loadtest -operation topup
./bin/loadtest -operation withdraw
./bin/loadtest
./bin/loadtest -operation increment
```

Use explicit flags when benchmarking different accounts or parameters; `./bin/loadtest -h` lists
all defaults. `-concurrency 0` restores automatic selection after any wrapper or environment has
supplied flags.

The load tool exits non-zero on any HTTP, transport, or business-outcome failure and reports the
failure category plus a sample error. Reusing one transport across runs is important on a local
host: repeatedly starting separate 4,096-connection generators can exhaust the host's ephemeral
ports and measure connection setup rather than the service.

## Interpretation

This is evidence that the implementation meets the challenge target on the stated development
environment for the challenge-sized latency run; it is not a production capacity claim, and the
longer top-up run demonstrates that limitation. A production qualification must use the
six-replica, three-site topology, representative inter-site latency and storage, multiple remote
load generators, realistic account contention, sustained soak tests, and replica/network failure
tests. Capacity should be accepted from that environment's p99/p99.9 and error-rate results.
