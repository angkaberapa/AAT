// P2 kriteria 1 — head-of-line blocking.
//
// Prasyarat: pvmbg-mock berjalan dengan PVMBG_RESPONSE_DELAY_MIN_MS=MAX_MS=3000.
// Tiga skenario berjalan BERSAMAAN selama 60 s:
//   bmkg_only   : GET /hazards?hazard_type=SEISMIC lewat bnpb-api  (yang diukur: p95 < 300 ms)
//   pvmbg_data  : GET /hazards?hazard_type=VOLCANIC lewat bnpb-api (permintaan yang butuh data PVMBG)
//   pvmbg_direct: GET pvmbg-mock /volcanic-reports langsung, sebagai bukti sumbernya memang ~3 s
// Laju dijaga di bawah rate limit per client supaya yang terukur murni latensi, bukan 429.

import http from 'k6/http';
import { check, sleep } from 'k6';
import {
  API, PVMBG, TREND_STATS, authParams, clientForVU, record, summarize,
} from './lib/common.js';

const DURATION = __ENV.DURATION || '60s';

export const options = {
  summaryTrendStats: TREND_STATS,
  scenarios: {
    bmkg_only: { executor: 'constant-vus', vus: 10, duration: DURATION, exec: 'seismic' },
    pvmbg_data: { executor: 'constant-vus', vus: 10, duration: DURATION, exec: 'volcanic' },
    pvmbg_direct: { executor: 'constant-vus', vus: 2, duration: DURATION, exec: 'direct' },
  },
  thresholds: {
    'http_req_duration{endpoint:seismic,expected_response:true}': ['p(95)<300'],
    'http_req_duration{endpoint:volcanic,expected_response:true}': ['p(95)<300'],
    'http_req_duration{endpoint:pvmbg_direct}': ['p(95)>=2900'], // sumber memang lambat
    api_unexpected_error: ['rate<0.01'],
  },
};

export function seismic() {
  const res = http.get(`${API}/hazards?hazard_type=SEISMIC`,
    authParams(clientForVU(), { endpoint: 'seismic' }));
  record(res);
  check(res, { 'seismic 200': (r) => r.status === 200 });
  sleep(0.2);
}

export function volcanic() {
  const res = http.get(`${API}/hazards?hazard_type=VOLCANIC`,
    authParams(clientForVU(), { endpoint: 'volcanic' }));
  record(res);
  check(res, { 'volcanic 200': (r) => r.status === 200 });
  sleep(0.2);
}

export function direct() {
  const res = http.get(`${PVMBG}/volcanic-reports?since=2100-01-01T00:00:00Z`, {
    headers: { Authorization: `Bearer ${__ENV.PVMBG_TOKEN}` },
    tags: { endpoint: 'pvmbg_direct' },
  });
  check(res, { 'pvmbg direct 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  return summarize('p2-k1-hol', data, {
    'BMKG-only via bnpb-api (200)': 'http_req_duration{endpoint:seismic,expected_response:true}',
    'Data PVMBG via bnpb-api (200)': 'http_req_duration{endpoint:volcanic,expected_response:true}',
    'PVMBG langsung (sumber)': 'http_req_duration{endpoint:pvmbg_direct}',
  });
}
