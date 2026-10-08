// P2 kriteria 3 — graceful degradation saat PVMBG outage.
//
// Linimasa (total 90 s), outage dipicu lewat POST /admin/outage oleh skenario `controller`:
//   0–20 s   before   : normal
//   20 s     outage NYALA (mode OUTAGE_MODE, default "hang" = tidak menjawab sama sekali)
//   28–60 s  outage   : diharapkan data vulkanik tetap 200 + meta.stale=true, seismik normal
//   60 s     outage MATI
//   70–90 s  after    : diharapkan kembali meta.stale=false tanpa restart apa pun
// Jeda 20–28 s dan 60–70 s adalah masa transisi (poller perlu satu siklus / timeout untuk
// mendeteksi perubahan), jadi tidak dinilai.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import exec from 'k6/execution';
import {
  API, TREND_STATS, authParams, clientForVU, record, setPVMBGOutage, summarize,
} from './lib/common.js';

const MODE = __ENV.OUTAGE_MODE || 'hang';

const staleDuringOutage = new Rate('volcanic_marked_stale_during_outage');
const freshAfterRecovery = new Rate('volcanic_fresh_after_recovery');
const seismicFreshDuringOutage = new Rate('seismic_fresh_during_outage');

export const options = {
  summaryTrendStats: TREND_STATS,
  scenarios: {
    load: { executor: 'constant-vus', vus: 10, duration: '90s', exec: 'load' },
    controller: {
      executor: 'shared-iterations', vus: 1, iterations: 1, maxDuration: '95s', exec: 'controller',
    },
  },
  thresholds: {
    'http_req_duration{endpoint:seismic,phase:outage}': ['p(95)<300'],
    'http_req_duration{endpoint:volcanic,phase:outage}': ['p(95)<300'],
    'http_req_duration{endpoint:seismic,phase:before}': ['p(95)<300'],
    'http_req_duration{endpoint:volcanic,phase:after}': ['p(95)<300'],
    'http_req_failed{kind:data}': ['rate<0.01'],
    api_5xx: ['count==0'],
    volcanic_marked_stale_during_outage: ['rate==1'],
    volcanic_fresh_after_recovery: ['rate==1'],
    seismic_fresh_during_outage: ['rate==1'],
  },
};

function phase() {
  const t = exec.instance.currentTestRunDuration / 1000;
  if (t < 20) return 'before';
  if (t < 28) return 'transition_on';
  if (t < 60) return 'outage';
  if (t < 70) return 'transition_off';
  return 'after';
}

export function load() {
  const p = phase();
  const client = clientForVU();
  const volcanic = exec.vu.iterationInScenario % 2 === 0;
  const endpoint = volcanic ? 'volcanic' : 'seismic';
  const res = http.get(`${API}/hazards?hazard_type=${volcanic ? 'VOLCANIC' : 'SEISMIC'}`,
    authParams(client, { endpoint, phase: p }));
  record(res);

  const ok = check(res, {
    'tidak 5xx': (r) => r.status < 500,
    'bukan menggantung (< 2 s)': (r) => r.timings.duration < 2000,
  });
  if (ok && res.status === 200) {
    const stale = res.json('meta.stale');
    if (volcanic && p === 'outage') {
      staleDuringOutage.add(stale === true);
      check(res, { 'vulkanik tetap berisi data': (r) => r.json('data').length > 0 });
    }
    if (volcanic && p === 'after') freshAfterRecovery.add(stale === false);
    if (!volcanic && p === 'outage') seismicFreshDuringOutage.add(stale === false);
  }
  sleep(0.2);
}

export function controller() {
  sleep(20);
  setPVMBGOutage(true, MODE);
  console.log(`[t=20s] PVMBG outage ON (mode=${MODE})`);
  sleep(40);
  setPVMBGOutage(false);
  console.log('[t=60s] PVMBG outage OFF');
}

// Pastikan outage dimatikan walau tes dihentikan di tengah.
export function teardown() {
  setPVMBGOutage(false);
}

export function handleSummary(data) {
  return summarize(`p2-k3-outage-${MODE}`, data, {
    'Seismik, sebelum outage': 'http_req_duration{endpoint:seismic,phase:before}',
    'Seismik, selama outage': 'http_req_duration{endpoint:seismic,phase:outage}',
    'Vulkanik, selama outage (data store + stale)': 'http_req_duration{endpoint:volcanic,phase:outage}',
    'Vulkanik, setelah pulih': 'http_req_duration{endpoint:volcanic,phase:after}',
  });
}
