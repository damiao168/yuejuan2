// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useScoreWorkspaceData } from "./useScoreWorkspaceData";

const api = vi.hoisted(() => ({
  listExams: vi.fn(), listSubmissions: vi.fn(), listExamGrades: vi.fn(),
  checkExamGradeQuality: vi.fn()
}));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: vi.fn() } }) } }));
vi.mock("../../../api/exams", () => ({ listAllExams: api.listExams }));
vi.mock("../../../api/submissions", () => ({ listSubmissions: api.listSubmissions }));
vi.mock("../../../api/scores", () => ({
  listExamGrades: api.listExamGrades, checkExamGradeQuality: api.checkExamGradeQuality,
  listExamRoster: vi.fn()
}));
vi.mock("../../../api/audit", () => ({ listAuditLogs: vi.fn() }));
vi.mock("../../../api/scoreReleases", () => ({
  getScoreReleaseGate: vi.fn(), listScoreReleases: vi.fn(),
  getCurrentPublishedScoreRelease: vi.fn(), listRegradeJobs: vi.fn()
}));

type Options = Parameters<typeof useScoreWorkspaceData>[0];
let current: ReturnType<typeof useScoreWorkspaceData>;
let options: Options;
function Probe() {
  current = useScoreWorkspaceData(options);
  return null;
}

describe("成绩页的考试选择恢复", () => {
  let root: Root;
  let container: HTMLDivElement;

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.history.replaceState({ route: "scores" }, "", "/admin/scores");
    window.sessionStorage.clear();
    api.listExams.mockResolvedValue({ exams: [
      { id: "exam-a", status: "grading" }, { id: "exam /b", status: "grading" }
    ] });
    api.listSubmissions.mockResolvedValue({ submissions: [] });
    api.listExamGrades.mockImplementation(async (examId: string) => ({
      grades: [{ id: `grade-${examId}` }], total: 1, filtered_total: 1,
      all_locked: false, next_cursor: "", has_more: false
    }));
    api.checkExamGradeQuality.mockResolvedValue({ quality: { passed: true, issues: [] }, can_publish: true });
    options = { canManage: false, canReadAudit: false, canReadStudentNames: false, initialExamId: "",
      selectionScope: "tenant-a:user-a:admin" };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function mount(next: Partial<Options> = {}) {
    options = { ...options, ...next };
    await act(async () => root.render(<Probe />));
  }

  async function remount() {
    await act(async () => root.unmount());
    root = createRoot(container);
    await mount();
  }

  it("切换非首场只替换恢复地址，保留其他查询和浏览器历史状态", async () => {
    window.history.replaceState({ route: "scores" }, "", "/admin/scores?status=grading&extra=keep");
    await mount();
    const historyLength = window.history.length;
    await act(async () => current.setSelectedExamId("exam /b"));
    expect(current.selectedExamId).toBe("exam /b");
    expect(new URLSearchParams(window.location.search).get("exam")).toBe("exam /b");
    expect(new URLSearchParams(window.location.search).get("status")).toBe("grading");
    expect(new URLSearchParams(window.location.search).get("extra")).toBe("keep");
    expect(window.history.state).toEqual({ route: "scores" });
    expect(window.history.length).toBe(historyLength);
  });

  it("直接打开带考试的成绩地址时读取指定考试，不先显示首场成绩", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=exam%20%2Fb");
    await mount();
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
    expect(api.listExamGrades.mock.calls.some(([id]) => id === "exam-a")).toBe(false);
  });

  it("离开小题页面后浏览器返回，重建成绩页仍恢复离开前的考试", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    // 导航会卸载成绩页；返回时从历史地址重建，不能依赖旧组件还留着 state。
    await act(async () => root.unmount());
    window.history.pushState(null, "", "/admin/exams/exam%20%2Fb/questions");
    await new Promise<void>((resolve) => {
      window.addEventListener("popstate", () => resolve(), { once: true });
      window.history.back();
    });
    root = createRoot(container);
    await mount();
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
  });

  it("刷新重建组件后仍恢复最近选择的非首场考试", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    await remount();
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
  });

  it("从其他模块点击成绩菜单的裸地址，也恢复该用户最近选中的考试", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    window.history.replaceState(null, "", "/admin/scores");
    await remount();
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
  });

  it("显式考试地址优先于该用户上次的考试记忆", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    window.history.replaceState(null, "", "/admin/scores?exam=exam-a");
    await remount();
    expect(current.selectedExamId).toBe("exam-a");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-a"]);
  });

  it("裸地址的最近选择按学校、用户和工作区隔离", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    for (const [selectionScope, path] of [["tenant-a:user-b:admin", "/admin/scores"],
      ["tenant-b:user-a:admin", "/admin/scores"], ["tenant-a:user-a:teacher", "/teacher/scores"]]) {
      window.history.replaceState(null, "", path);
      options = { ...options, selectionScope };
      await remount();
      expect(current.selectedExamId).toBe("exam-a");
    }
    window.history.replaceState(null, "", "/admin/scores");
    options = { ...options, selectionScope: "tenant-a:user-a:admin" };
    await remount();
    expect(current.selectedExamId).toBe("exam /b");
  });

  it("入口指定第一场后手动切换，重读考试目录不能又抢回入口考试", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=exam-a&status=grading");
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    await act(async () => current.refresh());
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
  });

  it("考试内嵌页以路由考试为准，不受通用成绩页查询参数影响", async () => {
    window.history.replaceState(null, "", "/admin/exams/exam-a/scores?exam=exam%20%2Fb");
    await mount({ initialExamId: "exam-a" });
    expect(current.selectedExamId).toBe("exam-a");
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam-a"]);
    expect(new URLSearchParams(window.location.search).get("exam")).toBe("exam /b");
    await mount({ initialExamId: "exam /b" });
    expect(current.selectedExamId).toBe("exam /b");
  });

  it("地址指定的考试不可见时清空成绩并提示重选，不改成首场考试", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=removed-exam");
    await mount();
    expect(current.selectedExamId).toBe("");
    expect(current.grades).toEqual([]);
    expect(current.error).toContain("考试不存在或当前无权查看");
    expect(api.listExamGrades.mock.calls.some(([id]) => id === "exam-a")).toBe(false);
  });

  it("不可见考试的提示出现后可以手动重选有效考试并恢复读取", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=removed-exam");
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.error).toBeNull();
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
    expect(new URLSearchParams(window.location.search).get("exam")).toBe("exam /b");
  });

  it("记住的考试不再可见时清空成绩，不能静默展示另一场考试", async () => {
    await mount();
    await act(async () => current.setSelectedExamId("exam /b"));
    api.listExams.mockResolvedValue({ exams: [{ id: "exam-a", status: "grading" }] });
    window.history.replaceState(null, "", "/admin/scores");
    await remount();
    expect(current.selectedExamId).toBe("");
    expect(current.grades).toEqual([]);
    expect(current.error).toContain("考试不存在或当前无权查看");
  });

  it("学校管理员裸入口清理外校旧记忆，先核对本校目录再选择本校考试", async () => {
    window.sessionStorage.setItem("edugrade.score-exam.tenant-a:user-a:admin", "foreign-exam");
    await mount({ resetUnavailableRememberedSelection: true });
    expect(current.selectedExamId).toBe("exam-a");
    expect(window.sessionStorage.getItem("edugrade.score-exam.tenant-a:user-a:admin")).toBe("exam-a");
    expect(api.listExamGrades.mock.calls.some(([id]) => id === "foreign-exam")).toBe(false);
    expect(api.listSubmissions.mock.calls.some(([id]) => id === "foreign-exam")).toBe(false);
    expect(api.checkExamGradeQuality.mock.calls.some(([id]) => id === "foreign-exam")).toBe(false);
  });

  it("学校管理员显式外校深链不被静默替换，也不请求外校成绩", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=foreign-exam");
    await mount({ resetUnavailableRememberedSelection: true });
    expect(current.selectedExamId).toBe("");
    expect(current.error).toContain("考试不存在或当前无权查看");
    expect(api.listExamGrades).not.toHaveBeenCalled();
    expect(api.listSubmissions).not.toHaveBeenCalled();
  });

  it("同一路径的后退前进事件会恢复对应查询考试，不依赖组件重挂载", async () => {
    await mount();
    for (const examId of ["exam /b", "exam-a", "exam /b"]) {
      await act(async () => {
        window.history.replaceState(null, "", `/admin/scores?exam=${encodeURIComponent(examId)}`);
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      expect(current.selectedExamId).toBe(examId);
      expect(current.grades.map((grade) => grade.id)).toEqual([`grade-${examId}`]);
    }
  });

  it("刷新目录期间切换考试，刷新不能再按旧闭包读取另一场考试", async () => {
    await mount();
    let resolveExams!: (result: { exams: Array<{ id: string; status: string }> }) => void;
    api.listExams.mockImplementationOnce(() => new Promise((resolve) => { resolveExams = resolve; }));
    api.listExamGrades.mockClear();
    let refreshing!: Promise<void>;
    await act(async () => { refreshing = current.refresh(); });
    await act(async () => current.setSelectedExamId("exam /b"));
    await act(async () => {
      resolveExams({ exams: [{ id: "exam-a", status: "grading" }, { id: "exam /b", status: "grading" }] });
      await refreshing;
    });
    expect(current.selectedExamId).toBe("exam /b");
    expect(current.loadingScores).toBe(false);
    expect(current.grades.map((grade) => grade.id)).toEqual(["grade-exam /b"]);
    expect(api.listExamGrades.mock.calls.some(([id]) => id === "exam-a")).toBe(false);
  });

  it("刷新发现当前考试已不可见时清空成绩，不再发起旧考试的成绩读取", async () => {
    window.history.replaceState(null, "", "/admin/scores?exam=exam%20%2Fb");
    await mount();
    api.listExams.mockResolvedValue({ exams: [{ id: "exam-a", status: "grading" }] });
    api.listExamGrades.mockClear();
    await act(async () => current.refresh());
    expect(current.selectedExamId).toBe("");
    expect(current.grades).toEqual([]);
    expect(current.error).toContain("考试不存在或当前无权查看");
    expect(api.listExamGrades).not.toHaveBeenCalled();
  });

  it("成绩目录晚到且旧页面尚未卸载时，不能向小题地址追加考试参数", async () => {
    let resolveExams!: (result: { exams: Array<{ id: string; status: string }> }) => void;
    api.listExams.mockImplementationOnce(() => new Promise((resolve) => { resolveExams = resolve; }));
    await mount();
    window.history.pushState({ route: "questions" }, "", "/admin/exams/exam%20%2Fb/questions?target=answer");
    await act(async () => resolveExams({ exams: [{ id: "exam-a", status: "grading" }, { id: "exam /b", status: "grading" }] }));
    expect(window.location.pathname).toBe("/admin/exams/exam%20%2Fb/questions");
    expect(window.location.search).toBe("?target=answer");
    expect(window.history.state).toEqual({ route: "questions" });
  });
});
