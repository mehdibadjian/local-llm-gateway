import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: 50,
  duration: '60s',
  thresholds: {
    http_req_duration: ['p(95)<3000'],
    'http_req_duration{status:200}': ['p(95)<3000'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY  = __ENV.API_KEY  || 'dev-key';

export default function () {
  const payload = JSON.stringify({
    model: 'gemma:2b',
    messages: [{ role: 'user', content: 'Hello, what is 2+2?' }],
    stream: false,
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${API_KEY}`,
    },
  };

  const res = http.post(`${BASE_URL}/v1/chat/completions`, payload, params);

  check(res, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
  });

  sleep(0.1);
}

export function handleSummary(data) {
  return {
    'tests/k6/summary.json': JSON.stringify(data),
  };
}
