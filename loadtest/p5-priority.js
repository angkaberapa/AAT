// P5 priority admission test.
//
// Media is driven hard enough to fill its in-flight cap. Field runs at a
// steady rate and must keep getting 200 while Media receives controlled 429.
//
//   loadtest/run.sh p5-priority.js
//
// The API and Auth URLs default to the Docker Compose network names.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { API, CLIENTS, authParams, record, summarize } from './lib/common.js';

export const options = {
  scenarios: {
    media_flood: {
      executor: 'constant-vus',
      vus: 150,
      duration: '60s',
      exec: 'mediaFlood',
    },
    field_steady: {
      executor: 'constant-arrival-rate',
      rate: 10,
      timeUnit: '1s',
      duration: '60s',
      preAllocatedVUs: 20,
      maxVUs: 40,
      exec: 'fieldSteady',
    },
  },
  thresholds: {
    'http_req_duration{endpoint:field}': ['p(95)<2000'],
    api_unexpected_error: ['rate<0.01'],
  },
};

export function mediaFlood() {
  const response = http.get(`${API}/hazards?limit=5`, authParams(CLIENTS.media, { endpoint: 'media' }));
  record(response);
  check(response, {
    'media gets 200 or controlled 429': (r) => r.status === 200 || r.status === 429,
  });
  sleep(0.1);
}

export function fieldSteady() {
  const response = http.get(`${API}/hazards?limit=5`, authParams(CLIENTS.field, { endpoint: 'field' }));
  record(response);
  check(response, {
    'field gets 200': (r) => r.status === 200,
  });
  sleep(0.5);
}

export function handleSummary(data) {
  return summarize('p5-priority', data, {
    'Media (200 + 429)': 'http_req_duration{endpoint:media}',
    'Field (200 only)': 'http_req_duration{endpoint:field}',
  });
}
