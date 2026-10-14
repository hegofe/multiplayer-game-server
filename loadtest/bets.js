import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const BETS = parseInt(__ENV.BETS || '5000', 10);
const MODE = __ENV.MODE || 'uniform';
const NAME = __ENV.NAME || `${MODE}-${BETS}`;
const OUT = __ENV.OUT || '';
const VUS = parseInt(__ENV.VUS || '1000', 10);
const WINDOW_MS = parseFloat(__ENV.WINDOW_MS || '4800');
const WARMUP_MS = parseFloat(__ENV.WARMUP_MS || '2000');
const BURST_SHARE = parseFloat(__ENV.BURST_SHARE || '0.8');
const BURST_MS = parseFloat(__ENV.BURST_MS || '1000');
const LATE_TOLERANCE_MS = 50;

const accepted = new Counter('bets_accepted');
const rejectedClosed = new Counter('bets_rejected_closed');
const rejectedBusy = new Counter('bets_rejected_busy');
const rejectedOther = new Counter('bets_rejected_other');
const transportErrors = new Counter('bets_transport_errors');
const acceptedAfterClose = new Counter('bets_accepted_after_close');
const behindSchedule = new Counter('bets_behind_schedule');
const storedBets = new Counter('bets_stored');
const betLatency = new Trend('bet_latency', true);
const sendDelay = new Trend('bet_send_delay', true);

export const options = {
  scenarios: {
    bets: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: Math.ceil(BETS / VUS),
      maxDuration: '90s',
      exec: 'bet',
    },
  },
  setupTimeout: '60s',
  teardownTimeout: '60s',
  summaryTrendStats: ['count', 'avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

function scheduledOffset(i) {
  if (MODE !== 'burst') return (i / BETS) * WINDOW_MS;
  const early = Math.round(BETS * (1 - BURST_SHARE));
  const earlyMs = WINDOW_MS - BURST_MS;
  if (i < early) return (i / early) * earlyMs;
  return earlyMs + ((i - early) / (BETS - early)) * BURST_MS;
}

function sleepUntil(t) {
  const ms = t - Date.now();
  if (ms > 0) sleep(ms / 1000);
}

export function setup() {
  const deadline = Date.now() + 50000;
  while (Date.now() < deadline) {
    const res = http.get(`${BASE_URL}/rounds/next`, { tags: { name: 'next' }, timeout: '2s' });
    if (res.status === 200) {
      const round = res.json();
      const opensAt = Date.parse(round.startTime);
      const closesAt = opensAt + round.bettingWindowSeconds * 1000;
      if (round.state === 'created' && opensAt - Date.now() > WARMUP_MS + 300) {
        sleepUntil(opensAt - WARMUP_MS);
        return { roundId: round.roundId, opensAt, closesAt, betsFrom: opensAt + 50 };
      }
    }
    sleep(0.2);
  }
  throw new Error('no upcoming round found');
}

export function bet(data) {
  const vu = exec.vu.idInTest - 1;
  const k = exec.vu.iterationInScenario;
  const i = k * VUS + vu;

  if (k === 0) {
    sleepUntil(data.betsFrom - WARMUP_MS + (vu / VUS) * WARMUP_MS * 0.8);
    http.get(`${BASE_URL}/health`, { tags: { name: 'warmup' } });
  }
  if (i >= BETS) return;

  const scheduled = data.betsFrom + scheduledOffset(i);
  sleepUntil(scheduled);
  const sentAt = Date.now();
  const delay = sentAt - scheduled;
  sendDelay.add(Math.max(0, delay));
  if (delay > LATE_TOLERANCE_MS) behindSchedule.add(1);

  const res = http.post(
    `${BASE_URL}/bets`,
    JSON.stringify({ roundId: data.roundId, userId: `user-${i + 1}`, multiplier: 2, amount: 1 }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'bet' }, responseCallback: http.expectedStatuses(201, 409, 503) },
  );
  betLatency.add(res.timings.duration);

  if (res.status === 201) {
    accepted.add(1);
    if (sentAt >= data.closesAt) acceptedAfterClose.add(1);
  } else if (res.status === 409 && String(res.body).includes('closed')) {
    rejectedClosed.add(1);
  } else if (res.status === 503) {
    rejectedBusy.add(1);
  } else if (res.status === 0) {
    transportErrors.add(1);
  } else {
    rejectedOther.add(1);
  }
}

export function teardown(data) {
  sleepUntil(data.closesAt + 500);
  const res = http.get(`${BASE_URL}/rounds/${data.roundId}/bets/count`, { tags: { name: 'count' } });
  if (res.status === 200) storedBets.add(parseInt(res.body, 10));
}

function metric(data, name, stat) {
  const m = data.metrics[name];
  if (!m) return 0;
  return m.values[stat] ?? 0;
}

export function handleSummary(data) {
  const s = {
    name: NAME,
    mode: MODE,
    bets: BETS,
    vus: VUS,
    windowMs: WINDOW_MS,
    sent: metric(data, 'bet_latency', 'count'),
    accepted: metric(data, 'bets_accepted', 'count'),
    rejectedClosed: metric(data, 'bets_rejected_closed', 'count'),
    rejectedBusy: metric(data, 'bets_rejected_busy', 'count'),
    rejectedOther: metric(data, 'bets_rejected_other', 'count'),
    transportErrors: metric(data, 'bets_transport_errors', 'count'),
    acceptedAfterClose: metric(data, 'bets_accepted_after_close', 'count'),
    behindSchedule: metric(data, 'bets_behind_schedule', 'count'),
    stored: metric(data, 'bets_stored', 'count'),
    latencyMs: {
      avg: metric(data, 'bet_latency', 'avg'),
      med: metric(data, 'bet_latency', 'med'),
      p90: metric(data, 'bet_latency', 'p(90)'),
      p95: metric(data, 'bet_latency', 'p(95)'),
      p99: metric(data, 'bet_latency', 'p(99)'),
      max: metric(data, 'bet_latency', 'max'),
    },
    sendDelayP95Ms: metric(data, 'bet_send_delay', 'p(95)'),
  };
  s.errorRate = s.sent ? (s.rejectedOther + s.transportErrors) / s.sent : 0;
  s.targetRate = BETS / (WINDOW_MS / 1000);

  const line = `${NAME}: sent ${s.sent}/${BETS}, accepted ${s.accepted}, closed ${s.rejectedClosed}, busy ${s.rejectedBusy}, ` +
    `other ${s.rejectedOther}, transport ${s.transportErrors}, behind schedule ${s.behindSchedule}, stored ${s.stored}, ` +
    `accepted after close ${s.acceptedAfterClose}, p50 ${s.latencyMs.med.toFixed(2)} ms, p95 ${s.latencyMs.p95.toFixed(2)} ms, ` +
    `p99 ${s.latencyMs.p99.toFixed(2)} ms\n`;
  const out = { stdout: line };
  if (OUT) out[OUT] = JSON.stringify(s, null, 2);
  return out;
}
