# Concurrency test results

Results of the concurrency tests for the Aviator betting table (`internal/game`), generated on 2026-10-14 12:23 UTC.

**60 of 60 runs passed** with the Go race detector enabled.

![Summary](summary.svg)

## What is being tested

A single goroutine owns each round. HTTP requests never touch the round directly: they send commands through a channel and wait for the answer. These tests fire hundreds of goroutines at the table at the same instant and check that the rules still hold.

The tests use [`testing/synctest`](https://pkg.go.dev/testing/synctest), so time is simulated: a bet can be sent exactly 1 ms before the close, and the results are deterministic. They test correctness, not speed; throughput and latency are measured separately with k6.

| Test | Checks | Runs passed | Result |
|---|---|---|---|
| `TestConcurrencyBurstAtClose` | No bet is accepted at or after the close of the betting window | 20 / 20 | ✅ pass |
| `TestConcurrencyQueueSaturation` | A full queue rejects immediately instead of blocking | 20 / 20 | ✅ pass |
| `TestConcurrencySameUser` | A user firing many bets at once gets exactly one accepted | 20 / 20 | ✅ pass |

## 1. Burst at the close of the betting window

![Bets around the close of the betting window](burst.svg)

1000 users bet at the same time, spread from 50 ms before to 49 ms after the close (10 bets per millisecond).

| Metric | Value | Expected |
|---|---|---|
| Accepted | 500 | every bet sent before the close |
| Rejected as betting closed | 500 | every bet sent at or after the close |
| Accepted at or after the close | 0 | 0 |
| Bets stored | 500 | same as accepted |
| Sum of all balances (cents) | 9950000 | 9950000 |

## 2. Same user betting from many goroutines at once

| Metric | Value | Expected |
|---|---|---|
| Simultaneous attempts | 200 | |
| Accepted | 1 | 1 |
| Rejected as duplicates | 199 | 199 |
| Bets stored | 1 | 1 |
| Final balance (cents) | 9500 | 9500 |

## 3. Saturated command queue

The table is held busy with one bet while 500 more arrive. The queue holds 16; the rest must be rejected immediately instead of waiting.

| Metric | Value | Expected |
|---|---|---|
| Queued | 16 | 16 |
| Rejected as busy | 484 | 484 |
| Longest wait before a busy rejection | 0 ms | 0 ms |
| Queued bets accepted once the table frees up | 16 | 16 |

## Coverage of the table

Statement coverage of `internal/game/table.go` reached by these tests alone (package total: 41.7%). Paths such as test rounds and round cancellation are not exercised by these tests.

| Function | Coverage |
|---|---|
| `reject` | 0.0% |
| `reject` | 0.0% |
| `NewTable` | 100.0% |
| `PlaceBet` | 60.0% |
| `StartTestRound` | 0.0% |
| `Done` | 100.0% |
| `Run` | 100.0% |
| `drain` | 75.0% |
| `handle` | 50.0% |
| `advance` | 50.0% |
| `schedule` | 100.0% |
| `retryLater` | 0.0% |
| `newRound` | 60.0% |
| `startRound` | 55.6% |
| `openBetting` | 80.0% |
| `closeBetting` | 78.6% |
| `takeOff` | 0.0% |
| `land` | 0.0% |
| `placeBet` | 63.6% |
| `refund` | 0.0% |
| `startTestRound` | 0.0% |
| `cancelRound` | 0.0% |

## Environment

| | |
|---|---|
| Host CPU | AMD Ryzen 5 7520U with Radeon Graphics |
| Host RAM | 14 GB |
| Host OS | Microsoft Windows 11 Home |
| Test runtime | Docker container `golang` (go1.27.2, linux/amd64, 8 CPUs) |

## Reproduce

```powershell
.\scripts\concurrency-report.ps1
```

The script runs `go test -race -count=20 -run '^TestConcurrency' ./internal/game/` inside a Go container and regenerates this folder. The full test output is in [test-output.txt](test-output.txt) and the raw figures in [data/](data/).
