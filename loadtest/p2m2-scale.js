// P2 M2 kriteria 6 & 8: load test sustained >= 10.000 VU, >= 120 s, sambil jumlah instance
// Aggregator diubah (3 -> 5 -> 2, lihat p2m2-scale.sh) atau versinya diganti (rolling update).
//
// Tiap VU = satu pengguna yang membuka data lalu berpikir THINK_SECONDS (acak 0,5x-1,5x) sebelum
// request berikutnya, jadi beban yang ditawarkan ~ VUS / THINK req/s. VU dibagi rata ke client
// media / field-team / bnpb-ops. 429 dihitung sebagai penolakan terkontrol, bukan error.
// PHASES (detik sejak mulai, dipisah koma) menandai request per fase supaya throughput & error
// tiap fase (mis. saat 3, 5, lalu 2 instance) bisa dibandingkan.

import http from 'k6/http';
import { sleep } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';
import {
  API, CLIENTS, TREND_STATS, authParams, clientForVU, record, summarize, unexpected,
} from './lib/common.js';

const VUS = parseInt(__ENV.VUS || '10000', 10);
const RAMP = __ENV.RAMP || '30s';
const HOLD = __ENV.HOLD || '150s';
const THINK = parseFloat(__ENV.THINK_SECONDS || '2');
const LIMIT = parseInt(__ENV.LIMIT || '50', 10);
const LABEL = __ENV.LABEL || 'p2m2-scale';
// Batas fase dalam detik sejak tes mulai, mis. "60,120" -> fase f1 [0,60), f2 [60,120), f3 [120,..).
const PHASES = (__ENV.PHASES || '').split(',').filter(Boolean).map(Number);
const PHASE_NAMES = Array.from({ length: PHASES.length + 1 }, (_, i) => `f${i + 1}`);

const phaseOK = new Counter('phase_ok');
const phase429 = new Counter('phase_429');
const phaseErr = new Counter('phase_err');
// Gagal mendapat token (bnpb-auth) = request yang tidak sampai ke API; dihitung sebagai error.
const authFail = new Counter('auth_fail');

const thresholds = {
  api_unexpected_error: ['rate<0.01'], // spek: error di luar penolakan terkontrol < 1%
  // Hanya supaya submetrik latensi request data sukses muncul di ringkasan.
  'http_req_duration{kind:data,expected_response:true}': ['p(95)<100000'],
  auth_fail: ['count>=0'],
};
// Threshold "selalu lulus" ini hanya supaya k6 menyimpan submetrik per fase di ringkasan.
for (const p of PHASE_NAMES) {
  thresholds[`phase_ok{phase:${p}}`] = ['count>=0'];
  thresholds[`phase_429{phase:${p}}`] = ['count>=0'];
  thresholds[`phase_err{phase:${p}}`] = ['count>=0'];
}

export const options = {
  summaryTrendStats: TREND_STATS,
  discardResponseBodies: true,
  setupTimeout: '60s',
  scenarios: {
    sustained: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: RAMP, target: VUS },
        { duration: HOLD, target: VUS },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds,
};

export function setup() {
  const res = http.get(`${API}/hazards?limit=50`,
    { ...authParams(CLIENTS.ops, { kind: 'setup' }), responseType: 'text' });
  if (res.status !== 200) {
    throw new Error(`setup: GET /hazards returned ${res.status}`);
  }
  const ids = res.json('data').map((e) => e.hazard_id);
  if (ids.length === 0) {
    throw new Error('setup: Canonical Store masih kosong, tunggu polling beberapa detik');
  }
  return { ids };
}

function currentPhase() {
  const t = exec.instance.currentTestRunDuration / 1000;
  let i = 0;
  while (i < PHASES.length && t >= PHASES[i]) i++;
  return PHASE_NAMES[i];
}

export default function (data) {
  const client = clientForVU();
  const phase = currentPhase();
  const roll = Math.random();
  const endpoint = roll < 0.45 ? 'seismic' : roll < 0.9 ? 'volcanic' : 'detail';
  let params;
  try {
    params = authParams(client, { endpoint, phase });
  } catch (e) {
    authFail.add(1, { phase });
    unexpected.add(true);
    phaseErr.add(1, { phase });
    if (THINK > 0) sleep(THINK * (0.5 + Math.random()));
    return;
  }
  let res;
  if (endpoint === 'seismic') {
    res = http.get(`${API}/hazards?hazard_type=SEISMIC&limit=${LIMIT}`, params);
  } else if (endpoint === 'volcanic') {
    res = http.get(`${API}/hazards?hazard_type=VOLCANIC&limit=${LIMIT}`, params);
  } else {
    const id = data.ids[Math.floor(Math.random() * data.ids.length)];
    res = http.get(`${API}/hazards/${id}`, params);
  }
  record(res);
  if (res.status === 200) phaseOK.add(1, { phase });
  else if (res.status === 429) phase429.add(1, { phase });
  else phaseErr.add(1, { phase });

  if (THINK > 0) sleep(THINK * (0.5 + Math.random()));
}

export function handleSummary(data) {
  const out = summarize(LABEL, data, {
    'Semua request data (200)': 'http_req_duration{kind:data,expected_response:true}',
  });
  // Tabel per fase: dibandingkan dengan jadwal scale di p2m2-scale.sh.
  const bounds = [0, ...PHASES];
  const rows = ['', '| Fase | mulai (s) | 200 | 429 | error lain | error lain (%) |', '|---|---|---|---|---|---|'];
  for (let i = 0; i < PHASE_NAMES.length; i++) {
    const p = PHASE_NAMES[i];
    const get = (m) => {
      const s = data.metrics[`${m}{phase:${p}}`];
      return s ? s.values.count : 0;
    };
    const ok = get('phase_ok');
    const r429 = get('phase_429');
    const err = get('phase_err');
    const total = ok + r429 + err;
    rows.push(`| ${p} | ${bounds[i]} | ${ok} | ${r429} | ${err} | ${total ? ((100 * err) / total).toFixed(3) : '0'} |`);
  }
  const af = data.metrics.auth_fail ? data.metrics.auth_fail.values.count : 0;
  rows.push('', `- Gagal mendapat token (dihitung sebagai error di luar 429): ${af}`);
  const extra = rows.join('\n') + '\n';
  out.stdout += extra;
  out[`/results/${LABEL}.md`] += extra;
  return out;
}
