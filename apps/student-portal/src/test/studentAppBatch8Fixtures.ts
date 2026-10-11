import { vi } from "vitest";
import type { AuthUser, StudentQuestion, StudentQuestionAppeal, StudentResult } from "../api";

export const studentUser: AuthUser = {
  id: "student-user-1",
  tenant_id: "tenant-1",
  tenant_code: "school-a",
  username: "student-001",
  display_name: "测试学生",
  roles: ["student"],
  permissions: ["student:grade:read"],
  data_scope: { student_id: "student-1" }
};

export function makeQuestion(overrides: Partial<StudentQuestion> = {}): StudentQuestion {
  return {
    question_id: "question-1",
    question_no: "1",
    subject: "math",
    score: 14,
    max_score: 20,
    correct_answer: "A",
    actual_answer: "B",
    feedback: "第一题公开评分依据",
    ...overrides
  };
}

export function makeResult(questions: StudentQuestion[] = [makeQuestion()], overrides: Partial<StudentResult> = {}): StudentResult {
  return {
    exam_id: "exam-1",
    release_id: "release-7",
    release_version: 7,
    total_score: 82,
    max_score: 100,
    questions,
    appeal_window: {
      open: true,
      allowed_reason_codes: ["recognition_error", "rubric_disagreement"]
    },
    ...overrides
  };
}

export function makeAppeal(overrides: Partial<StudentQuestionAppeal> = {}): StudentQuestionAppeal {
  return {
    id: "appeal-1",
    exam_id: "exam-1",
    source_release_id: "release-7",
    source_release_version: 7,
    question_id: "question-1",
    question_no: "1",
    source_score: 14,
    source_max_score: 20,
    reason_code: "recognition_error",
    reason: "原卷第二步已经写出，请核对识别内容。",
    status: "submitted",
    created_at: "2026-10-01T08:00:00Z",
    updated_at: "2026-10-01T08:00:00Z",
    ...overrides
  };
}

export interface ApiCall {
  url: URL;
  init: RequestInit;
}

export type ApiHandler = (call: ApiCall) => Response | undefined | Promise<Response | undefined>;

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" }
  });
}

export function dataResponse<T>(body: T): Response {
  // JSON.stringify 会把 NaN/Infinity 改成 null；这里直接提供解析结果以覆盖有限数判断。
  return Object.assign(new Response(), { json: async () => body });
}

function requestURL(input: RequestInfo | URL): URL {
  if (typeof input === "string") return new URL(input, "http://portal.test");
  if (input instanceof URL) return input;
  return new URL(input.url, "http://portal.test");
}

function defaultResponse(call: ApiCall, user: AuthUser): Response | undefined {
  if (call.url.pathname === "/api/v1/auth/me") return jsonResponse({ user });
  if (call.url.pathname === "/api/v1/student/exams") return jsonResponse({ exams: [] });
  if (call.url.pathname === "/api/v1/student/question-appeals") return jsonResponse({ appeals: [] });
  if (call.url.pathname.endsWith("/annotations")) return jsonResponse({ annotations: [] });
  return undefined;
}

export function installApi(handler: ApiHandler = () => undefined, user: AuthUser = studentUser): ApiCall[] {
  const calls: ApiCall[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const call = { url: requestURL(input), init };
    calls.push(call);
    const response = await handler(call) ?? defaultResponse(call, user);
    if (response) return response;
    throw new Error(`Unexpected API request: ${init.method ?? "GET"} ${call.url.pathname}`);
  }));
  return calls;
}

export function resultRoute(call: ApiCall, result: StudentResult): Response | undefined {
  if (call.url.pathname === "/api/v1/student/exams/exam-1/result") return dataResponse({ result });
  const question = result.questions?.find((item) => call.url.pathname === `/api/v1/student/exams/exam-1/questions/${encodeURIComponent(item.question_id)}`);
  if (question) return dataResponse({ question });
  return undefined;
}

export function deferredResponse() {
  let resolve: (response: Response) => void = () => { throw new Error("Response promise not initialized"); };
  const promise = new Promise<Response>((fulfill) => { resolve = fulfill; });
  return { promise, resolve };
}
