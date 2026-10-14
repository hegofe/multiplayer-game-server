# Load test results

Load tests of the Aviator betting service with [k6](https://k6.io), generated on 2026-10-14 13:32 UTC.

**At the production load of 5,000 bets per round, the median run answered with p95 11 ms and p99 18 ms, accepted 100% of the bets and had 0 errors.**

![Bet latency by scenario](latency.svg)

![Outcome of the bets by scenario](outcome.svg)

## How the test works

Each run targets one real round of the running server. k6 waits until the round is announced, opens 1000 connections (one per virtual user) before betting opens, and then sends every bet at a scheduled instant inside the betting window, each from a different user (one bet per user and round).

- **Uniform**: bets are spread evenly over the first 4.8 s of the 5 s window.
- **Burst**: 20% of the bets over the first 3.8 s and 80% in the last second before the close.
- **Stress**: uniform, at 2, 5 and 10 times the production volume.

The production reference is an average of 5,000 bets per round. Each scenario ran 3 times; tables show the run with the median p95.

## Results

| Scenario | Bets per round | Target rate | Accepted | Rejected after close | Errors | p50 | p95 | p99 | p95 across runs | Sent late by k6 |
|---|---|---|---|---|---|---|---|---|---|---|
| Base | 5,000 | 1,042 bets/s | 5,000 (100%) | 0 | 0 | 6.2 ms | 11 ms | 18 ms | 10 ms – 11 ms | 0% |
| Burst | 5,000 | 1,042 bets/s on average, 4,000 bets/s in the last second | 5,000 (100%) | 0 | 0 | 8.7 ms | 27 ms | 40 ms | 22 ms – 267 ms | 2% |
| Stress 2x | 10,000 | 2,083 bets/s | 10,000 (100%) | 0 | 0 | 9.4 ms | 50 ms | 76 ms | 30 ms – 69 ms | 1% |
| Stress 5x | 25,000 | 5,208 bets/s | 23,941 (96%) | 1,059 | 0 | 48 ms | 223 ms | 432 ms | 217 ms – 242 ms | 37% |
| Stress 10x | 50,000 | 10,417 bets/s | 23,841 (48%) | 26,159 | 0 | 83 ms | 327 ms | 542 ms | 277 ms – 397 ms | 99% |

*Sent late by k6* is the share of bets that k6 sent more than 50 ms after their scheduled instant. When it is high, the load generator, not only the server, has run out of capacity.

## Correctness under load

| Check | Result |
|---|---|
| Bets accepted after the window closed, across all 15 runs | 0 |
| Runs where bets stored in Redis differ from bets accepted | 0 |
| Server or transport errors, across all runs | 0 |

## Reading the results

- k6 and the server run on the **same laptop** and compete for its CPU. The figures describe this single-machine setup, not the server on dedicated hardware.
- When the requested rate exceeds what the machine can handle, latency grows, k6 falls behind schedule and some bets reach the server after the window has closed. The server rejects them, as it must; they are not errors.
- Results vary between runs on a laptop (power and thermal management, background processes); the *p95 across runs* column shows that spread.

## Environment

| | |
|---|---|
| Host CPU | AMD Ryzen 5 7520U with Radeon Graphics (8 logical processors) |
| Host RAM | 14 GB |
| Host OS | Microsoft Windows 11 Home |
| k6 | v2.2.0, 1000 virtual users |
| Server | native Windows binary, Redis 8 in Docker Desktop |

## Reproduce

```powershell
.\scripts\load-test.ps1
```

The script starts Redis and the server with enough users, runs every scenario, saves the raw k6 summaries in [data/](data/) and regenerates this folder.
