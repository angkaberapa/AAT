// P5 criterion 3: BMKG and PVMBG latency while Sensor Pantai is unhealthy.
import http from 'k6/http';
import { check } from 'k6';
import { API, CLIENTS, authParams, record, summarize } from './lib/common.js';

const VUS = Number(__ENV.VUS || 5);

export const options = {
  scenarios: {
    bmkg: {
      executor: 'constant-vus',
      vus: VUS,
      duration: '60s',
      exec: 'bmkg',
    },
    pvmbg: {
      executor: 'constant-vus',
      vus: VUS,
      duration: '60s',
      exec: 'pvmbg',
    },
  },
  thresholds: {
    http_reqs: ['count>0'],
    'http_req_duration{endpoint:bmkg}': ['p(95)<2000'],
    'http_req_duration{endpoint:pvmbg}': ['p(95)<2000'],
    api_unexpected_error: ['rate<0.01'],
  },
};

function fetchSource(source, endpoint) {
  const response = http.get(
    `${API}/hazards?source=${source}&limit=5`,
    authParams(CLIENTS.ops, { endpoint }),
  );
  record(response);
  check(response, { [`${source} 200`]: (r) => r.status === 200 });
}

export function bmkg() {
  fetchSource('BMKG', 'bmkg');
}

export function pvmbg() {
  fetchSource('PVMBG', 'pvmbg');
}

export function handleSummary(data) {
  return summarize('p5-other-sources', data, {
    'BMKG (200)': 'http_req_duration{endpoint:bmkg}',
    'PVMBG (200)': 'http_req_duration{endpoint:pvmbg}',
  });
}
