// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useScoreWorkspaceData } from "./useScoreWorkspaceData";

const api = vi.hoisted(() => ({
  listExams: vi.fn(), listSubmissions: vi.fn(), listExamGrades: vi.fn(),
  checkExamGradeQuality: vi.fn(), listExamRoster: vi.fn(), listAuditLogs: vi.fn(),
  getScoreReleaseGate: vi.fn(), listScoreReleases: vi.fn(), listRegradeJobs: vi.fn()
}));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: vi.fn() } }) } }));
vi.mock("../../../api/exams", () => ({ listExams: api.listExams }));
vi.mock("../../../api/submissions", () => ({ listSubmissions: api.listSubmissions }));
vi.mock("../../../api/scores", () => ({
  listExamGrades: api.listExamGrades, checkExamGradeQuality: api.checkExamGradeQuality,
  listExamRoster: api.listExamRoster
}));
vi.mock("../../../api/audit", () => ({ listAuditLogs: api.listAuditLogs }));
vi.mock("../../../api/scoreReleases", () => ({
  getScoreReleaseGate: api.getScoreReleaseGate,
  listScoreReleases: api.listScoreReleases, listRegradeJobs: api.listRegradeJobs
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("score workspace controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useScoreWorkspaceData>;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    api.listExams.mockResolvedValue({ exams: [{ id: "exam-a" }, { id: "exam-b" }] });
    api.listSubmissions.mockImplementation(async (examId: string) => ({
      submissions: [{ id: `submission-${examId}` }]
    }));
    api.listExamGrades.mockImplementation(async (examId: string) => ({
      grades: [{ id: `grade-${examId}` }], total: 1, filtered_total: 1,
      all_locked: false, next_cursor: "", has_more: false
    }));
    api.checkExamGradeQuality.mockResolvedValue({ passed: true });
    api.listExamRoster.mockResolvedValue({ roster: null });
    api.listAuditLogs.mockResolvedValue({ audit_logs: [] });
    api.getScoreReleaseGate.mockResolvedValue({ release_gate: null });
    api.listScoreReleases.mockResolvedValue({ score_releases: [] });
    api.listRegradeJobs.mockResolvedValue({ regrade_jobs: [] });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function mount() {
    function Probe() {
      current = useScoreWorkspaceData({
        canManage: true, canReadAudit: true, canReadStudentNames: false,
        initialExamId: "exam-a"
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("keeps one coherent snapshot when quality fails after grades succeed", async () => {
    await mount();
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-a"]);
    api.listExamGrades.mockResolvedValueOnce({
      grades: [{ id: "new-grade" }], total: 1, filtered_total: 1,
      all_locked: false, next_cursor: "", has_more: false
    });
    api.checkExamGradeQuality.mockRejectedValueOnce(new Error("quality unavailable"));
    await act(async () => { await current.loadScores("exam-a"); });
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-a"]);
    expect(current.submissions.map((item) => item.id)).toEqual(["submission-exam-a"]);
    expect(current.error).toBeTruthy();
  });

  it("does not let a stale exam response replace the current exam", async () => {
    const oldQuality = deferred<{ passed: boolean }>();
    api.checkExamGradeQuality.mockImplementationOnce(() => oldQuality.promise)
      .mockResolvedValue({ passed: true });
    await mount();
    await act(async () => { current.setSelectedExamId("exam-b"); });
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-b"]);
    await act(async () => { oldQuality.resolve({ passed: true }); });
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-b"]);
    expect(current.submissions.map((item) => item.id)).toEqual(["submission-exam-b"]);
  });

  it("degrades audit data without discarding required score data", async () => {
    api.listAuditLogs.mockRejectedValueOnce(new Error("audit unavailable"));
    await mount();
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-a"]);
    expect(current.auditLogs).toEqual([]);
    expect(current.error).toBeNull();
  });

  it("appends a grade page without losing the first page", async () => {
    api.listExamGrades.mockResolvedValueOnce({
      grades: [{ id: "first" }], total: 2, filtered_total: 2,
      all_locked: false, next_cursor: "cursor-1", has_more: true
    }).mockResolvedValueOnce({
      grades: [{ id: "second" }], total: 2, filtered_total: 2,
      all_locked: false, next_cursor: "", has_more: false
    });
    await mount();
    await act(async () => { await current.loadMoreGrades(); });
    expect(current.grades.map((grade) => grade.id)).toEqual(["first", "second"]);
    expect(current.gradesHaveMore).toBe(false);
    expect(api.listExamGrades).toHaveBeenLastCalledWith("exam-a", expect.objectContaining({ cursor: "cursor-1" }));
  });
});
