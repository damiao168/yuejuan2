import { afterEach, describe, expect, it, vi } from "vitest";
import { commandAfterFailure, beginExamCreateCommand } from "../features/exams/create/examCreateCommand";
import { archiveManagementExam, createExamSession, getManagementExam, listAllExams, listAllManagementExams, restoreManagementExam, updateManagementExam, type Exam, type ExamSessionPayload } from "./exams";

afterEach(() => {
  vi.unstubAllGlobals();
});

const payload: ExamSessionPayload = {
  school_id: "school-1", grade_id: "grade-1", name: "期中", exam_type: "midterm",
  grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "after_admin_approval",
  class_ids: ["class-1"], subjects: [{
    subject: "math", total_score: 100, duration_minutes: 90,
    candidate_rule: "all_selected_classes", class_ids: [],
    sections: [{ title: "全卷", question_type: "short_answer", question_count: 10, score_per_question: 10 }]
  }]
};

const exam = (id: string, revision = 1): Exam => ({
  id, tenant_id: "tenant-1", school_id: "school-1", name: id, subject: "math", exam_type: "midterm",
  total_score: 100, status: "ready", grading_mode: "ai_assisted", appeal_enabled: true,
  publish_policy: "after_admin_approval", created_by: "admin-1", class_ids: ["class-1"], revision
});

const examPage = (exams: Exam[], has_more: boolean, next_cursor: string) => new Response(JSON.stringify({ exams, has_more, next_cursor }), {
  headers: { "Content-Type": "application/json" }
});

describe("listAllExams", () => {
  it("reads later pages with the same filters and removes duplicate exam rows", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(examPage(Array.from({ length: 100 }, (_, index) => exam(`exam-${index}`)), true, "page-2"))
      .mockResolvedValueOnce(examPage([exam("exam-99", 2), exam("exam-100")], false, ""));
    vi.stubGlobal("fetch", fetchMock);

    const result = await listAllExams({ school_id: "school-1", status: "ready" });

    expect(result.exams).toHaveLength(101);
    expect(result.exams[99]).toMatchObject({ id: "exam-99", revision: 2 });
    expect(result.exams[100]?.id).toBe("exam-100");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const queries = fetchMock.mock.calls.map(([url]) => Object.fromEntries(new URL(String(url), "http://local").searchParams));
    expect(queries).toEqual([
      { school_id: "school-1", status: "ready", limit: "100" },
      { school_id: "school-1", status: "ready", limit: "100", cursor: "page-2" }
    ]);
  });

  it("stops when has_more is false even if a next cursor remains", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(examPage([exam("exam-1")], false, "unused-cursor"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listAllExams()).resolves.toEqual({ exams: [exam("exam-1")] });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("rejects a missing next cursor instead of returning an incomplete exam list", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(examPage([exam("exam-1")], true, ""));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listAllExams()).rejects.toThrow("考试分页异常");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("rejects a repeated cursor even when it cycles back through another page", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(examPage([exam("exam-1")], true, "page-2"))
      .mockResolvedValueOnce(examPage([exam("exam-2")], true, "page-3"))
      .mockResolvedValueOnce(examPage([exam("exam-3")], true, "page-2"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listAllExams()).rejects.toThrow("考试分页异常");
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("propagates a later page request error instead of returning the first page", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(examPage([exam("exam-1")], true, "page-2"))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "service_unavailable" } }), {
        status: 503, headers: { "Content-Type": "application/json" }
      }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listAllExams()).rejects.toMatchObject({ name: "ApiClientError", status: 503, code: "service_unavailable" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe("createExamSession", () => {
  it("uses the same stable command ID in the header and business payload", async () => {
    const commandId = "exam-session-command-123";
    const fetchMock = vi.fn(async (_url: string | URL | Request, init?: RequestInit) => {
      void init;
      return new Response(JSON.stringify({
        exam_session: { id: "session-1", school_id: "school-1", grade_id: "grade-1", name: "期中", exam_type: "midterm", status: "draft", exams: [] }
      }), { status: 201, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    await createExamSession(payload, commandId);

    const init = fetchMock.mock.calls[0]?.[1];
    const headers = new Headers(init?.headers);
    expect(headers.get("Idempotency-Key")).toBe(commandId);
    expect(JSON.parse(String(init?.body))).toMatchObject({ command_id: commandId, name: "期中" });
  });

  it("retries a lost post-commit response through the real client with the same id and body", async () => {
    const command = beginExamCreateCommand(payload, "stable-command-after-503");
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "idempotency_persist_failed" } }), { status: 503, headers: { "Content-Type": "application/json", "Retry-After": "2" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ exam_session: { id: "session-1", school_id: "school-1", grade_id: "grade-1", name: "期中", exam_type: "midterm", status: "draft", exams: [] } }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    let retained = command;
    try {
      await createExamSession(command.payload, command.commandId);
    } catch (error) {
      retained = commandAfterFailure(command, error)!;
    }
    await createExamSession(retained.payload, retained.commandId);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const requests = fetchMock.mock.calls.map(([, init]) => ({
      key: new Headers(init?.headers).get("Idempotency-Key"),
      body: String(init?.body)
    }));
    expect(requests[0]).toEqual(requests[1]);
    expect(requests[0]?.key).toBe("stable-command-after-503");
  });
});

describe("整场考试 API", () => {
  it("整场分页异常拒绝返回部分范围", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ exams: [], has_more: true }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(listAllManagementExams({ archive: "all" })).rejects.toThrow("考试分页异常");
    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/v1/exam-management?archive=all");
  });

  it("详情与公共编辑以整场 kind/id 定位，名称修改不夹带学科设置", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => new Response(JSON.stringify({ exam: {} }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await getManagementExam("session", "session/1");
    await updateManagementExam({ kind: "session", id: "session/1" }, { name: "期末", expected_revision: 4, expected_subject_revisions: { math: 2, physics: 3 } }, "stable-edit-command");
    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/v1/exam-management/session/session%2F1");
    expect(fetchMock.mock.calls[1][1]?.method).toBe("PATCH");
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual({ name: "期末", expected_revision: 4, expected_subject_revisions: { math: 2, physics: 3 } });
    expect(new Headers(fetchMock.mock.calls[1][1]?.headers).get("Idempotency-Key")).toBe("stable-edit-command");
  });

  it("归档与恢复在请求体和幂等头使用相同命令 ID，传入全部学科版本", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => new Response(JSON.stringify({ exam: {} }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const version = { expected_revision: 4, expected_subject_revisions: { math: 2, physics: 3 } };
    await archiveManagementExam({ kind: "session", id: "session" }, version, "archive-command");
    await restoreManagementExam({ kind: "session", id: "session" }, version, "restore-command");
    for (const [index, action] of ["archive", "restore"].entries()) {
      expect(String(fetchMock.mock.calls[index][0])).toBe(`/api/v1/exam-management/session/session/${action}`);
      expect(JSON.parse(String(fetchMock.mock.calls[index][1]?.body))).toEqual({ ...version, command_id: `${action}-command` });
      expect(new Headers(fetchMock.mock.calls[index][1]?.headers).get("Idempotency-Key")).toBe(`${action}-command`);
    }
  });
});
