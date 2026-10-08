// Shared helpers for the P2 k6 scripts.
//
// Credentials are read from the environment (docker run --env-file .env), never
// hard-coded here. Each VU keeps its own access token and refreshes it before the
// 60 s TTL runs out, so tests longer than the TTL keep working.

import http from 'k6/http';
import { Counter, Rate } from 'k6/metrics';
import exec from 'k6/execution';

export const API = __ENV.API_URL || 'http://bnpb-api:8080';
export const AUTH = __ENV.AUTH_URL || 'http://bnpb-auth:8000';
export const PVMBG = __ENV.PVMBG_URL || 'http://pvmbg-mock:8082';

function required(name) {
  const value = __ENV[name];
  if (!value) {
    throw new Error(`${name} is not set; run k6 with --env-file .env`);
  }
  return value;
}

export const CLIENTS = {
  media: { id: required('CLIENT_MEDIA_ID'), secret: required('CLIENT_MEDIA_SECRET') },
  field: { id: required('CLIENT_FIELD_ID'), secret: required('CLIENT_FIELD_SECRET') },
  ops: { id: required('CLIENT_OPS_ID'), secret: required('CLIENT_OPS_SECRET') },
};

// --- metrics shared by all scripts -------------------------------------------
// Only calls to bnpb-api data endpoints are counted (auth calls are excluded).
export const apiOK = new Counter('api_ok');                 // HTTP 200
export const rejected429 = new Counter('api_rejected_429'); // controlled rejections
export const unexpected = new Rate('api_unexpected_error'); // anything that is not 200 or 429
export const status5xx = new Counter('api_5xx');

const JSON_HEADERS = { 'Content-Type': 'application/json' };
const REFRESH_MARGIN_MS = 10000;

// Per-VU token state (module scope is per VU in k6).
let session = null;

function storeSession(res) {
  const body = res.json();
  return {
    access: body.access_token,
    refresh: body.refresh_token,
    expiresAt: Date.now() + body.expires_in * 1000,
  };
}

function issue(client) {
  const res = http.post(`${AUTH}/token`,
    JSON.stringify({ client_id: client.id, client_secret: client.secret }),
    { headers: JSON_HEADERS, tags: { name: 'auth' } });
  if (res.status !== 200) {
    throw new Error(`token request for ${client.id} failed: HTTP ${res.status}`);
  }
  return storeSession(res);
}

function refresh(current, client) {
  const res = http.post(`${AUTH}/token/refresh`,
    JSON.stringify({ refresh_token: current.refresh }),
    { headers: JSON_HEADERS, tags: { name: 'auth' } });
  return res.status === 200 ? storeSession(res) : issue(client);
}

// Returns request params with a valid bearer token for this VU's client.
export function authParams(client, tags) {
  if (!session) {
    session = issue(client);
  } else if (Date.now() > session.expiresAt - REFRESH_MARGIN_MS) {
    session = refresh(session, client);
  }
  // kind:data membedakan request data dari request token / admin di metrik.
  return { headers: { Authorization: `Bearer ${session.access}` }, tags: { kind: 'data', ...tags } };
}

// Spread VUs evenly over the three client identities (round robin by VU id).
export function clientForVU() {
  const all = [CLIENTS.ops, CLIENTS.field, CLIENTS.media];
  return all[(exec.vu.idInTest - 1) % all.length];
}

// Records the outcome of one bnpb-api data call in the shared metrics.
export function record(res) {
  if (res.status === 200) {
    apiOK.add(1);
  } else if (res.status === 429) {
    rejected429.add(1);
  }
  if (res.status >= 500) {
    status5xx.add(1);
  }
  unexpected.add(res.status !== 200 && res.status !== 429);
}

export function setPVMBGOutage(enabled, mode) {
  const body = enabled ? { enabled: true, mode: mode || 'error' } : { enabled: false };
  const res = http.post(`${PVMBG}/admin/outage`, JSON.stringify(body),
    { headers: JSON_HEADERS, tags: { name: 'admin' } });
  if (res.status !== 200) {
    throw new Error(`toggle PVMBG outage failed: HTTP ${res.status}`);
  }
}

// --- summary -----------------------------------------------------------------

function fmt(n, digits = 2) {
  return n === undefined || n === null ? '-' : Number(n).toFixed(digits);
}

function trendRow(label, metric) {
  if (!metric) return `| ${label} | - | - | - | - | - | - |`;
  const v = metric.values;
  return `| ${label} | ${fmt(v.avg)} | ${fmt(v.med)} | ${fmt(v['p(95)'])} | ${fmt(v['p(99)'])} | ${fmt(v.max)} | ${fmt(v.min)} |`;
}

function count(data, name) {
  const m = data.metrics[name];
  return m ? m.values.count : 0;
}

// Builds a markdown summary plus a JSON dump under loadtest/results/.
// `latencies` maps a row label to a metric key, e.g.
// {'seismic (200 only)': 'http_req_duration{endpoint:seismic,expected_response:true}'}.
export function summarize(name, data, latencies) {
  const durationS = data.state.testRunDurationMs / 1000;
  const ok = count(data, 'api_ok');
  const r429 = count(data, 'api_rejected_429');
  const err = data.metrics.api_unexpected_error;
  const total = err ? err.values.passes + err.values.fails : 0;
  const unexpectedCount = err ? err.values.passes : 0;

  const lines = [
    `## ${name}`,
    '',
    `- Durasi tes: ${fmt(durationS, 1)} s, VU maksimum: ${data.metrics.vus_max ? data.metrics.vus_max.values.max : '-'}`,
    `- Request data ke bnpb-api: ${total}`,
    `- Throughput total: ${fmt(total / durationS, 1)} req/s, sukses (200): ${fmt(ok / durationS, 1)} req/s`,
    `- 200: ${ok} | 429 (penolakan terkontrol): ${r429} (${fmt(total ? (100 * r429) / total : 0)} %)`,
    `- Error di luar 429: ${unexpectedCount} (${fmt(total ? (100 * unexpectedCount) / total : 0, 3)} %), di antaranya 5xx: ${count(data, 'api_5xx')}`,
    '',
    '| Latensi (ms) | avg | p50 | p95 | p99 | max | min |',
    '|---|---|---|---|---|---|---|',
  ];
  for (const [label, key] of Object.entries(latencies)) {
    lines.push(trendRow(label, data.metrics[key]));
  }

  const thresholds = [];
  for (const [metric, m] of Object.entries(data.metrics)) {
    if (!m.thresholds) continue;
    for (const [expr, t] of Object.entries(m.thresholds)) {
      thresholds.push(`- ${t.ok ? 'LULUS' : 'GAGAL'}: \`${metric}\` ${expr}`);
    }
  }
  if (thresholds.length) {
    lines.push('', 'Threshold:', ...thresholds);
  }

  const text = lines.join('\n') + '\n';
  return {
    stdout: text,
    [`/results/${name}.md`]: text,
    [`/results/${name}.json`]: JSON.stringify(data, null, 2),
  };
}

export const TREND_STATS = ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'];
