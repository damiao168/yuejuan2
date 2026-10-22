// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { QualityDashboard } from "../api/qualityDashboard";
import type { BackmarkBatch, BackmarkSummary } from "../api/backmark";
import { QualityDashboardPage } from "./QualityDashboardPage";

const mocks = vi.hoisted(() => ({
  dashboard: vi.fn(), seed: vi.fn(), saveSeed: vi.fn(), batches: vi.fn(), users: vi.fn(), summary: vi.fn(), releases: vi.fn(),
  preview: vi.fn(), create: vi.fn(), regradePreview: vi.fn(), regradeCreate: vi.fn(), incidents: vi.fn(), resolve: vi.fn(), recompute: vi.fn(),
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  forms: [] as Array<{ setFieldsValue: (values: Record<string, unknown>) => void; getFieldsValue: () => Record<string, unknown> }>
}));
vi.mock("../api/qualityDashboard", () => ({ getExamQualityDashboard: mocks.dashboard }));
vi.mock("../api/qualityOperations", () => ({ getSeedPolicy: mocks.seed, putSeedPolicy: mocks.saveSeed, listBackmarkBatches: mocks.batches }));
vi.mock("../api/backmark", () => ({ getBackmarkBatch: mocks.summary, previewBackmarkBatch: mocks.preview, createBackmarkBatch: mocks.create, previewBackmarkBatchRegrade: mocks.regradePreview, createBackmarkBatchRegradeJob: mocks.regradeCreate }));
vi.mock("../api/scoreReleases", () => ({ listScoreReleases: mocks.releases }));
vi.mock("../api/workflowActors", () => ({ listReviewActors: mocks.users }));
vi.mock("../api/graderDrift", () => ({ listGradingQualityIncidents: mocks.incidents, resolveGradingQualityIncident: mocks.resolve, recomputeGraderDrift: mocks.recompute }));
vi.mock("../components/PageState", () => ({ LoadingState: ({ label }: { label: string }) => <p>{label}</p>, ErrorState: ({ message }: { message: string }) => <p>{message}</p> }));
vi.mock("../features/gold-papers/GoldPaperManagerDrawer", () => ({ GoldPaperManagerDrawer: ({ examId, questionId }: { examId: string; questionId: string }) => <div data-material="gold">{examId}:{questionId}</div> }));
vi.mock("../features/calibration/CalibrationDrawer", () => ({ CalibrationDrawer: ({ examId, questionId }: { examId: string; questionId: string }) => <div data-material="calibration">{examId}:{questionId}</div> }));
vi.mock("../features/answer-groups/AnswerGroupingDrawer", () => ({ AnswerGroupingDrawer: ({ examId, initialQuestionId }: { examId: string; initialQuestionId: string }) => <div data-material="groups">{examId}:{initialQuestionId}</div> }));

// 组件 mock 只转交字段和回调，用同一公共页面输入核对展示与请求；不模拟 Ant Design 校验规则。
vi.mock("antd", async () => {
  const React = await import("react");
  type Store = {
    values: Record<string, unknown>; version: number; listeners: Set<() => void>;
    setFieldsValue: (values: Record<string, unknown>) => void; getFieldsValue: () => Record<string, unknown>;
    getFieldValue: (name: string) => unknown; validateFields: () => Promise<Record<string, unknown>>;
    subscribe: (listener: () => void) => () => void;
  };
  const Context = React.createContext<{ store: Store; change?: (changed: Record<string, unknown>) => void } | null>(null);
  function makeStore(): Store {
    const store: Store = {
      values: {}, version: 0, listeners: new Set(),
      setFieldsValue(values) { Object.assign(store.values, values); store.version++; for (const listener of store.listeners) listener(); },
      getFieldsValue() { return { ...store.values }; }, getFieldValue(name) { return store.values[name]; },
      async validateFields() { return store.getFieldsValue(); },
      subscribe(listener) { store.listeners.add(listener); return () => { store.listeners.delete(listener); }; }
    };
    mocks.forms.push(store);
    return store;
  }
  function FormItem({ name, label, valuePropName = "value", children }: {
    name?: string; label?: string; valuePropName?: string;
    children: React.ReactElement<Record<string, unknown>> | ((form: Store) => ReactNode);
  }) {
    const context = React.useContext(Context)!;
    React.useSyncExternalStore(context.store.subscribe, () => context.store.version);
    if (typeof children === "function") return children(context.store);
    if (!name) return <label>{label}{children}</label>;
    return <label data-field={name}>{label}{React.cloneElement(children, {
      [valuePropName]: context.store.getFieldValue(name) ?? "",
      onChange: (value: unknown) => {
        const next = typeof value === "object" && value !== null && "target" in value ? (value.target as HTMLInputElement).value : value;
        context.store.setFieldsValue({ [name]: next }); context.change?.({ [name]: next });
      }
    })}</label>;
  }
  const Form = Object.assign(({ form, onValuesChange, children }: { form: Store; onValuesChange?: (changed: Record<string, unknown>) => void; children: ReactNode }) =>
    <Context.Provider value={{ store: form, change: onValuesChange }}>{children}</Context.Provider>, { useForm: () => [React.useState(makeStore)[0]], Item: FormItem });
  const Space = Object.assign(({ children }: { children: ReactNode }) => <div>{children}</div>, { Compact: ({ children }: { children: ReactNode }) => <div>{children}</div> });
  function Table({ dataSource, columns, rowKey, footer, expandable, locale }: {
    dataSource: Record<string, unknown>[];
    columns: Array<{ title: string; dataIndex?: string | string[]; render?: (value: unknown, record: Record<string, unknown>) => ReactNode }>;
    rowKey: string | ((row: Record<string, unknown>) => string); footer?: () => ReactNode;
    expandable?: { expandedRowRender: (row: Record<string, unknown>) => ReactNode }; locale?: { emptyText: string };
  }) {
    return <><table><tbody>{dataSource.map((row) => {
      const id = typeof rowKey === "function" ? rowKey(row) : String(row[rowKey]);
      return <React.Fragment key={id}><tr data-row={id}>{columns.map((column) => {
        const path = Array.isArray(column.dataIndex) ? column.dataIndex : [column.dataIndex];
        const value = path.reduce<unknown>((current, key) => key && current && typeof current === "object" ? (current as Record<string, unknown>)[key] : undefined, row);
        return <td key={column.title} data-column={column.title}>{column.render ? column.render(value, row) : String(value ?? "")}</td>;
      })}</tr>{expandable ? <tr><td>{expandable.expandedRowRender(row)}</td></tr> : null}</React.Fragment>;
    })}</tbody></table>{!dataSource.length ? locale?.emptyText : null}{footer?.()}</>;
  }
  return {
    App: { useApp: () => ({ message: mocks.message }) }, Form, Space, Table,
    Button: ({ children, onClick, loading, disabled }: { children: ReactNode; onClick?: () => void; loading?: boolean; disabled?: boolean }) => <button type="button" disabled={loading || disabled} onClick={onClick}>{children}</button>,
    Drawer: ({ title, open, extra, children, onClose }: { title: string; open: boolean; extra?: ReactNode; children: ReactNode; onClose: () => void }) => open ? <section data-drawer={title}><h2>{title}</h2><button onClick={onClose}>关闭工作区</button>{extra}{children}</section> : null,
    Alert: ({ type, message, description, action }: { type: string; message: string; description?: string; action?: ReactNode }) => <aside data-tone={type}><strong>{message}</strong><p>{description}</p>{action}</aside>,
    Tag: ({ color, children }: { color?: string; children: ReactNode }) => <span data-tone={color}>{children}</span>,
    Statistic: ({ title, value }: { title: string; value: number }) => <span>{title}{value}</span>,
    Empty: Object.assign(({ description }: { description: string }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Descriptions: ({ items }: { items: Array<{ key: string; label: string; children: ReactNode }> }) => <dl>{items.map((item) => <div key={item.key} data-description={item.label}><dt>{item.label}</dt><dd>{item.children}</dd></div>)}</dl>,
    Collapse: ({ items }: { items: Array<{ key: string; label: string; children: ReactNode }> }) => <div>{items.map((item) => <section key={item.key}><h3>{item.label}</h3>{item.children}</section>)}</div>,
    Select: ({ options, value, placeholder, onChange }: { options: Array<{ value: string; label: string }>; value?: string; placeholder?: string; onChange?: (value: string) => void }) => <select aria-label={placeholder} value={value ?? ""} onChange={(event) => onChange?.(event.target.value)}>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Input: ({ value, onChange }: { value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value ?? ""} onChange={onChange} />,
    InputNumber: ({ value, onChange }: { value?: number; onChange?: (value: number) => void }) => <input type="number" value={value ?? ""} onChange={(event) => onChange?.(Number(event.target.value))} />,
    Switch: ({ checked, onChange }: { checked: boolean; onChange: (checked: boolean) => void }) => <input type="checkbox" checked={Boolean(checked)} onChange={(event) => onChange(event.target.checked)} />,
    DatePicker: { RangePicker: () => <input aria-label="date range" readOnly /> }
  };
});

const now = "2026-10-01T01:02:03Z";
const ratio = { numerator: 0, denominator: 0, sample_size: 0 };
function dashboard(gate: QualityDashboard["gate"] = "ready"): QualityDashboard {
  return { exam_id: "exam", gate, blocking: [], warnings: [], generated_at: now, questions: [{
    question: { id: "q1", question_no: "Q1", archetype_code: "", risk_tier: "", max_score: 10 }, gate,
    gold: { ready: true, active_approved: 2, gaps: [], score_bands: [] },
    calibration: { configured: false, completed: 0, passed: 0, pass_rate: ratio, graders: [] },
    seed: { policy_status: "paused", sample_size: 0, exact_agreement: ratio, within_one_agreement: ratio, criterion_agreement: ratio, graders: [] },
    human_human_agreement: { sample_size: 0, within_rule_agreement: ratio, open_cases: 0 },
    answer_groups: { group_count: 0, member_count: 0, open_sample_groups: 0 }, drift: { open_warnings: 0, open_critical: 0, incidents: [] },
    backmark: { open_batches: 1, pending_items: 0, completed_items: 1, correction_rate: ratio }, findings: []
  }] };
}
const batch: BackmarkBatch = { id: "batch", exam_id: "exam", question_id: "q1", source_incident_id: "incident", selector: {}, policy: { disposition: "regrade" }, affected_count: 10, status: "ready_for_confirmation", created_by: "admin", created_at: now, updated_at: now };
function summary(overrides: Partial<BackmarkSummary> = {}): BackmarkSummary { return { batch, items: [], diff_histogram: [], regrade_required_count: 8, ...overrides }; }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }

describe("质量看板展示职责回归", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks(); mocks.forms.length = 0;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.dashboard.mockResolvedValue({ dashboard: dashboard() }); mocks.batches.mockResolvedValue({ backmark_batches: [batch], has_more: true, next_cursor: "batch-next" });
    mocks.users.mockResolvedValue([{ id: "grader", display_name: "阅卷员", username: "grader" }]);
    mocks.summary.mockResolvedValue({ backmark: summary(), has_more: true, next_cursor: "item-next" });
    mocks.releases.mockResolvedValue({ score_releases: [{ id: "draft", status: "draft" }, { id: "release", status: "published", version: 1, reason: "" }, { id: "release-2", status: "published", version: 2, reason: "补发" }] });
    mocks.regradePreview.mockResolvedValue({ preview: { affected_count: 8, source_release_version: 1 } });
    mocks.regradeCreate.mockResolvedValue({ regrade: { job: { affected_count: 8 } } });
    mocks.preview.mockResolvedValue({ preview: { affected_count: 1, selector_hash: "hash", time_range: {}, score_bands: [] } });
    mocks.recompute.mockResolvedValue({});
    mocks.incidents.mockResolvedValue({ incidents: [] });
    mocks.seed.mockResolvedValue({ policy: { exam_id: "exam", question_id: "q1", rate: .1, min_interval: 10, max_interval: 30, status: "active", revision: 3 } });
    mocks.saveSeed.mockResolvedValue({ policy: { exam_id: "exam", question_id: "q1", rate: .1, min_interval: 10, max_interval: 30, status: "paused", revision: 4 } });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });
  async function render(canManage = true) { await act(async () => root.render(<QualityDashboardPage examId="exam" canManage={canManage} />)); }
  function button(label: string, scope: ParentNode = host) { const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label); expect(found).toBeDefined(); return found!; }
  async function click(label: string, scope: ParentNode = host) { await act(async () => button(label, scope).click()); }
  async function openSummary() { await render(); await click("创建回标"); await click("查看差异"); }
  const drawer = () => host.querySelector<HTMLElement>('[data-drawer="回标差异与复评交接"]')!;

  it("差异首次读取失败结束加载，刷新按已选择批次重新读取", async () => {
    mocks.summary.mockRejectedValueOnce(new Error("差异暂不可读"));
    await openSummary();
    expect(drawer().textContent).toContain("回标差异读取失败");
    expect(drawer().textContent).toContain("差异暂不可读");
    expect(drawer().textContent).not.toContain("正在加载回标差异");
    await click("刷新", drawer());
    expect(mocks.summary).toHaveBeenCalledTimes(2);
    expect(mocks.summary).toHaveBeenLastCalledWith("batch", { status: "regrade_required" });
    expect(drawer().querySelector('[data-description="回标题目"] dd')?.textContent).toBe("Q1");
  });

  it.each(["releases", "users"] as const)("%s 交接读取失败不遮住已成功的差异，可同页重读恢复", async (part) => {
    await render(); await click("创建回标");
    mocks[part].mockRejectedValueOnce(new Error("交接目录暂不可读"));
    await click("查看差异");
    expect(drawer().querySelector('[data-description="受影响答卷"] dd')?.textContent).toBe("10 份");
    expect(drawer().textContent).toContain("回标差异已读取，复评交接资料尚未齐全");
    expect(drawer().textContent).not.toContain("正在加载回标差异");
    await click("重新读取交接资料", drawer());
    expect(button("预览正式复评范围", drawer()).disabled).toBe(false);
  });

  it("切批次后迟到差异不能覆盖新题，关闭后结果不能重开工作区", async () => {
    const first = deferred<{ backmark: BackmarkSummary; has_more: boolean }>();
    const value = dashboard(); value.questions.push({ ...value.questions[0], question: { ...value.questions[0].question, id: "q2", question_no: "Q2" } });
    mocks.dashboard.mockResolvedValue({ dashboard: value });
    const secondBatch = { ...batch, id: "batch-2", question_id: "q2", affected_count: 20 };
    mocks.batches.mockResolvedValue({ backmark_batches: [batch, secondBatch], has_more: false });
    mocks.summary.mockReturnValueOnce(first.promise).mockResolvedValueOnce({ backmark: summary({ batch: secondBatch }), has_more: false });
    await render(); await click("创建回标");
    await click("查看差异", host.querySelector('[data-row="batch"]')!);
    await click("查看差异", host.querySelector('[data-row="batch-2"]')!);
    await act(async () => first.resolve({ backmark: summary(), has_more: false }));
    expect(drawer().querySelector('[data-description="回标题目"] dd')?.textContent).toBe("Q2");
    expect(drawer().querySelector('[data-description="受影响答卷"] dd')?.textContent).toBe("20 份");
    const late = deferred<{ backmark: BackmarkSummary; has_more: boolean }>();
    mocks.summary.mockReturnValueOnce(late.promise);
    await click("刷新", drawer()); await click("关闭工作区", drawer());
    await act(async () => late.resolve({ backmark: summary(), has_more: false }));
    expect(drawer()).toBeNull();
  });

  it("同名教师的回标与复评选项均含账号，确认和成功回执显示实际接手人", async () => {
    mocks.users.mockResolvedValue([{ id: "grader", display_name: "王老师", username: "wang.math" }, { id: "grader-2", display_name: "王老师", username: "wang.physics" }]);
    await openSummary();
    for (const field of ["grader_id", "reassigned_to"]) expect(host.querySelector(`[data-field="${field}"] select`)?.textContent).toContain("王老师（账号：wang.physics）");
    const assignee = drawer().querySelector<HTMLSelectElement>('select[aria-label="选择复评阅卷员"]')!;
    await act(async () => { assignee.value = "grader-2"; assignee.dispatchEvent(new Event("change", { bubbles: true })); });
    await click("预览正式复评范围", drawer());
    expect(drawer().textContent).toContain("Q1；接手教师：王老师（账号：wang.physics）");
    await click("创建复评任务", drawer());
    expect(mocks.regradeCreate).toHaveBeenCalledWith("batch", { source_release_id: "release", assignee_id: "grader-2" });
    expect(mocks.message.success).toHaveBeenCalledWith(expect.stringContaining("接手教师：王老师（账号：wang.physics）"));
  });

  it("回标历史和差异主信息显示题号、份数、时间及中文去向", async () => {
    await openSummary();
    const row = host.querySelector('[data-row="batch"]')!;
    expect(row.querySelector('[data-column="题目"]')?.textContent).toBe("Q1");
    expect(row.querySelector('[data-column="影响答卷"]')?.textContent).toBe("10 份");
    expect(row.querySelector('[data-column="状态"]')?.textContent).toBe("待确认");
    expect(row.querySelector('[data-column="创建时间"]')?.textContent).toContain("2026");
    expect(row.querySelector('[data-column="差异去向"]')?.textContent).toBe("进入题目重评流程");
    expect(drawer().querySelector('[data-description="差异去向"] dd')?.textContent).toBe("进入题目重评流程");
  });

  it.each([
    ["blocked", "不可发布", "error", "存在必须先处置的质量问题，不能发布成绩。"],
    ["ready", "可继续", "success", "当前没有阻断性质量事件。"],
    ["warning", "需要关注", "warning", "当前没有阻断项，但仍有待补齐的质量证据或处理项。"],
    ["insufficient_data", "需要关注", "warning", "当前没有阻断项，但仍有待补齐的质量证据或处理项。"]
  ])("发布状态 %s 与题目证据保留原展示", async (gate, label, tone, description) => {
    mocks.dashboard.mockResolvedValue({ dashboard: dashboard(gate as QualityDashboard["gate"]) }); await render(false);
    expect(host.querySelector("aside")?.getAttribute("data-tone")).toBe(tone); expect(host.querySelector("aside strong")?.textContent).toBe(`发布可行性：${label}`); expect(host.querySelector("aside p")?.textContent).toBe(description);
    expect(host.querySelector('[data-column="质量状态"] [data-tone]')?.textContent).toBe(label);
    expect(host.textContent).toContain("样本不足 · 0 份"); expect(host.textContent).toContain("当前没有待处理事项");
    expect([...host.querySelectorAll("button")].map((item) => item.textContent)).toEqual(["刷新"]);
  });

  it.each([
    ["completed", "已完成"], ["cancelled", "已取消"], ["ready_for_confirmation", "待确认"],
    // 刻意模拟服务端返回 SDK 尚未识别的状态，核对界面兜底；不放宽生产状态枚举。
    ["other" as unknown as BackmarkBatch["status"], "处理中"]
  ] as const)("批次 %s 保留状态兜底", async (status, label) => {
    mocks.summary.mockResolvedValue({ backmark: summary({ batch: { ...batch, status } }), has_more: false }); await openSummary();
    expect(drawer().querySelector('[data-description="批次状态"] dd')?.textContent).toBe(label);
  });

  it.each([
    { count: 0, status: "ready_for_confirmation", releases: true, expected: "没有需要题目复评的严重差异" },
    { count: 8, status: "completed", releases: true, expected: "回标批次尚未完成" },
    { count: 8, status: "ready_for_confirmation", releases: false, expected: "当前考试没有已发布的成绩版本" }
  ] as const)("复评条件 count=$count/status=$status 保留优先级", async ({ count, status, releases, expected }) => {
    mocks.summary.mockResolvedValue({ backmark: summary({ batch: { ...batch, status }, regrade_required_count: count }), has_more: false }); if (!releases) mocks.releases.mockResolvedValue({ score_releases: [] }); await openSummary();
    expect(drawer().textContent).toContain(expected); expect([...drawer().querySelectorAll("button")].some((item) => item.textContent === "预览正式复评范围")).toBe(false);
  });

  it("分差区分缺失、负数、零、正数，只列严重项", async () => {
    const item = { batch_id: "batch", review_task_id: "task", original_grade_id: "grade", original_reviewer_id: "original", reassigned_to: "grader", original_score: 0, max_score: 10, status: "regrade_required", revision: 1, created_at: now, updated_at: now };
    mocks.summary.mockResolvedValue({ backmark: summary({ items: [undefined, -1, 0, 1].map((diff, index) => ({ ...item, id: `item-${index}`, diff })), diff_histogram: [{ delta: -1, count: 1 }, { delta: 0, count: 1 }, { delta: 1, count: 1 }] }), has_more: false }); await openSummary();
    expect([...drawer().querySelectorAll('[data-column="分差"]')].map((cell) => cell.textContent)).toEqual(["-", "-1", "0", "+1"]);
    expect([...drawer().querySelectorAll('[data-column="新旧分差"]')].map((cell) => cell.textContent)).toEqual(["-1 分", "0 分", "+1 分"]);
  });

  it("批次和严重项分页分别追加，空当前页仍按总数显示复评入口", async () => {
    await render(); await click("创建回标"); mocks.batches.mockResolvedValueOnce({ backmark_batches: [{ ...batch, id: "batch-2" }], has_more: false }); await click("加载更多批次");
    expect(mocks.batches).toHaveBeenLastCalledWith("exam", "batch-next"); expect(host.querySelector('[data-row="batch-2"]')).not.toBeNull();
    await click("查看差异"); expect(drawer().textContent).toContain("转入题目复评"); expect(drawer().textContent).toContain("当前页暂无需重评项，请加载更多结果");
    mocks.summary.mockResolvedValueOnce({ backmark: summary(), has_more: false }); await click("加载更多回标结果", drawer());
    expect(mocks.summary).toHaveBeenLastCalledWith("batch", { limit: 50, cursor: "item-next", status: "regrade_required" });
  });

  it("复评选择变化清预览，创建保持批次和来源版本请求", async () => {
    await openSummary(); await click("预览正式复评范围", drawer()); expect(mocks.regradePreview).toHaveBeenCalledWith("batch", { source_release_id: "release", assignee_id: "grader" }); expect(drawer().textContent).toContain("将冻结 8 份已发布答卷");
    const select = drawer().querySelector<HTMLSelectElement>('select[aria-label="选择已发布成绩版本"]')!;
    await act(async () => { select.value = "release-2"; select.dispatchEvent(new Event("change", { bubbles: true })); }); expect(drawer().textContent).not.toContain("将冻结");
    await click("预览正式复评范围", drawer()); await click("创建复评任务", drawer());
    expect(mocks.regradeCreate).toHaveBeenCalledWith("batch", { source_release_id: "release-2", assignee_id: "grader" }); expect(drawer()).toBeNull(); expect(mocks.dashboard).toHaveBeenCalledTimes(2);
  });

  it("普通刷新保持字段节点和值，题目重算转交对应题目", async () => {
    await render(); await click("创建回标"); const field = host.querySelector<HTMLSelectElement>('[data-field="grader_id"] select')!;
    await act(async () => mocks.forms[0].setFieldsValue({ grader_id: "grader" })); field.focus(); await render(); expect(document.activeElement).toBe(field);
    await click("刷新", host.querySelector(".page-heading")!); expect(host.querySelector('[data-field="grader_id"] select')).toBe(field); expect(field.value).toBe("grader"); expect(mocks.forms).toHaveLength(2);
    await click("重新计算本题质量"); expect(mocks.recompute).toHaveBeenCalledWith("exam", "q1");
  });

  it("范围变化淘汰迟到预览，不能重新开放创建", async () => {
    const pending = deferred<{ preview: { affected_count: number; selector_hash: string; time_range: object; score_bands: unknown[] } }>(); mocks.preview.mockReturnValue(pending.promise);
    await render(); await click("创建回标"); await click("预览影响范围");
    const field = host.querySelector<HTMLInputElement>('[data-field="score_min"] input')!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "1"); field.dispatchEvent(new Event("input", { bubbles: true })); });
    await act(async () => pending.resolve({ preview: { affected_count: 1, selector_hash: "hash", time_range: {}, score_bands: [] } }));
    expect(button("创建并分配回标").disabled).toBe(true); expect(host.querySelector('[data-description="受影响任务"]')).toBeNull();
  });

  it("盲测保存继续提交修订号和暂停值", async () => {
    await render(); await click("盲测策略"); await act(async () => mocks.forms[1].setFieldsValue({ enabled: false })); await click("保存");
    expect(mocks.saveSeed).toHaveBeenCalledWith("exam", "q1", { rate: .1, min_interval: 10, max_interval: 30, status: "paused", expected_revision: 3 }); expect(mocks.message.success).toHaveBeenCalledWith("盲测策略已保存");
  });

  it("选人走考试专属目录，质量事件只可选择当前题并在切题后清除", async () => {
    const value = dashboard(); value.questions.push({ ...value.questions[0], question: { ...value.questions[0].question, id: "q2", question_no: "Q2" } });
    mocks.dashboard.mockResolvedValue({ dashboard: value }); mocks.incidents.mockResolvedValue({ incidents: [{ id: "event-1", question_id: "q1", severity: "warning", type: "drift", metric_snapshot: { sample_count: 12 } }, { id: "event-2", question_id: "q2", severity: "critical", type: "severe", metric_snapshot: { sample_count: 3 } }] });
    await render(); await click("创建回标"); expect(mocks.users).toHaveBeenCalledWith("exam");
    expect(host.querySelector('[data-field="source_incident_id"]')?.textContent).toContain("12份样本"); expect(host.querySelector('[data-field="source_incident_id"]')?.textContent).not.toContain("3份样本");
    await act(async () => { mocks.forms[0].setFieldsValue({ source_incident_id: "event-1" }); const select = host.querySelector<HTMLSelectElement>('[data-field="question_id"] select')!; select.value = "q2"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.forms[0].getFieldsValue().source_incident_id).toBeUndefined(); expect(host.querySelector('[data-field="source_incident_id"]')?.textContent).toContain("3份样本");
  });

  it("质量事件和来源选项展示真实题号、中文原因、教师账号及时间", async () => {
    mocks.incidents.mockResolvedValue({ incidents: [{ id: "event-readable", question_id: "q1", grader_id: "grader", severity: "warning", type: "grader_bias_high", metric_snapshot: { sample_count: 12 }, created_at: now }] });
    await render(); await click("质量事件");
    const event = host.querySelector('[data-row="event-readable"]')!;
    expect(event.querySelector('[data-column="题目"]')?.textContent).toBe("Q1");
    expect(event.querySelector('[data-column="类型"]')?.textContent).toBe("评分持续偏高");
    expect(event.querySelector('[data-column="阅卷教师"]')?.textContent).toBe("阅卷员（账号：grader）");
    expect(event.querySelector('[data-column="发生时间"]')?.textContent).toContain("2026");
    await click("创建回标", event);
    const option = host.querySelector('[data-field="source_incident_id"]')!;
    expect(option.textContent).toContain("评分持续偏高");
    expect(option.textContent).not.toContain("grader_bias_high");
  });

  it("人员目录读取失败时保留真实事件，题号缺失和新类型使用可读兜底", async () => {
    mocks.incidents.mockResolvedValue({ incidents: [{ id: "event-partial", question_id: "unreadable-question-uuid", grader_id: "unreadable-teacher-uuid", severity: "warning", type: "future_internal_code", metric_snapshot: {}, created_at: now }] });
    mocks.users.mockRejectedValue(new Error("directory unavailable"));
    await render(); await click("质量事件");
    const event = host.querySelector('[data-row="event-partial"]')!;
    expect(event.textContent).toContain("题号暂不可读");
    expect(event.textContent).toContain("其他评分质量异常");
    expect(event.textContent).toContain("教师姓名暂不可读");
    expect(event.textContent).not.toMatch(/uuid|future_internal_code/);
    expect(mocks.message.warning).toHaveBeenCalledWith("质量事件已读取，教师姓名暂不可读；可刷新重试。");
  });

  it.each([["核对本题标准卷", "gold"], ["处理标定", "calibration"], ["复核分组样本", "groups"]])("%s 直接打开本题材料工作区", async (label, material) => {
    await render(); await click(label); expect(host.querySelector(`[data-material="${material}"]`)?.textContent).toBe("exam:q1");
  });

  it.each([true, false])("盲测切题读取失败=%s时只能保存当前题已成功读取的策略", async failed => {
    const value = dashboard(); value.questions.push({ ...value.questions[0], question: { ...value.questions[0].question, id: "q2", question_no: "Q2" } });
    mocks.dashboard.mockResolvedValue({ dashboard: value });
    mocks.seed.mockImplementation(async (_exam: string, question: string) => {
      if (question === "q2" && failed) throw new Error("second question policy unavailable");
      return { policy: { exam_id: "exam", question_id: question, rate: question === "q1" ? .1 : .7, min_interval: question === "q1" ? 10 : 20, max_interval: question === "q1" ? 30 : 50, status: "active", revision: 3 } };
    });
    mocks.saveSeed.mockImplementation(async (_exam: string, question: string, body: Record<string, unknown>) => ({ policy: { ...body, exam_id: "exam", question_id: question, revision: 4 } }));
    await render(); await click("盲测策略");
    const pane = host.querySelector('[data-drawer="盲测策略"]')!;
    const select = pane.querySelector<HTMLSelectElement>("select")!;
    await act(async () => { select.value = "q2"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(select.value).toBe("q2"); expect(button("保存", pane).disabled).toBe(failed);
    await click("保存", pane);
    if (failed) {
      expect(mocks.saveSeed).not.toHaveBeenCalled();
      expect(pane.textContent).toContain("当前题目的盲测策略读取失败，暂不能保存");
      expect(mocks.forms[1].getFieldsValue().rate).toBeUndefined();
    } else {
      expect(mocks.saveSeed).toHaveBeenCalledWith("exam", "q2", { rate: .7, min_interval: 20, max_interval: 50, status: "active", expected_revision: 3 });
      expect(mocks.message.success).toHaveBeenCalledWith("盲测策略已保存");
    }
  });
});
