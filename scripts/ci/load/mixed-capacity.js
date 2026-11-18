import { check } from 'k6';
import { sha256 } from 'k6/crypto';
import { b64decode } from 'k6/encoding';
import { scenario } from 'k6/execution';
import http from 'k6/http';
import { Counter, Rate, Trend } from 'k6/metrics';

const base = __ENV.MIXED_BASE_URL;
const admin = __ENV.MIXED_ADMIN_TOKEN;
const grader = __ENV.MIXED_GRADER_TOKEN;
const student = __ENV.MIXED_STUDENT_TOKEN;
const captureExam = __ENV.MIXED_CAPTURE_EXAM_ID;
const batch = __ENV.MIXED_CAPTURE_BATCH_ID;
const scoreExam = __ENV.MIXED_SCORE_EXAM_ID;
const release = __ENV.MIXED_RELEASE_ID;
const publishedTotal = Number(__ENV.MIXED_TOTAL);
const reviews = JSON.parse(__ENV.MIXED_REVIEW_TASK_IDS || '[]');
const scan = new Uint8Array(b64decode(__ENV.MIXED_SCAN_PDF || ''));
const output = __ENV.MIXED_SUMMARY_PATH;

if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(base || '') || !admin || !grader || !student
  || !captureExam || !batch || !scoreExam || !release || !__ENV.MIXED_TOTAL || !Number.isFinite(publishedTotal)
  || reviews.length < 18 || scan.length === 0 || !output) {
  throw new Error('Run this smoke test with the local Go fixture and synthetic users');
}

const completedUploads = new Counter('mixed_uploads');
const submittedReviews = new Counter('mixed_submissions');
const readScores = new Counter('mixed_score_reads');
const serverErrors = new Counter('mixed_http_5xx');
const operationOK = new Rate('mixed_operation_ok');
const operationTime = new Trend('mixed_operation_duration', true);

function ramp(exec, rates, vus) {
  return {
    executor: 'ramping-arrival-rate',
    exec,
    startRate: rates[0],
    timeUnit: '1s',
    preAllocatedVUs: vus,
    stages: rates.map((target) => ({ duration: '3s', target })),
    gracefulStop: '8s',
  };
}

export const options = {
  scenarios: {
    upload: ramp('uploadScan', [1, 2, 3], 8),
    review: ramp('submitReview', [1, 2, 3], 8),
    result: ramp('readScore', [4, 8, 12], 12),
  },
  summaryTrendStats: ['count', 'avg', 'p(95)', 'p(99)'],
  thresholds: {
    dropped_iterations: ['count==0'],
    mixed_http_5xx: ['count==0'],
    'mixed_operation_ok{operation:upload}': ['rate==1'],
    'mixed_operation_ok{operation:review}': ['rate==1'],
    'mixed_operation_ok{operation:result}': ['rate==1'],
    mixed_uploads: ['count>0'],
    mixed_submissions: ['count>0'],
    mixed_score_reads: ['count>0'],
    // 用标签子指标分别保留各操作的延迟分布。容量门槛在目标环境试验中另行约定。
    'mixed_operation_duration{operation:upload}': ['p(95)>=0'],
    'mixed_operation_duration{operation:review}': ['p(95)>=0'],
    'mixed_operation_duration{operation:result}': ['p(95)>=0'],
  },
};

function request(method, path, token, body, headers = {}, name = path) {
  const response = http.request(method, base + path, body, {
    headers: { Authorization: `Bearer ${token}`, ...headers },
    tags: { name },
    timeout: '5s',
  });
  // 连接失败和业务断言失败另有统计，5xx 只包含网关实际返回的服务端错误。
  serverErrors.add(response.status >= 500 && response.status < 600 ? 1 : 0);
  return response;
}

function digest(data) {
  return sha256(data, 'hex');
}

function matches(response, description, predicate) {
  let matched = false;
  let errorCode;
  try {
    const value = response.json();
    matched = Boolean(predicate(value));
    errorCode = value.error?.code;
  } catch {
    // HTML 错误页或断开连接在本次业务检查中算失败。
  }
  if (!matched) console.warn(`${description} HTTP ${response.status} code=${errorCode || response.error_code || 'none'}`);
  return check(matched, { [description]: (value) => value === true });
}

function measure(operation, counter, work) {
  const started = Date.now();
  let ok = false;
  try {
    ok = work();
    if (ok) counter.add(1);
  } finally {
    const tags = { operation };
    operationOK.add(ok, tags);
    operationTime.add(Date.now() - started, tags);
  }
}

function uploadPayload(index) {
  // 复用真实 PDF，并追加唯一的 PDF 注释以避免命中原件去重。
  const suffix = `\n% mixed capacity scan ${index}\n`;
  const bytes = new Uint8Array(scan.length + suffix.length);
  bytes.set(scan);
  for (let i = 0; i < suffix.length; i++) bytes[scan.length + i] = suffix.charCodeAt(i);
  return bytes.buffer;
}

export function uploadScan() {
  measure('upload', completedUploads, () => {
    const index = scenario.iterationInTest;
    const body = uploadPayload(index);
    const hash = digest(body);
    const init = request('POST', '/api/v1/capture/uploads:init', admin, JSON.stringify({
      exam: captureExam, batch, sha256: hash, size: body.byteLength, mime: 'application/pdf',
      filename: `scan-${index}.pdf`, idempotency_key: `mixed-capacity-${index}`,
    }), { 'Content-Type': 'application/json' });
    if (!matches(init, 'upload created', (value) => init.status === 200 && value.remote_upload_id
      && value.confirmed_offset === 0 && !value.already_exists)) return false;
    const uploadPath = `/api/v1/capture/uploads/${init.json('remote_upload_id')}`;
    const split = Math.floor(body.byteLength / 2);
    const offsets = [0, split, body.byteLength];
    for (let i = 0; i < offsets.length - 1; i++) {
      const chunk = body.slice(offsets[i], offsets[i + 1]);
      const sent = request('PUT', `${uploadPath}/chunks`, admin, chunk, {
        'Content-Type': 'application/octet-stream',
        'Upload-Offset': String(offsets[i]),
        'X-Chunk-SHA256': digest(chunk),
      }, '/api/v1/capture/uploads/{id}/chunks');
      if (!matches(sent, 'chunk offset confirmed', (value) => sent.status === 200
        && value.confirmed_offset === offsets[i + 1])) return false;
    }
    const complete = request('POST', `${uploadPath}/complete`, admin, JSON.stringify({ sha256: hash }),
      { 'Content-Type': 'application/json' }, '/api/v1/capture/uploads/{id}/complete');
    return matches(complete, 'upload completed', (value) => complete.status === 200 && value.status === 'completed'
      && value.confirmed_offset === body.byteLength && value.capture_file_id && value.sha256 === hash);
  });
}

export function submitReview() {
  measure('review', submittedReviews, () => {
    const taskID = reviews[scenario.iterationInTest];
    if (!taskID) return false;
    const response = request('POST', `/api/v1/review-tasks/${taskID}/submit`, grader, JSON.stringify({
      expected_revision: 1, score: 1, score_only: true, rubric_selections: [],
    }), { 'Content-Type': 'application/json' }, '/api/v1/review-tasks/{id}/submit');
    return matches(response, 'review submitted', (value) => response.status === 201
      && value.task?.status === 'submitted' && value.question_grade_id);
  });
}

export function readScore() {
  measure('result', readScores, () => {
    const response = request('GET', `/api/v1/student/exams/${scoreExam}/result`, student, null,
      {}, '/api/v1/student/exams/{id}/result');
    return matches(response, 'published result is consistent', (value) => response.status === 200
      && value.result?.release_id === release && value.result?.total_score === publishedTotal
      && value.result?.questions?.length === 3);
  });
}

export function handleSummary(data) {
  const metric = (name) => data.metrics[name]?.values || {};
  const summary = {
    uploads: metric('mixed_uploads').count || 0,
    submissions: metric('mixed_submissions').count || 0,
    score_reads: metric('mixed_score_reads').count || 0,
    http_requests: metric('http_reqs').count || 0,
    http_fail_rate: metric('http_req_failed').rate || 0,
    http_5xx: metric('mixed_http_5xx').count || 0,
    dropped_iterations: metric('dropped_iterations').count || 0,
    upload_ms: metric('mixed_operation_duration{operation:upload}'),
    review_ms: metric('mixed_operation_duration{operation:review}'),
    result_ms: metric('mixed_operation_duration{operation:result}'),
  };
  const report = JSON.stringify(summary, null, 2) + '\n';
  return { [output]: report, stdout: report };
}
