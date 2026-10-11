// @vitest-environment jsdom
import { act, type ChangeEvent, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ColumnType } from "antd/es/table";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { RosterEntry, SubmissionGrade } from "../api/scores";
import type { ScoreActionsPanel } from "../features/score-management/components/ScoreActionsPanel";
import type { ScoreReleaseWorkspace } from "../features/score-management/components/ScoreReleaseWorkspace";
import type { QualityPanel } from "../features/score-management/components/QualityPanel";
import type { useScoreWorkspaceData } from "../features/score-management/hooks/useScoreWorkspaceData";
import { emptyScoreWorkspaceSnapshot } from "../features/score-management/hooks/scoreWorkspaceState";
import { ScoreManagementPage } from "./ScoreManagementPage";

type Workspace = ReturnType<typeof useScoreWorkspaceData>;
type PageProps = ComponentProps<typeof ScoreManagementPage>;
type ActionProps = ComponentProps<typeof ScoreActionsPanel>;
type ReleaseProps = ComponentProps<typeof ScoreReleaseWorkspace>;
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
  actions: vi.fn<(props: ActionProps) => void>(),
  releases: vi.fn<(props: ReleaseProps) => void>(),
  quality: vi.fn<(props: QualityProps) => void>(),
  tables: vi.fn<(props: TableProps) => void>(),
  search: vi.fn<(props: SearchProps) => void>(),
  error: vi.fn(), modal: vi.fn<(options: Confirmation) => void>(),
  finalize: vi.fn(), confirm: vi.fn(), publish: vi.fn(), export: vi.fn(),
  stepUp: vi.fn(), run: vi.fn(), setActioning: vi.fn(), attendance: vi.fn(),
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
vi.mock("../features/score-management/hooks/useScoreReleaseWorkflow", () => ({ useScoreReleaseWorkflow: () => ({}) }));
vi.mock("../features/score-management/hooks/useRegradeWorkflow", () => ({ useRegradeWorkflow: () => ({}) }));
vi.mock("../features/score-management/hooks/useRosterAttendance", () => ({ useRosterAttendance: () => ({ openAttendanceEditor: mocks.attendance }) }));
vi.mock("../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: mocks.stepUp }) }));
vi.mock("../api/scores", () => ({
  finalizeExamGrades: mocks.finalize, confirmExamGrades: mocks.confirm,
  publishExamGrades: mocks.publish, exportExamGrades: mocks.export
}));
vi.mock("../features/score-management/components/ScoreWorkflowModals", () => ({ ScoreWorkflowModals: () => null }));
vi.mock("../features/score-management/components/StudentScorePreviewDialog", () => ({ StudentScorePreviewDialog: () => null }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <p role="status">{label}</p>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <aside role="alert">{message}<button onClick={onRetry}>重试读取</button></aside>
}));
vi.mock("../features/score-management/components/ScoreActionsPanel", () => ({
  ScoreActionsPanel: (props: ActionProps) => {
    mocks.actions(props);
    return <aside data-testid="actions-panel">
      <input aria-label="确认原因" value={props.confirmReason} onChange={(event) => props.setConfirmReason(event.target.value)} />
      <input aria-label="发布原因" value={props.publishReason} onChange={(event) => props.setPublishReason(event.target.value)} />
      <button onClick={props.publish} disabled={!props.canWrite}>公开并锁定基础成绩</button>
    </aside>;
  }
}));
vi.mock("../features/score-management/components/ScoreReleaseWorkspace", () => ({
  ScoreReleaseWorkspace: (props: ReleaseProps) => { mocks.releases(props); return <aside data-testid="release-workspace" />; }
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
  Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <aside role="alert">{message}{description}</aside>,
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
  const actions = () => latest(mocks.actions.mock.calls);
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
    it("身份降级和读取失败可同时提示，教师模式仍省略管理端发布区域", async () => {
      data = { ...data, error: "成绩重读失败", identities: { ...data.identities, error: "身份读取不完整" } };
      await render();
      const alerts = [...host.querySelectorAll('[role="alert"]')];
      expect(alerts.map((item) => item.textContent)).toEqual(["学生身份信息读取不完整身份读取不完整", "成绩重读失败重试读取"]);
      expect(host.querySelector(".score-publication-status")).not.toBeNull();
      expect(host.querySelector('[data-testid="release-workspace"]')).not.toBeNull();
      await click("重试读取");
      expect(mocks.refresh).toHaveBeenCalledTimes(1);
      await render({ mode: "teacher", canManage: false });
      expect(host.querySelector(".score-publication-status")).toBeNull();
      expect(host.querySelector('[data-testid="release-workspace"]')).toBeNull();
      expect(host.querySelector('[data-testid="actions-panel"]')).toBeNull();
      expect(host.querySelectorAll('[role="alert"]')).toHaveLength(2);
      expect(host.querySelector(".score-shell")?.classList.contains("read-only")).toBe(true);
    });

    it("重读时替换三个展示区，读完后保留表格数据引用、分页及加载更多入口", async () => {
      data = { ...data, loadingScores: true, loadingMoreGrades: true };
      await render();
      expect([...host.querySelectorAll('[role="status"]')].map((item) => item.textContent)).toEqual(["正在核对考试名册", "正在读取质量检查", "正在读取成绩"]);
      expect(mocks.tables).not.toHaveBeenCalled();
      expect(mocks.quality).not.toHaveBeenCalled();
      expect(host.querySelector(".load-more-row")).toBeNull();
      data = { ...data, loadingScores: false };
      await render();
      expect(table("key").dataSource).toBe(data.roster!.entries);
      expect(table("id").dataSource).toBe(data.grades);
      expect(table("id").pagination).toEqual({ pageSize: 8, showSizeChanger: false });
      expect(latest(mocks.quality.mock.calls).quality).toBe(data.quality);
      expect(host.textContent?.replace(/\s+/g, "")).toContain("已加载1/1条（全场2条）");
      expect(host.querySelector(".load-more-row button")?.getAttribute("data-loading")).toBe("true");
      await click("加载更多成绩");
      expect(mocks.loadMoreGrades).toHaveBeenCalledTimes(1);
      data = { ...data, gradesHaveMore: false };
      await render();
      expect(host.querySelector(".load-more-row")).toBeNull();
    });
  });

  describe("控件回调与重渲染", () => {
    it("搜索清空和提交沿用顺序，原因更新不会重新挂载成绩搜索控件", async () => {
      await render();
      const originalInput = host.querySelector<HTMLInputElement>('input[aria-label="成绩搜索"]')!;
      await act(async () => search().onChange({ target: { value: "  anonymous-a  " } } as ChangeEvent<HTMLInputElement>));
      await render();
      expect(mocks.order).toEqual(["keyword:  anonymous-a  "]);
      await click("搜索成绩");
      expect(mocks.setAppliedKeyword).toHaveBeenLastCalledWith("anonymous-a");
      await act(async () => { actions().setConfirmReason(" 保留确认原因 "); actions().setPublishReason("保留发布原因"); });
      expect(host.querySelector('input[aria-label="成绩搜索"]')).toBe(originalInput);
      expect(originalInput.value).toBe("  anonymous-a  ");
      expect(host.querySelector<HTMLInputElement>('input[aria-label="确认原因"]')!.value).toBe(" 保留确认原因 ");
      expect(host.querySelector<HTMLInputElement>('input[aria-label="发布原因"]')!.value).toBe("保留发布原因");
      await act(async () => actions().confirmGrades());
      expect(mocks.confirm).toHaveBeenCalledWith("exam /a", "保留确认原因");
      mocks.order = [];
      await act(async () => search().onChange({ target: { value: "" } } as ChangeEvent<HTMLInputElement>));
      await render();
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
        canManage: boundary !== "read-only", canReadStudentNames: false, canReadAudit: false, initialExamId: ""
      });
      expect(data.grades).toHaveLength(1);
      expect(actions().canWrite).toBe(false);
      expect(latest(mocks.releases.mock.calls).canWrite).toBe(false);
    });

    it("真实名册列和质量回调保留 review、arbitration、capture 入口及编码参数", async () => {
      const onNavigate = vi.fn();
      data = { ...data, quality: { stage: "publish", can_publish: false, quality: { passed: false, issues: [
        { code: "unfinished_review_tasks", message: "review", blocking: true, count: 1 },
        { code: "unfinished_arbitration_tasks", message: "arbitration", blocking: true, count: 1 },
        { code: "missing_pages_unresolved", message: "capture", blocking: true, count: 1 }
      ] } } };
      await render({ canManage: false, canHandleReview: true, canHandleArbitration: true, onNavigate });
      expect(host.textContent).toContain("权限受限");
      expect(host.textContent).not.toContain("测试学生");
      expect(host.textContent).not.toContain("定位答卷处理");
      expect(host.textContent).not.toContain("核查答卷与匹配");
      await click("查看阅卷任务"); await click("处理阅卷任务"); await click("处理仲裁任务");
      expect(onNavigate.mock.calls.map(([path]) => path)).toEqual([
        "/exams/exam%20%2Fa/grading?q=paper%20%2Freview", "/exams/exam%20%2Fa/grading", "/arbitration?exam=exam%20%2Fa"
      ]);
      onNavigate.mockClear();
      await render({ canManage: false, canReadStudentNames: true, canHandleCapture: true, onNavigate });
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

  describe("基础成绩发布确认", () => {
    it("窗口打开后重渲染仍使用原考试和原因，确认后先完成二次校验再重读考试", async () => {
      await render();
      await act(async () => actions().setPublishReason(" 原发布原因 "));
      await click("公开并锁定基础成绩");
      const confirmation = mocks.modal.mock.calls[0][0];
      expect(confirmation.title).toBe("确认发布成绩");
      expect(confirmation.content).toContain("第一场考试");
      expect(mocks.publish).not.toHaveBeenCalled();
      expect(mocks.stepUp).not.toHaveBeenCalled();
      // 确认窗口保留打开时的闭包；页面重渲染不能把尚未确认的命令改成另一场考试。
      data = { ...data, selectedExamId: "exam-b", gradeTotal: 9 };
      await render();
      await act(async () => actions().setPublishReason("新发布原因"));
      mocks.order = [];
      await act(async () => confirmation.onOk());
      expect(mocks.publish).toHaveBeenCalledWith("exam /a", "原发布原因");
      expect(mocks.stepUp).toHaveBeenCalledWith(expect.objectContaining({ reason: "正式发布成绩" }));
      expect(mocks.order).toEqual(["action", "step-up", "publish", "step-up-complete", "exams"]);
      expect(mocks.run).toHaveBeenCalledWith("publish", expect.any(Function), "基础成绩已公开并锁定");
    });
  });
});
