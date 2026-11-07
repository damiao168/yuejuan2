// @vitest-environment jsdom
import { act, type ChangeEvent, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ColumnType } from "antd/es/table";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { RosterEntry, SubmissionGrade } from "../api/scores";
import type { ScoreReleaseStatus } from "../features/score-management/components/ScoreReleaseStatus";
import type { ScorePublicationChecklist } from "../features/score-management/components/ScorePublicationChecklist";
import type { QualityPanel } from "../features/score-management/components/QualityPanel";
import type { useScoreWorkspaceData } from "../features/score-management/hooks/useScoreWorkspaceData";
import { emptyScoreWorkspaceSnapshot } from "../features/score-management/hooks/scoreWorkspaceState";
import { ScoreManagementPage } from "./ScoreManagementPage";

type Workspace = ReturnType<typeof useScoreWorkspaceData>;
type PageProps = ComponentProps<typeof ScoreManagementPage>;
type StatusProps = ComponentProps<typeof ScoreReleaseStatus>;
type QualityProps = ComponentProps<typeof QualityPanel>;
type SearchProps = {
  value: string;
  onChange: (event: ChangeEvent<HTMLInputElement>) => void;
  onSearch: (value: string) => void;
};
type TableProps = {
  rowKey: string;
  columns: Array<ColumnType<RosterEntry>> | Array<ColumnType<SubmissionGrade>>;
  dataSource: RosterEntry[] | SubmissionGrade[];
  pagination: { pageSize: number; showSizeChanger: boolean };
  locale: { emptyText: ReactNode };
};
type Confirmation = { title: string; content: ReactNode; onOk: () => Promise<void> };

const mocks = vi.hoisted(() => ({
  workspace: vi.fn<typeof useScoreWorkspaceData>(),
  status: vi.fn<(props: StatusProps) => void>(),
  openPublish: vi.fn(), openCorrection: vi.fn(),
  quality: vi.fn<(props: QualityProps) => void>(),
  checklist: vi.fn<(props: ComponentProps<typeof ScorePublicationChecklist>) => void>(),
  tables: vi.fn<(props: TableProps) => void>(),
  search: vi.fn<(props: SearchProps) => void>(),
  error: vi.fn(), modal: vi.fn<(options: Confirmation) => void>(),
  finalize: vi.fn(), confirm: vi.fn(), publish: vi.fn(), export: vi.fn(), exportReport: vi.fn(), exportOfficial: vi.fn(),
  stepUp: vi.fn(), run: vi.fn(), setActioning: vi.fn(),
  setKeyword: vi.fn(), setAppliedKeyword: vi.fn(), setStatusFilter: vi.fn(),
  setSelectedExamId: vi.fn(), setRoster: vi.fn(), refresh: vi.fn(),
  loadExamList: vi.fn(), loadScores: vi.fn(), loadMoreGrades: vi.fn(),
  actioning: null as string | null,
  order: [] as string[]
}));

vi.mock("../features/score-management/hooks/useScoreWorkspaceData", () => ({ useScoreWorkspaceData: mocks.workspace }));
vi.mock("../features/score-management/hooks/useScoreAction", () => ({
  useScoreAction: () => ({ actioning: mocks.actioning, setActioning: mocks.setActioning, runAction: mocks.run })
}));
vi.mock("../features/score-management/hooks/useScoreReleaseWorkflow", () => ({ useScoreReleaseWorkflow: () => ({ openPublish: mocks.openPublish, openCorrection: mocks.openCorrection }) }));
vi.mock("../features/score-management/hooks/useExamEducationStage", () => ({ useExamEducationStage: () => "senior" }));
vi.mock("../features/score-management/hooks/useSessionPublication", () => ({ useSessionPublication: () => null, sessionPublicationText: () => "" }));
vi.mock("../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: mocks.stepUp }) }));
vi.mock("../api/scores", () => ({
  finalizeExamGrades: mocks.finalize, confirmExamGrades: mocks.confirm,
  publishExamGrades: mocks.publish, exportExamGrades: mocks.export,
  exportExamGradeReport: mocks.exportReport, exportPublishedExamGrades: mocks.exportOfficial
}));
vi.mock("../features/score-management/components/ScorePublishDialog", () => ({ ScorePublishDialog: () => null }));
vi.mock("../features/score-management/components/ScoreCorrectionDialog", () => ({ ScoreCorrectionDialog: () => null }));
vi.mock("../features/score-management/components/ScoreReleaseHistory", () => ({ ScoreReleaseHistory: () => null }));
vi.mock("../features/score-management/components/StudentScorePreviewDialog", () => ({ StudentScorePreviewDialog: () => null }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <p role="status">{label}</p>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <aside role="alert">{message}<button onClick={onRetry}>重试读取</button></aside>
}));
vi.mock("../features/score-management/components/ScoreReleaseStatus", () => ({
  ScoreReleaseStatus: (props: StatusProps) => {
    mocks.status(props);
    return <aside data-testid="release-status"><button onClick={props.onPrimary}>{props.model.primary?.label ?? "无主操作"}</button></aside>;
  }
}));
vi.mock("../features/score-management/components/QualityPanel", () => ({
  QualityPanel: (props: QualityProps) => {
    mocks.quality(props);
    return <div data-testid="quality-panel">{props.quality?.quality.issues.map((issue) => {
      const action = props.actionFor?.(issue.code);
      return action ? <button key={issue.code} onClick={action.run}>{action.label}</button> : null;
    })}</div>;
  }
}));
vi.mock("../features/score-management/components/ScorePublicationChecklist", () => ({ ScorePublicationChecklist: (props: ComponentProps<typeof ScorePublicationChecklist>) => { mocks.checklist(props); return <section aria-label="发布待办" />; } }));
vi.mock("../components/ResponsiveTable", () => ({
  ResponsiveTable: (props: TableProps) => {
    mocks.tables(props);
    // 使用生产列的 render 观察入口与姓名；测试不复制页面的权限分派规则。
    const rows = props.rowKey === "key"
      ? (props.dataSource as RosterEntry[]).map((entry, index) => <div key={entry.key}>{(props.columns[5] as ColumnType<RosterEntry>).render?.(undefined, entry, index) as ReactNode}</div>)
      : (props.dataSource as SubmissionGrade[]).map((grade, index) => <div key={grade.id}>{(props.columns[1] as ColumnType<SubmissionGrade>).render?.(grade.student_id, grade, index) as ReactNode}</div>);
    return <div data-testid={`table-${props.rowKey}`}>{props.dataSource.length ? rows : props.locale.emptyText}</div>;
  }
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { error: mocks.error }, modal: { confirm: mocks.modal } }) },
  Alert: ({ message, description, action }: { message: ReactNode; description: ReactNode; action?: ReactNode }) => <aside role="alert">{message}{description}{action}</aside>,
  Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button disabled={disabled} data-loading={loading} onClick={onClick}>{children}</button>,
  Empty: Object.assign(({ description }: { description: ReactNode }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Input: { Search: (props: SearchProps) => {
    mocks.search(props);
    return <div><input aria-label="成绩搜索" value={props.value} onChange={props.onChange} /><button onClick={() => props.onSearch(props.value)}>搜索成绩</button></div>;
  } },
  Select: ({ className, value, options, onChange, disabled }: {
    className: string; value?: string; options: Array<{ label: string; value: string }>;
    onChange: (value: string) => void; disabled?: boolean;
  }) => <select className={className} value={value ?? ""} disabled={disabled} onChange={(event) => onChange(event.target.value)}>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));

function exam(id: string, name: string): Exam {
  return { id, name, tenant_id: "tenant", school_id: "school", subject: "math", exam_type: "mock",
    total_score: 100, status: "grading", grading_mode: "human_review_required", appeal_enabled: false,
    publish_policy: "manual", created_by: "user", class_ids: [], revision: 1 };
}

function workspaceFixture(): Workspace {
  const grade: SubmissionGrade = { id: "grade-a", tenant_id: "tenant", exam_id: "exam /a",
    submission_id: "paper /review", student_id: "student-a", anonymous_code: "anonymous-a",
    total_score: 73, max_score: 100, status: "confirmed", locked: false, revision: 1,
    created_by: "user", created_at: "", updated_at: "" };
  return {
    ...emptyScoreWorkspaceSnapshot("exam /a"),
    exams: [exam("exam /a", "第一场考试"), exam("exam-b", "第二场考试")],
    selectedExamId: "exam /a", setSelectedExamId: mocks.setSelectedExamId,
    grades: [grade], gradeTotal: 2, filteredGradeTotal: 1, gradesHaveMore: true,
    quality: { stage: "publish", can_publish: true, quality: { passed: true, issues: [] } },
    roster: { entries: [
      { key: "student:review", student_id: "student /review", submission_id: "paper /review",
        status: "unmatched", resolution_code: "grading_incomplete", expected_page_count: 2, actual_page_count: 2 },
      { key: "student:capture", student_id: "student /capture", submission_id: "paper /capture",
        status: "missing_pages", resolution_code: "missing_pages", expected_page_count: 2, actual_page_count: 1 }
    ], summary: { expected: 2, received: 2, graded: 0, absent: 0, unresolved: 2, missing_pages: 1, unidentified: 0 } },
    identities: {
      students: { "student-a": { id: "student-a", tenant_id: "tenant", school_id: "school",
        class_id: "class-a", student_no: "123", name: "测试学生", status: "active" } },
      classes: { "class-a": { id: "class-a", tenant_id: "tenant", school_id: "school",
        grade_id: "grade", name: "测试班级", code: "1", status: "active" } }
    },
    setRoster: mocks.setRoster, keyword: "", setKeyword: mocks.setKeyword,
    appliedKeyword: "", setAppliedKeyword: mocks.setAppliedKeyword,
    statusFilter: "all", setStatusFilter: mocks.setStatusFilter,
    loadingExams: false, loadingScores: false, loadingMoreGrades: false, error: null,
    loadExamList: mocks.loadExamList, loadScores: mocks.loadScores,
    refresh: mocks.refresh, loadMoreGrades: mocks.loadMoreGrades
  };
}

function latest<T>(calls: Array<[T]>): T {
  return calls[calls.length - 1][0];
}

describe("评分管理页第53批展示拆分", () => {
  let data: Workspace;
  let host: HTMLDivElement;
  let root: Root;
  const base: PageProps = { mode: "admin", canManage: true, canReadStudentNames: false, canReadAudit: false };
  const actions = () => latest(mocks.status.mock.calls);
  const search = () => latest(mocks.search.mock.calls);
  const table = (key: string) => mocks.tables.mock.calls.slice().reverse().find(([props]) => props.rowKey === key)![0];

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.actioning = null; mocks.order = [];
    data = workspaceFixture();
    mocks.workspace.mockImplementation(() => data);
    mocks.setKeyword.mockImplementation((value: string) => {
      mocks.order.push(`keyword:${value}`); data = { ...data, keyword: value };
    });
    mocks.setAppliedKeyword.mockImplementation((value: string) => { mocks.order.push(`applied:${value}`); });
    mocks.loadExamList.mockImplementation(async () => { mocks.order.push("exams"); });
    mocks.loadScores.mockResolvedValue(undefined);
    mocks.refresh.mockResolvedValue(undefined);
    mocks.loadMoreGrades.mockResolvedValue(undefined);
    mocks.run.mockImplementation(async (_key: string, action: () => Promise<void>) => {
      mocks.order.push("action"); await action();
    });
    mocks.stepUp.mockImplementation(async ({ action }: { action: () => Promise<void> }) => {
      mocks.order.push("step-up"); await action(); mocks.order.push("step-up-complete");
    });
    mocks.publish.mockImplementation(async () => { mocks.order.push("publish"); });
    mocks.confirm.mockResolvedValue(undefined);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async (props: Partial<PageProps> = {}) => { await act(async () => root.render(<ScoreManagementPage {...base} {...props} />)); };
  const click = async (label: string) => {
    const button = [...host.querySelectorAll("button")].find((item) => item.textContent === label);
    expect(button, label).toBeDefined();
    await act(async () => button!.click());
  };

  describe("展示接线", () => {
    it("移除手工汇总确认入口，前置问题继续定位本场小题", async () => {
      const onNavigate = vi.fn();
      await render({ onNavigate, canHandleCapture: true });
      expect(host.querySelector(".score-summary-strip")).toBeNull();
      expect(host.querySelector("#score-confirmation-step")).toBeNull();
      expect(host.querySelector("#score-preparation")).toBeNull();
      expect(host.querySelector(".score-results-panel")).toBeNull();
      expect(host.querySelector(".score-roster-panel")).toBeNull();
      expect(host.querySelector(".score-quality-panel")).toBeNull();
      latest(mocks.checklist.mock.calls).actionFor("quality_questions_missing", "questions")?.run();
      expect(onNavigate).toHaveBeenCalledWith("/exams/exam%20%2Fa/questions");
      latest(mocks.checklist.mock.calls).actionFor("roster_unresolved", "roster")?.run();
      expect(onNavigate).toHaveBeenLastCalledWith("/exams/exam%20%2Fa/capture");
      onNavigate.mockClear();
      expect(latest(mocks.checklist.mock.calls).actionFor("capture_collection_incomplete", "capture")?.label).toBe("核查答卷与匹配");
      expect(mocks.finalize).not.toHaveBeenCalled();
      expect(mocks.confirm).not.toHaveBeenCalled();
    });
    it("身份降级和读取失败可同时提示，教师模式仍省略管理端发布区域", async () => {
      data = { ...data, error: "成绩重读失败", identities: { ...data.identities, error: "身份读取不完整" } };
      await render();
      const alerts = [...host.querySelectorAll('[role="alert"]')];
      expect(alerts.map((item) => item.textContent)).toEqual(["学生身份信息读取不完整身份读取不完整", "成绩重读失败重试读取"]);
      expect(host.querySelectorAll('[aria-label="成绩发布任务"]')).toHaveLength(1);
      expect(host.querySelector(".score-publication-status")).toBeNull();
      expect(host.querySelector('[data-testid="release-status"]')).not.toBeNull();
      await click("重试读取");
      expect(mocks.refresh).toHaveBeenCalledTimes(1);
      await render({ mode: "teacher", canManage: false });
      expect(host.querySelector(".score-publication-status")).toBeNull();
      expect(host.querySelector('[data-testid="release-status"]')).toBeNull();
      expect(host.querySelectorAll('[role="alert"]')).toHaveLength(2);
      expect(host.querySelector(".score-shell")?.classList.contains("read-only")).toBe(true);
    });

    it("重读时替换三个展示区，读完后保留表格数据引用、分页及加载更多入口", async () => {
      data = { ...data, loadingScores: true, loadingMoreGrades: true };
      await render({ mode: "teacher", canManage: false });
      expect([...host.querySelectorAll('[role="status"]')].map((item) => item.textContent)).toEqual(["正在核对考试名册", "正在读取质量检查", "正在读取成绩"]);
      expect(mocks.tables).not.toHaveBeenCalled();
      expect(mocks.quality).not.toHaveBeenCalled();
      expect(host.querySelector(".load-more-row")).toBeNull();
      data = { ...data, loadingScores: false };
      await render({ mode: "teacher", canManage: false });
      expect(table("key").dataSource).toBe(data.roster!.entries);
      expect(table("id").dataSource).toBe(data.grades);
      expect(table("id").pagination).toBe(false);
      expect(latest(mocks.quality.mock.calls).quality).toBe(data.quality);
      expect(host.textContent?.replace(/\s+/g, "")).toContain("当前显示1条·共1条（全场2条）");
      expect(host.querySelector(".load-more-row button")?.getAttribute("data-loading")).toBe("true");
      await click("加载更多成绩");
      expect(mocks.loadMoreGrades).toHaveBeenCalledTimes(1);
      data = { ...data, gradesHaveMore: false };
      await render({ mode: "teacher", canManage: false });
      expect(host.querySelector(".load-more-row")).toBeNull();
    });
  });

  describe("控件回调与重渲染", () => {
    it("搜索清空和提交沿用顺序，重读仍保留成绩搜索控件", async () => {
      await render({ mode: "teacher", canManage: false });
      const originalInput = host.querySelector<HTMLInputElement>('input[aria-label="成绩搜索"]')!;
      await act(async () => search().onChange({ target: { value: "  anonymous-a  " } } as ChangeEvent<HTMLInputElement>));
      await render({ mode: "teacher", canManage: false });
      expect(mocks.order).toEqual(["keyword:  anonymous-a  "]);
      await click("搜索成绩");
      expect(mocks.setAppliedKeyword).toHaveBeenLastCalledWith("anonymous-a");
      await render({ mode: "teacher", canManage: false });
      expect(host.querySelector('input[aria-label="成绩搜索"]')).toBe(originalInput);
      expect(originalInput.value).toBe("  anonymous-a  ");
      mocks.order = [];
      await act(async () => search().onChange({ target: { value: "" } } as ChangeEvent<HTMLInputElement>));
      await render({ mode: "teacher", canManage: false });
      expect(mocks.order).toEqual(["keyword:", "applied:"]);
      await act(async () => {
        const select = host.querySelector<HTMLSelectElement>(".toolbar-select")!;
        select.value = "confirmed"; select.dispatchEvent(new Event("change", { bubbles: true }));
      });
      expect(mocks.setStatusFilter).toHaveBeenCalledWith("confirmed");
    });
  });

  describe("门禁与权限", () => {
    it.each(["read-only", "loading", "error", "action"] as const)("旧快照在 %s 边界不能恢复写入权限", async (boundary) => {
      if (boundary === "loading") data = { ...data, loadingScores: true };
      if (boundary === "error") data = { ...data, error: "重新读取失败" };
      if (boundary === "action") mocks.actioning = "publish";
      await render({ canManage: boundary !== "read-only" });
      expect(mocks.workspace).toHaveBeenLastCalledWith({
        canManage: boundary !== "read-only", canReadStudentNames: false, canReadAudit: false, initialExamId: "", selectionScope: undefined, resetUnavailableRememberedSelection: false
      });
      expect(data.grades).toHaveLength(1);
      expect(actions().canWrite).toBe(false);
    });

    it("真实名册列和质量回调保留 review、arbitration、capture 入口及编码参数", async () => {
      const onNavigate = vi.fn();
      data = { ...data, quality: { stage: "publish", can_publish: false, quality: { passed: false, issues: [
        { code: "unfinished_review_tasks", message: "review", blocking: true, count: 1 },
        { code: "unfinished_arbitration_tasks", message: "arbitration", blocking: true, count: 1 },
        { code: "missing_pages_unresolved", message: "capture", blocking: true, count: 1 }
      ] } } };
      await render({ mode: "teacher", canManage: false, canHandleReview: true, canHandleArbitration: true, onNavigate });
      expect(host.textContent).toContain("权限受限");
      expect(host.textContent).not.toContain("测试学生");
      expect(host.textContent).not.toContain("定位答卷处理");
      expect(host.textContent).not.toContain("核查答卷与匹配");
      await click("查看阅卷任务"); await click("处理阅卷任务"); await click("处理仲裁任务");
      expect(onNavigate.mock.calls.map(([path]) => path)).toEqual([
        "/exams/exam%20%2Fa/grading?q=paper%20%2Freview", "/exams/exam%20%2Fa/grading", "/arbitration?exam=exam%20%2Fa"
      ]);
      onNavigate.mockClear();
      await render({ mode: "teacher", canManage: false, canReadStudentNames: true, canHandleCapture: true, onNavigate });
      expect(host.textContent).toContain("测试学生");
      expect(host.textContent).not.toContain("查看阅卷任务");
      expect(host.textContent).not.toContain("处理阅卷任务");
      expect(host.textContent).not.toContain("处理仲裁任务");
      await click("定位答卷处理"); await click("核查答卷与匹配");
      expect(onNavigate.mock.calls.map(([path]) => path)).toEqual([
        "/exams/exam%20%2Fa/capture?submission=paper%20%2Fcapture&student=student%20%2Fcapture", "/exams/exam%20%2Fa/capture"
      ]);
    });
  });

  describe("学生端发布状态", () => {
    const releasedAt = "2026-10-31T13:30:00Z";
    const release = (id: string, version: number, status: "draft" | "published", extra: Record<string, unknown> = {}) =>
      ({ id, version, status, source: "initial", reason: "", visibility_policy: {}, appeal_window: { enabled: false }, published_at: status === "published" ? releasedAt : null, ...extra }) as unknown as Workspace["scoreReleases"][number];

    // 阅卷已完成但尚未生成总分的答卷由发布时自动汇总；只剩这种情况时主按钮应直接是“发布成绩”。
    it("只剩等待自动汇总的答卷时显示检查已通过，主按钮打开发布弹窗", async () => {
      data = { ...data,
        releaseGate: { version: "1", passed: true, blocking: [], warnings: [], counts: {}, generated_at: "" },
        roster: { entries: [{ key: "student:a", student_id: "a", submission_id: "paper-a", status: "submitted", resolution_code: "awaiting_aggregation", expected_page_count: 2, actual_page_count: 2 }],
          summary: { expected: 1, received: 1, graded: 0, absent: 0, unresolved: 0, missing_pages: 0, unidentified: 0 } } };
      await render();
      expect(actions().model).toMatchObject({ text: "未发布 · 检查已通过", primary: { kind: "publish", label: "发布成绩" } });
      await click("发布成绩");
      expect(mocks.openPublish).toHaveBeenCalledTimes(1);
    });

    // 复评任务完成后状态不变；只有对应结果版本已发布，才不再算待发布更正。
    it("已完成复评的结果版本未发布时主按钮为发布更正，已发布后改为导出", async () => {
      const job = { id: "job-1", status: "ready_for_release", reason_text: "答案更正", affected_count: 2, updated_at: "2026-10-31T12:00:00Z" } as unknown as Workspace["regradeJobs"][number];
      const current = release("r1", 1, "published");
      data = { ...data, currentPublishedRelease: current, regradeJobs: [job], scoreReleases: [current, release("r2", 2, "draft", { source: "regrade", regrade_job_id: "job-1" })] };
      await render();
      expect(actions().model.primary).toEqual({ kind: "correction", label: "发布更正" });
      await click("发布更正");
      expect(mocks.openCorrection).toHaveBeenCalledWith(expect.objectContaining({ job, draft: expect.objectContaining({ id: "r2" }) }));
      data = { ...data, scoreReleases: [current, release("r2", 2, "published", { source: "regrade", regrade_job_id: "job-1" })] };
      await render();
      expect(actions().model.primary).toEqual({ kind: "export", label: "导出教师成绩表" });
    });

    it("从复核申请跳来且复评仍在进行时，引导到复核与异常页的对应任务", async () => {
      const onNavigate = vi.fn();
      const original = window.location.pathname + window.location.search;
      window.history.replaceState(null, "", "/admin/scores?appeal=appeal-1&regrade=job-2");
      try {
        data = { ...data, regradeJobs: [{ id: "job-2", status: "running" } as unknown as Workspace["regradeJobs"][number]] };
        await render({ onNavigate });
        expect(host.textContent).toContain("本次申请的复评仍在进行");
        expect(actions().activeRegrades).toBe(1);
        await click("前往复评");
        expect(onNavigate).toHaveBeenLastCalledWith("/exams/exam%20%2Fa/quality?regrade=job-2");
      } finally {
        window.history.replaceState(null, "", original);
      }
    });
  });

  describe("正式教师成绩表导出", () => {
    it("使用正式快照接口，并保留当前考试归属判断", async () => {
      mocks.exportOfficial.mockReturnValue(new Promise(() => undefined));
      await render({ canReadAudit: true });
      await act(async () => actions().onExport());
      expect(mocks.exportOfficial).toHaveBeenCalledWith("exam /a", expect.any(AbortSignal));
      expect(mocks.run).toHaveBeenCalledWith("export-official", expect.any(Function), "教师成绩表已导出，班级平均分在每张表最下面", expect.any(Function));
      expect(mocks.workspace).toHaveBeenLastCalledWith(expect.objectContaining({ canReadAudit: false }));
      expect(mocks.export).not.toHaveBeenCalled();
      expect(mocks.exportReport).not.toHaveBeenCalled();
    });

    it("切换考试后不下载上一场考试迟到的文件", async () => {
      let resolve!: (value: { blob: Blob; contentType: string }) => void;
      mocks.exportOfficial.mockReturnValue(new Promise(done => { resolve = done; }));
      const download = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
      const originalCreate = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
      const createURL = vi.fn(() => "blob:synthetic");
      Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createURL });
      try {
        await render({ selectionScope: "school:admin" });
        await act(async () => actions().onExport());
        const oldOperation = mocks.run.mock.results[0].value as Promise<void>;
        data = { ...data, selectedExamId: "exam-b" };
        await render({ selectionScope: "school:admin" });
        await act(async () => { resolve({ blob: new Blob(["old exam"]), contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }); await oldOperation; });
        expect(download).not.toHaveBeenCalled();
        expect(createURL).not.toHaveBeenCalled();
        const isCurrent = mocks.run.mock.calls[0][3] as () => boolean;
        expect(isCurrent()).toBe(false);
      } finally {
        download.mockRestore();
        if (originalCreate) Object.defineProperty(URL, "createObjectURL", originalCreate);
        else Reflect.deleteProperty(URL, "createObjectURL");
      }
    });
  });
});
