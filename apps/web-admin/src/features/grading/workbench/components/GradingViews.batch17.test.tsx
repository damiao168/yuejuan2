// @vitest-environment jsdom
import { act, type Key, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AiGrade, ReviewTask } from "../../../../api/review";
import { EvidenceInspector, type EvidenceInspectorProps } from "./EvidenceInspector";
import { TaskRail, type TaskRailProps } from "./TaskRail";

vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  type Props = Record<string, unknown>;
  const container = (props: Props) => React.createElement("div", {}, props.children as ReactNode);
  const List = Object.assign((props: Props) => {
    const items = (props.dataSource ?? []) as unknown[];
    const renderItem = props.renderItem as (item: unknown) => ReactNode;
    return React.createElement("div", {}, items.map((item, index) => React.createElement("div", { key: index }, renderItem(item))), props.loadMore as ReactNode);
  }, { Item: (props: Props) => React.createElement("div", { className: props.className as string }, props.children as ReactNode) });
  return {
    List,
    Button: (props: Props) => React.createElement("button", { disabled: Boolean(props.disabled), onClick: props.onClick as () => void }, props.children as ReactNode),
    Checkbox: (props: Props) => React.createElement("label", {}, React.createElement("input", {
      type: "checkbox", checked: Boolean(props.checked), disabled: Boolean(props.disabled),
      "aria-label": props["aria-label"] as string,
      onClick: props.onClick as () => void,
      onChange: props.onChange as () => void
    }), props.children as ReactNode),
    Input: () => null, Select: () => null, Segmented: () => null,
    Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Space: container, Tooltip: container, Tabs: () => null
  };
});
vi.mock("../../../../components/StatusTag", () => ({ StatusTag: ({ tone, children }: { tone: string; children: ReactNode }) => <span data-tone={tone}>{children}</span> }));
vi.mock("../../../../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <div>{label}</div>,
  EmptyState: ({ title }: { title: string }) => <div>{title}</div>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <div>{message}<button onClick={onRetry}>重试</button></div>
}));
vi.mock("../../../../components/ResponsiveTable", () => ({
  // 响应式布局由共享表格测试覆盖；这里保留真实分配表的列渲染、选择回调和禁用条件。
  ResponsiveTable: ({ dataSource, columns, rowSelection, loading }: {
    dataSource: ReviewTask[];
    columns: { title?: ReactNode; render?: (value: unknown, task: ReviewTask, index: number) => ReactNode }[];
    rowSelection: { selectedRowKeys: Key[]; getCheckboxProps: (task: ReviewTask) => { disabled: boolean }; onChange: (keys: Key[]) => void };
    loading: boolean;
  }) => <table aria-label="分配任务表" aria-busy={loading}><tbody>{dataSource.map((item, index) => <tr key={item.id}>
    <td><input type="checkbox" aria-label={`选择任务 ${item.id}`} checked={rowSelection.selectedRowKeys.includes(item.id)}
      disabled={rowSelection.getCheckboxProps(item).disabled} onChange={event => rowSelection.onChange(event.target.checked
        ? [...rowSelection.selectedRowKeys, item.id] : rowSelection.selectedRowKeys.filter(key => key !== item.id))} /></td>
    {columns.map((column, columnIndex) => <td key={columnIndex}>{column.render?.(undefined, item, index)}</td>)}
  </tr>)}</tbody></table>
}));
vi.mock("../ReviewContextInspector", () => ({ ReviewContextInspector: () => null }));
vi.mock("../MathEvidenceInspector", () => ({ MathEvidenceInspector: () => null }));
vi.mock("./MathRubricMatrix", () => ({ MathRubricMatrix: () => null }));

function task(overrides: Partial<ReviewTask> = {}): ReviewTask {
  return {
    id: "task", tenant_id: "tenant", exam_id: "exam", question_id: "question", question_no: "Q1",
    answer_segment_id: "segment", submission_id: "submission", anonymous_code: "匿名001", source: "manual_sample",
    status: "pending", priority: 20, grade_round: "first", revision: 1, created_by: "teacher", created_at: "", updated_at: "",
    subject: "mathematics", ...overrides
  };
}

function rail(): TaskRailProps {
  const item = task();
  return {
    scope: { effectiveExamId: "exam", subjectFilter: "", questionFilter: "", unassignedOnly: false, subjectOptions: [], examOptions: [], questionOptions: [], matchedAggregate: null, unassignedCount: 1, selectingAll: false, selectAllMatchingAssignments: vi.fn(async () => undefined) },
    fixedExam: false, onExamChange: vi.fn(), onSubjectChange: vi.fn(), onQuestionChange: vi.fn(), onUnassignedChange: vi.fn(),
    canManageTasks: true, canWork: true, queueScope: "all", taskFilter: "all", keyword: "", tasks: [item], filteredTasks: [item], assignableTasks: [item],
    assignmentTaskIds: [], assignmentUserId: "", graderOptions: [], graderNames: {}, gradersError: null, selectedTaskId: "task",
    loadingTasks: false, taskError: null, hasMoreTasks: false, pageError: null, loadingMoreTasks: false, actioning: null,
    onQueueScopeChange: vi.fn(), onTaskFilterChange: vi.fn(), onKeywordChange: vi.fn(), onAssignmentUserChange: vi.fn(),
    onSelectAllAssignments: vi.fn(), onToggleAssignment: vi.fn(), onPreviewAssignments: vi.fn(), onSelectTask: vi.fn(),
    onLoadTasks: vi.fn(async () => undefined), onLoadMoreTasks: vi.fn(async () => [])
  };
}

function evidence(overrides: Partial<AiGrade> = {}): EvidenceInspectorProps {
  const grade: AiGrade = {
    id: "ai", tenant_id: "tenant", answer_segment_id: "segment", question_id: "question", question_no: "Q1", question_type: "short_answer",
    grader_type: "ai", suggested_score: 2, max_score: 5, confidence: 0.9, matched_points: [], missing_points: [], evidence: [], risk_flags: [],
    needs_human_review: false, mock: false, created_by: "teacher", created_at: "", ...overrides
  };
  // 子工作区在此测试中替代；夹具只包含目标展示实际读取的冻结上下文字段。
  const context = {
    task: task(), reviewContext: { subject_tool_hints: { subject_code: "history" }, question_snapshot: { risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } } }, aiGrades: [grade]
  } as unknown as EvidenceInspectorProps["context"];
  return {
    context, draft: { score: null, comments: "", privateNote: "", studentFeedback: "", reason: "", disputeReason: "", rubricSelections: {}, answerText: "答案" },
    selectedGrade: grade, maxScore: 5, canEditDraft: false, canVerifyEvidence: false, actioning: null, onVerifyEvidence: vi.fn(async () => undefined),
    math: { enabled: false, state: { segmentId: "segment", phase: "unavailable", understanding: null, score: null, message: "" }, dirty: false, setDirty: vi.fn(), refresh: vi.fn(async () => null), correctionSaved: vi.fn() },
    onRefreshMath: vi.fn(), onSelectMathStep: vi.fn(), canRequestMath: false, requestingMath: false, onRequestMath: vi.fn(), mathRequestNotice: ""
  };
}

describe("阅卷展示分支回归", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); });
  const render = async (element: ReactNode) => { await act(async () => root.render(element)); };

  it("教师任务导航保留加载、错误、空列表和任务列表的优先顺序", async () => {
    const props = { ...rail(), canManageTasks: false, queueScope: "mine" as const };
    await render(<TaskRail {...props} loadingTasks taskError="读取失败" />);
    expect(host.textContent).toContain("正在读取阅卷任务");
    expect(host.querySelector(".grading-task-item")).toBeNull();
    await render(<TaskRail {...props} taskError="读取失败" />);
    expect(host.textContent).toContain("读取失败");
    await act(async () => (Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "重试")!).click());
    expect(props.onLoadTasks).toHaveBeenCalledOnce();
    await render(<TaskRail {...props} filteredTasks={[]} />);
    expect(host.textContent).toContain("暂无阅卷任务");
    await render(<TaskRail {...props} />);
    expect(host.querySelector(".grading-task-item.active")).not.toBeNull();
    expect(host.textContent).toContain("数学");
  });

  it("管理员全部队列使用分配表，并沿用选择、查看和分配回调", async () => {
    const props = rail();
    await render(<TaskRail {...props} />);
    expect(host.querySelector(".grading-assignment-workspace")).not.toBeNull();
    expect(host.querySelector(".grading-task-rail")).toBeNull();
    const checkbox = host.querySelector<HTMLInputElement>('input[aria-label="选择任务 task"]')!;
    await act(async () => checkbox.click());
    expect(props.onToggleAssignment).toHaveBeenCalledWith("task", true);
    await act(async () => (Array.from(host.querySelectorAll("button")).find(button => button.textContent === "查看答卷")!).click());
    expect(props.onSelectTask).toHaveBeenCalledWith("task");
    await render(<TaskRail {...props} assignmentTaskIds={["task"]} />);
    await act(async () => (Array.from(host.querySelectorAll("button")).find(button => button.textContent === "分配给教师")!).click());
    expect(props.onPreviewAssignments).toHaveBeenCalledOnce();
    await act(async () => (Array.from(host.querySelectorAll("button")).find(button => button.textContent === "选择筛选结果中的全部待分配项")!).click());
    expect(props.scope.selectAllMatchingAssignments).toHaveBeenCalledOnce();
  });

  it("管理者待我处理和教师队列仍使用旧任务导航，不出现分配表", async () => {
    const props = rail();
    await render(<TaskRail {...props} queueScope="mine" />);
    expect(host.querySelector(".grading-task-rail")).not.toBeNull();
    expect(host.querySelector(".grading-assignment-workspace")).toBeNull();
    await render(<TaskRail {...props} canManageTasks={false} />);
    expect(host.querySelector(".grading-task-select")).not.toBeNull();
    expect(host.querySelector('input[aria-label="选择 Q1 匿名001"]')).toBeNull();
  });

  it("分配表保留分页重试，不可分配任务不能选择，操作期间不能查看或分配", async () => {
    const props = rail();
    await render(<TaskRail {...props} filteredTasks={[task({ source: "double_mark_required" })]} assignableTasks={[]} hasMoreTasks pageError="分页失败" />);
    expect(host.querySelector<HTMLInputElement>('input[aria-label="选择任务 task"]')!.disabled).toBe(true);
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("分页失败");
    const more = Array.from(host.querySelectorAll("button")).find(button => button.textContent === "加载更多任务")!;
    await act(async () => more.click());
    expect(props.onLoadMoreTasks).toHaveBeenCalledOnce();
    await render(<TaskRail {...props} actioning="assign-tasks" assignmentTaskIds={["task"]} />);
    expect(host.querySelector<HTMLInputElement>('input[aria-label="选择任务 task"]')!.disabled).toBe(true);
    for (const label of ["查看答卷", "分配给教师"]) {
      expect((Array.from(host.querySelectorAll("button")).find(button => button.textContent === label)!).disabled).toBe(true);
    }
  });

  it("分配表用一个安排状态解释等待管理员还是等待教师", async () => {
    const props = rail();
    await render(<TaskRail {...props} />);
    const row = host.querySelector("tr")!;
    expect(row.textContent).toContain("未安排教师");
    expect(row.textContent).toContain("等待管理员选择教师并分配");
    expect(row.textContent).not.toContain("待分配");
    await render(<TaskRail {...props} filteredTasks={[task({ assigned_to: "teacher", status: "assigned" })]} graderNames={{ teacher: "张老师" }} />);
    expect(host.querySelector("tr")?.textContent).toContain("张老师 · 待评分");
    expect(host.querySelector("tr")?.textContent).toContain("等待教师处理答卷");
  });

  it("教师导航分页错误仍允许重试，固定双评任务保持不可改派", async () => {
    const props = { ...rail(), queueScope: "mine" as const };
    await render(<TaskRail {...props} filteredTasks={[task({ source: "double_mark_required" })]} hasMoreTasks pageError="分页失败" />);
    expect(host.querySelector<HTMLInputElement>('input[aria-label="选择 Q1 匿名001"]')!.disabled).toBe(true);
    const more = Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "重试加载更多")!;
    await act(async () => more.click());
    expect(props.onLoadMoreTasks).toHaveBeenCalledOnce();
    await render(<TaskRail {...props} actioning="assign-tasks" />);
    expect(host.querySelector<HTMLButtonElement>(".grading-task-select")!.disabled).toBe(true);
  });

  it.each([
    [{ needs_human_review: true }, "warning", "需人工复核"],
    [{ mock: true }, "warning", "未标记风险"],
    [{ confidence: 0.9 }, "success", "未标记风险"],
    [{ confidence: 0.5 }, "danger", "未标记风险"]
  ] as const)("保持判定颜色与原标签规则 %j", async (overrides, tone, label) => {
    await render(<EvidenceInspector {...evidence(overrides)} />);
    const decision = Array.from(host.querySelectorAll(".ai-score-strip > div")).find((item) => item.firstElementChild?.textContent === "判定")!;
    expect(decision.querySelector("[data-tone]")?.getAttribute("data-tone")).toBe(tone);
    expect(decision.textContent).toContain(label);
  });

  it("历史数学建议原因优先于人工复核标签，请求提示使用原生状态输出", async () => {
    const props = evidence({ needs_human_review: true });
    props.context.reviewContext.subject_tool_hints.subject_code = "mathematics";
    await render(<EvidenceInspector {...props} mathRequestNotice="正在确认数学证据" />);
    expect(host.textContent).toContain("缺少数学版本绑定");
    expect(host.textContent).toContain("历史建议 · 不可采纳");
    expect(host.querySelector("output.math-suggestion-note")?.textContent).toBe("正在确认数学证据");
    expect(host.querySelector('p[role="status"]')).toBeNull();
  });
});
