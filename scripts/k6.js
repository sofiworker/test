// k6 压测：examples/tasks API 的混合负载 + 断言阈值。
// 用法（先启动服务）:
//   go run ./examples/tasks &
//   k6 run scripts/k6.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

export const options = {
  stages: [
    { duration: '10s', target: 20 }, // 预热
    { duration: '20s', target: 100 }, // 爬坡
    { duration: '20s', target: 100 }, // 稳态
    { duration: '10s', target: 0 },  // 收尾
  ],
  thresholds: {
    http_req_duration: ['p(95)<50', 'p(99)<200'], // 毫秒
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
  },
};

const BASE = 'http://localhost:8092';
const TOKEN = 'dev-token';
const AUTH = { headers: { Authorization: `Bearer ${TOKEN}` } };

export function setup() {
  // 预置任务 1，更新端点稳定命中 200
  http.post(`${BASE}/tasks`, JSON.stringify({ title: 'seed' }),
    Object.assign({ headers: { 'Content-Type': 'application/json' } }, AUTH));
  return {};
}

const getLatency = new Trend('get_latency');
const createLatency = new Trend('create_latency');
const fullLatency = new Trend('fullchain_latency');

export default function () {
  // 读多写少（3:1:1）
  const r = Math.random();

  if (r < 0.6) {
    const res = http.get(`${BASE}/tasks?page=1&size=20`, AUTH);
    getLatency.add(res.timings.duration);
    check(res, { 'list 200': (x) => x.status === 200 });
  } else if (r < 0.8) {
    const res = http.post(`${BASE}/tasks`, JSON.stringify({ title: 'k6-task' }),
      Object.assign({ headers: { 'Content-Type': 'application/json' } }, AUTH));
    createLatency.add(res.timings.duration);
    check(res, { 'create 201': (x) => x.status === 201 });
  } else {
    const res = http.put(`${BASE}/tasks/1`, JSON.stringify({ title: 'updated' }),
      Object.assign({ headers: { 'Content-Type': 'application/json' } }, AUTH));
    fullLatency.add(res.timings.duration);
    check(res, { 'update 200/404': (x) => x.status === 200 || x.status === 404 });
  }
  sleep(0.05);
}
