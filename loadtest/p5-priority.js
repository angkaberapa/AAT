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
import { Counter } from 'k6/metrics';
import { API, CLIENTS, authParams, record, summarize } from './lib/common.js';

const fieldOK = new Counter('p5_field_ok');
const fieldRejected = new Counter('p5_field_rejected');
const mediaOK = new Counter('p5_media_ok');
const mediaRejected = new Counter('p5_media_rejected');
const mediaMissingRetryAfter = new Counter('p5_media_429_missing_retry_after');

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
    'http_req_duration{endpoint:media}': ['p(95)<1000'],
    'http_req_duration{endpoint:field}': ['p(95)<2000'],
    api_unexpected_error: ['rate<0.01'],
    p5_field_ok: ['count>0'],
    p5_field_rejected: ['count==0'],
    p5_media_rejected: ['count>0'],
    p5_media_429_missing_retry_after: ['count==0'],
  },
};

export function mediaFlood() {
  const response = http.get(`${API}/hazards?limit=5`, authParams(CLIENTS.media, { endpoint: 'media' }));
  record(response);
  if (response.status === 200) {
    mediaOK.add(1);
  } else if (response.status === 429) {
    mediaRejected.add(1);
    const retryAfter = response.headers['Retry-After'] || response.headers['retry-after'];
    if (!retryAfter) {
      mediaMissingRetryAfter.add(1);
    }
  }
  check(response, {
    'media gets 200 or controlled 429': (r) => r.status === 200 || r.status === 429,
  });
  sleep(0.1);
}

export function fieldSteady() {
  const response = http.get(`${API}/hazards?limit=5`, authParams(CLIENTS.field, { endpoint: 'field' }));
  record(response);
  if (response.status === 200) {
    fieldOK.add(1);
  } else if (response.status === 429) {
    fieldRejected.add(1);
  }
  check(response, {
    'field gets 200': (r) => r.status === 200,
  });
  sleep(0.5);
}

export function handleSummary(data) {
  const summary = summarize('p5-priority', data, {
    'Media (200 + 429)': 'http_req_duration{endpoint:media}',
    'Field (200 only)': 'http_req_duration{endpoint:field}',
  });
  const count = (name) => (data.metrics[name] ? data.metrics[name].values.count : 0);
  const perRole = [
    '',
    '## Peran',
    '',
    `- Field 200: ${count('p5_field_ok')}, 429: ${count('p5_field_rejected')}`,
    `- Media 200: ${count('p5_media_ok')}, 429: ${count('p5_media_rejected')}`,
    `- Media 429 tanpa Retry-After: ${count('p5_media_429_missing_retry_after')}`,
    '',
  ].join('\n');
  const text = summary.stdout + perRole;
  return {
    ...summary,
    stdout: text,
    '/results/p5-priority.md': text,
  };
}
