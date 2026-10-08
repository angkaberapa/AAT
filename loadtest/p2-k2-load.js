// P2 kriteria 2 — load test sustained.
//
// VUS koneksi paralel (default 60, spek minimal 50) terus-menerus mengirim request tanpa jeda
// selama DURATION (default 75 s, spek minimal 60 s). VU dibagi rata ke client media / field-team /
// bnpb-ops, masing-masing dengan token sendiri yang di-refresh otomatis.
// THINK_SECONDS (default 0) memberi jeda antar request per VU.
// Campuran request: 45% snapshot seismik, 45% snapshot vulkanik, 10% detail satu event.
// Yang dilaporkan: throughput, p50/p95/p99, error rate di luar 429, dan jumlah 429.

import http from 'k6/http';
import { check, sleep } from 'k6';
import {
  API, CLIENTS, TREND_STATS, authParams, clientForVU, record, summarize,
} from './lib/common.js';

const VUS = parseInt(__ENV.VUS || '60', 10);
const DURATION = __ENV.DURATION || '75s';
const LABEL = __ENV.LABEL || 'p2-k2-load';
// Jeda antar request per VU (detik). 0 = closed-loop flood (beban maksimum).
const THINK = parseFloat(__ENV.THINK_SECONDS || '0');

export const options = {
  summaryTrendStats: TREND_STATS,
  scenarios: {
    sustained: { executor: 'constant-vus', vus: VUS, duration: DURATION },
  },
  thresholds: {
    api_unexpected_error: ['rate<0.01'], // spek: error di luar penolakan terkontrol < 1%
    'http_req_duration{endpoint:seismic,expected_response:true}': ['p(95)<100000'],
    'http_req_duration{endpoint:volcanic,expected_response:true}': ['p(95)<100000'],
    'http_req_duration{endpoint:detail,expected_response:true}': ['p(95)<100000'],
    'http_req_duration{kind:data,expected_response:true}': ['p(95)<100000'],
  },
};

// Ambil beberapa hazard_id nyata untuk request detail.
export function setup() {
  const res = http.get(`${API}/hazards?limit=50`, authParams(CLIENTS.ops, { kind: 'setup' }));
  if (res.status !== 200) {
    throw new Error(`setup: GET /hazards returned ${res.status}`);
  }
  const ids = res.json('data').map((e) => e.hazard_id);
  if (ids.length === 0) {
    throw new Error('setup: Canonical Store masih kosong, tunggu polling beberapa detik');
  }
  return { ids };
}

export default function (data) {
  const client = clientForVU();
  const roll = Math.random();
  let res;
  if (roll < 0.45) {
    res = http.get(`${API}/hazards?hazard_type=SEISMIC`,
      authParams(client, { endpoint: 'seismic' }));
  } else if (roll < 0.9) {
    res = http.get(`${API}/hazards?hazard_type=VOLCANIC`,
      authParams(client, { endpoint: 'volcanic' }));
  } else {
    const id = data.ids[Math.floor(Math.random() * data.ids.length)];
    res = http.get(`${API}/hazards/${id}`, authParams(client, { endpoint: 'detail' }));
  }
  record(res);
  check(res, { '200 atau 429': (r) => r.status === 200 || r.status === 429 });
  if (THINK > 0) sleep(THINK);
}

export function handleSummary(data) {
  return summarize(LABEL, data, {
    'Semua request data (200)': 'http_req_duration{kind:data,expected_response:true}',
    'Snapshot seismik (200)': 'http_req_duration{endpoint:seismic,expected_response:true}',
    'Snapshot vulkanik (200)': 'http_req_duration{endpoint:volcanic,expected_response:true}',
    'Detail event (200)': 'http_req_duration{endpoint:detail,expected_response:true}',
  });
}
