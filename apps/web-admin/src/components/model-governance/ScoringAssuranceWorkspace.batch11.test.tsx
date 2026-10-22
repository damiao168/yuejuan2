// @vitest-environment jsdom
import { act, type ChangeEvent, type SubmitEvent, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../api/client";
import type {
  AIEligibilityPolicy, AIHumanDisagreement, GradingEvaluationQualitySummary, GradingEvaluationRun, ModelCalibration, ModelCalibrationEvidence
} from "../../api/scoringAssurance";
import { ScoringAssuranceWorkspace } from "./ScoringAssuranceWorkspace";
import { ManagedGovernanceWorkspace } from "./ManagedGovernanceWorkspace";

interface MockForm {
  id: number;
  values: Record<string, unknown>;
  validateFields: ReturnType<typeof vi.fn>;
  setFieldsValue: ReturnType<typeof vi.fn>;
  resetFields: ReturnType<typeof vi.fn>;
}
const mocks = vi.hoisted(() => ({
  api: {
    setScoringAssuranceSchool: vi.fn(), getAIEligibilityPolicy: vi.fn(), putAIEligibilityPolicy: vi.fn(), getAIEligibilityDecision: vi.fn(),
    listGradingEvaluations: vi.fn(), listModelCalibrations: vi.fn(), listAIHumanDisagreements: vi.fn(),
    listGradingEvaluationSliceMetrics: vi.fn(), listGradingEvaluationResponseDifficulty: vi.fn(), getGradingEvaluationQualitySummary: vi.fn(),
    listModelCalibrationEvidence: vi.fn(), createGradingEvaluation: vi.fn(), addGradingEvaluationObservation: vi.fn(),
    completeGradingEvaluation: vi.fn(), invalidateGradingEvaluation: vi.fn(), createModelCalibration: vi.fn(), addModelCalibrationEvidence: vi.fn(),
    completeModelCalibration: vi.fn(), approveModelCalibration: vi.fn(), invalidateModelCalibration: vi.fn(),
    classifyAIHumanDisagreement: vi.fn(), routeAIHumanDisagreement: vi.fn()
  },
  forms: new Map<number, MockForm>(), drawers: new Map<string, Record<string, unknown>>(), success: vi.fn(), error: vi.fn()
}));
const appContext = vi.hoisted(() => ({ message: { success: mocks.success, error: mocks.error } }));
vi.mock("../../api/scoringAssurance", () => mocks.api);
vi.mock("../../api/modelGovernance", () => ({
  listModelEvaluationRuns: async () => ({ evaluation_runs: [] }), listModelApprovals: async () => ({ model_approvals: [] }),
  getModelPolicy: async () => ({ policy: { display_name: "学校策略", mode: "local_only", allowed_model_config_ids: [], max_cost_micros_per_question: 0, max_cost_micros_per_exam: 0, version: 1 } }), updateModelPolicy: vi.fn()
}));
vi.mock("./ModelEvaluationWorkspace", () => ({ ModelEvaluationWorkspace: () => null }));
vi.mock("./ModelApprovalWorkspace", () => ({ ModelApprovalWorkspace: () => null }));
vi.mock("./PromptVersionWorkspace", () => ({ PromptVersionWorkspace: () => null }));
vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const Box = ({ children }: { children?: ReactNode }) => React.createElement("div", null, children);
  const Space = Object.assign(Box, { Compact: Box });
  const Input = Object.assign((props: Record<string, unknown>) => React.createElement("input", {
    placeholder: props.placeholder as string, value: props.value as string | undefined,
    onChange: props.onChange as (event: ChangeEvent<HTMLInputElement>) => void
  }), { TextArea: (props: Record<string, unknown>) => React.createElement("textarea", { rows: props.rows as number, value: props.value as string | undefined, onChange: props.onChange as (event: ChangeEvent<HTMLTextAreaElement>) => void }) });
  const Form = Object.assign((props: Record<string, unknown>) => React.createElement("form", {
    "data-form": (props.form as MockForm).id, className: props.className as string,
    onSubmit: (event: SubmitEvent<HTMLFormElement>) => { event.preventDefault(); (props.onFinish as (() => void) | undefined)?.(); }
  }, props.children as ReactNode), {
    Item: (props: Record<string, unknown>) => React.createElement("div", { "data-field": typeof props.name === "string" ? props.name : JSON.stringify(props.name) }, props.label as ReactNode, props.children as ReactNode),
    useForm: () => {
      const reference = React.useRef<MockForm | null>(null);
      if (!reference.current) {
        const form: MockForm = { id: mocks.forms.size + 1, values: {}, validateFields: vi.fn(), setFieldsValue: vi.fn(), resetFields: vi.fn() };
        form.validateFields.mockImplementation(async () => form.values);
        form.setFieldsValue.mockImplementation((values: Record<string, unknown>) => { Object.assign(form.values, values); });
        form.resetFields.mockImplementation(() => { form.values = {}; });
        reference.current = form; mocks.forms.set(form.id, form);
      }
      return [reference.current];
    }
  });
  const List = Object.assign((props: Record<string, unknown>) => React.createElement("div", null,
    (props.dataSource as Array<Record<string, unknown>>).map((item, index) => React.createElement("div", { key: index }, (props.renderItem as (item: Record<string, unknown>) => ReactNode)(item)))), {
    Item: Object.assign((props: Record<string, unknown>) => React.createElement("article", null, props.children as ReactNode, props.actions as ReactNode), {
      Meta: (props: Record<string, unknown>) => React.createElement("div", null, props.title as ReactNode, props.description as ReactNode)
    })
  });
  const Empty = Object.assign((props: Record<string, unknown>) => React.createElement("div", null, props.description as ReactNode), { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    App: { useApp: () => appContext }, Space, Input, Form, List, Empty,
    InputNumber: (props: Record<string, unknown>) => React.createElement("input", { type: "number", placeholder: props.placeholder as string }),
    Typography: { Text: Box }, Divider: Box, Spin: Box, Switch: () => null, Segmented: () => null,
    Alert: (props: Record<string, unknown>) => React.createElement("div", { "data-alert": props.message as string }, props.message as ReactNode, props.description as ReactNode, props.action as ReactNode),
    Card: (props: Record<string, unknown>) => React.createElement("section", { "data-card": true }, props.title as ReactNode, props.extra as ReactNode, props.children as ReactNode),
    Button: (props: Record<string, unknown>) => React.createElement("button", { disabled: Boolean(props.disabled), "data-loading": String(Boolean(props.loading)), type: props.htmlType === "submit" ? "submit" : "button", onClick: props.onClick as () => void }, props.children as ReactNode),
    Drawer: (props: Record<string, unknown>) => { mocks.drawers.set(String(props.title), props); return React.createElement("div", { "data-drawer": props.title as string }, props.children as ReactNode, props.extra as ReactNode); },
    Select: (props: Record<string, unknown>) => React.createElement("select", { "aria-label": props["aria-label"] as string, disabled: Boolean(props.disabled), value: props.value ?? "", onChange: (event: ChangeEvent<HTMLSelectElement>) => (props.onChange as ((value: string) => void) | undefined)?.(event.target.value) },
      React.createElement("option", { value: "" }, props.placeholder as string),
      (props.options as Array<{ value: string | boolean; label: string }>).map((option) => React.createElement("option", { key: String(option.value), value: String(option.value) }, option.label))),
    Tag: (props: Record<string, unknown>) => React.createElement("span", { "data-color": props.color as string }, props.children as ReactNode),
    Descriptions: (props: Record<string, unknown>) => React.createElement("div", null, (props.items as Array<{ key: string; label: string; children: ReactNode }>).map((item) => React.createElement("div", { key: item.key }, item.label, item.children))),
    Table: (props: Record<string, unknown>) => {
      const columns = props.columns as Array<{ title: string; dataIndex?: string; render?: (value: unknown, item: Record<string, unknown>) => ReactNode }>;
      return React.createElement("div", { "data-table": columns[0].title }, (props.dataSource as Array<Record<string, unknown>>).map((item) => React.createElement("div", { key: String(item.id), "data-row": String(item.id) },
        columns.map((column) => React.createElement("span", { key: column.title }, column.render ? column.render(column.dataIndex ? item[column.dataIndex] : undefined, item) : item[column.dataIndex ?? ""] as ReactNode)))));
    }
  };
});

function evaluation(id: string, overrides: Partial<GradingEvaluationRun> = {}): GradingEvaluationRun {
  return { id, key: id, display_name: `评测-${id}`, model_reference: "model-v1", prompt_version: "prompt-v1", rubric_version: "rubric-v1", dataset_reference: "dataset", dataset_sha256: "a".repeat(64), status: "draft", observation_count: 1, created_at: "", ...overrides };
}
function calibration(id: string, overrides: Partial<ModelCalibration> = {}): ModelCalibration {
  return { id, key: id, evaluation_run_id: "A", axis: { model_reference: "model-v1", prompt_version: "prompt-v1", rubric_version: "rubric-v1", subject: "mathematics", archetype: "short_constructed", slice_key: "all" }, method: "auto", status: "draft", calibration_n: 1, created_at: "", ...overrides };
}
function disagreement(overrides: Partial<AIHumanDisagreement> = {}): AIHumanDisagreement {
  return { id: "D", exam_id: "exam", question_id: "question", submission_id: "submission", answer_segment_id: "answer", ai_candidate_id: "candidate", human_grade_id: "grade", ai_candidate_score: 0, human_score: 1, max_score: 10, delta: 1, absolute_delta: 1, difference_type: "score", severity: "severe", risk_tier: "R2", trigger_rules: [], evidence_summary: { ai_evidence_count: 0, ai_matched_criterion_count: 0, human_criterion_count: 1 }, status: "needs_review", revision: 7, created_at: "", updated_at: "", ...overrides };
}
function quality(): GradingEvaluationQualitySummary {
  return { sample_count: 1, page_match_accuracy: { observed_count: 0 }, mean_crop_iou: { observed_count: 0 }, mean_transcription_cer: { observed_count: 0 }, formula_exact_rate: { observed_count: 0 }, mean_rubric_criterion_agreement: { observed_count: 0 }, human_review_rate: 0, risky_error_routing_recall: 0, error_attribution: [] };
}
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: Error) => void; const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; }); return { promise, resolve, reject }; }

describe("评分保障共同旧新回归：模拟 API 和表单", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks(); mocks.forms.clear(); mocks.drawers.clear(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.history.replaceState(null, "", "/admin/platform/model-config");
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.api.getAIEligibilityPolicy.mockRejectedValue(new ApiClientError(404, "not_found", "not found"));
    mocks.api.listGradingEvaluations.mockResolvedValue({ evaluation_runs: [evaluation("A"), evaluation("B")] });
    mocks.api.listModelCalibrations.mockResolvedValue({ calibrations: [calibration("C"), calibration("E")] });
    mocks.api.listAIHumanDisagreements.mockResolvedValue({ disagreements: [disagreement()] });
    mocks.api.listGradingEvaluationSliceMetrics.mockResolvedValue({ slice_metrics: [] });
    mocks.api.listGradingEvaluationResponseDifficulty.mockResolvedValue({ response_difficulty: [] });
    mocks.api.getGradingEvaluationQualitySummary.mockResolvedValue({ quality_summary: quality() });
    mocks.api.listModelCalibrationEvidence.mockResolvedValue({ evidence: [] });
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });
  async function render(props: Partial<Parameters<typeof ScoringAssuranceWorkspace>[0]> = {}) { await act(async () => root.render(<ScoringAssuranceWorkspace tenantID="school" canReadEligibility canManageEligibility canManageEvaluations canReadDisagreements canManageDisagreements {...props} />)); }
  function button(label: string, scope: ParentNode = host) { const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label); expect(found, label).toBeDefined(); return found!; }
  function form(id: number) { return mocks.forms.get(id)!; }
  function detail(index: number) { return host.querySelectorAll(".model-assurance-detail")[index]; }
  function selectRow(table: string, id: string) { button("查看", host.querySelector(`[data-table="${table}"] [data-row="${id}"]`)!).click(); }
  async function changeScope(label: string, value: string) {
    const select = host.querySelector<HTMLSelectElement>(`[aria-label="${label}"]`)!;
    await act(async () => { select.value = value; select.dispatchEvent(new Event("change", { bubbles: true })); });
  }

  it("导入失败保留确切成功行，继续只发送剩余行且保留原学校",async()=>{
    await render({canReadEligibility:false,canReadDisagreements:false});
    await act(async()=>button("导入对齐评分记录").click());
    const rows=["first","second"].map(response_key=>({response_key,response_fingerprint:"a".repeat(64),reference_kind:"gold",subject:"mathematics",archetype:"short_answer",reference_score:1,model_score:1,max_score:2,ocr_quality:"high",answer_length:"short",rubric_complexity:"low",error_source:"none",needs_human_review:false,reference_reviewer_count:1,reference_adjudicated:false}));
    const text=host.querySelector<HTMLTextAreaElement>('[data-drawer="导入脱敏对齐评分记录"] textarea')!;
    await act(async()=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,"value")!.set!.call(text,JSON.stringify(rows));text.dispatchEvent(new Event("input",{bubbles:true}));});
    await act(async()=>button("校验并预览").click());
    mocks.api.addGradingEvaluationObservation.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("failed second"));
    await act(async()=>button("确认导入").click());
    expect(host.textContent).toContain("已保存 1/2 条");
    // 关闭不丢弃服务器已确认的游标；重新打开继续原评测，避免重发第一行。
    await act(async()=>(mocks.drawers.get("导入脱敏对齐评分记录")?.onClose as (()=>void))());
    await act(async()=>button("导入对齐评分记录").click());
    expect(host.textContent).toContain("已保存 1/2 条");
    mocks.api.addGradingEvaluationObservation.mockResolvedValue({});
    await act(async()=>button("继续保存剩余记录").click());
    expect(mocks.api.addGradingEvaluationObservation.mock.calls.map(call=>call[1].response_key)).toEqual(["first","second","second"]);
    expect(mocks.api.addGradingEvaluationObservation.mock.calls.every(call=>call[0]==="A")).toBe(true);
  });

  it("管理位本身不授予面板读权限，也不取列表", async () => {
    await render({ canReadEligibility: false, canManageEvaluations: false, canReadDisagreements: false });
    expect(host.textContent).toContain("当前账号没有评分保障查看权限"); expect(mocks.api.getAIEligibilityPolicy).not.toHaveBeenCalled(); expect(mocks.api.listGradingEvaluations).not.toHaveBeenCalled(); expect(mocks.api.listAIHumanDisagreements).not.toHaveBeenCalled();
  });

  it("只读策略保持轴、404反馈和控件身份，不显示维护入口", async () => {
    await render({ initialAxis: { subject_code: "mathematics", education_stage: "junior", archetype_code: "numeric_expression", risk_tier: "R2" }, canManageEligibility: false, canManageEvaluations: false, canReadDisagreements: false });
    expect(host.textContent).toContain("尚未配置准入策略"); expect(host.textContent).not.toContain("维护策略");
    const select = host.querySelector<HTMLSelectElement>('[aria-label="学科"]')!; select.focus();
    await act(async () => { select.value = "english"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.querySelector('[aria-label="学科"]')).toBe(select); expect(document.activeElement).toBe(select);
    expect(mocks.api.getAIEligibilityPolicy).toHaveBeenLastCalledWith({ subject_code: "english", education_stage: "junior", archetype_code: "numeric_expression", risk_tier: "R2" });
    expect(mocks.api.listModelCalibrations).not.toHaveBeenCalled();
  });

  it.each(["essay", "*", "unknown"])("高中英语组合入口的%s不能静默查询初中数学；补齐具体题型和风险后发送真实范围", async archetype => {
    window.history.replaceState(null, "", `/admin/platform/model-config?tenant_id=school&tab=governance&view=assurance&stage=senior&subject=english&archetype=${encodeURIComponent(archetype)}`);
    await act(async () => root.render(<ManagedGovernanceWorkspace tenantID="school" schoolName="测试高中" configs={[]} />));
    expect(mocks.api.getAIEligibilityPolicy).not.toHaveBeenCalled();
    expect(host.querySelector<HTMLSelectElement>('[aria-label="学段"]')!.value).toBe("senior");
    expect(host.querySelector<HTMLSelectElement>('[aria-label="学科"]')!.value).toBe("english");
    expect(host.querySelector<HTMLSelectElement>('[aria-label="题型"]')!.value).toBe("");
    expect(button("查询当前范围").disabled).toBe(true);
    expect(button("维护当前范围策略").disabled).toBe(true);
    await changeScope("题型", "extended_response");
    expect(mocks.api.getAIEligibilityPolicy).not.toHaveBeenCalled();
    await changeScope("风险级别", "R3");
    expect(mocks.api.getAIEligibilityPolicy).toHaveBeenCalledExactlyOnceWith({ subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3" });
    expect(host.textContent).toContain("当前检查范围：高中 · 英语 · 长作答 · 风险等级 3（R3）");
    expect(host.textContent).toContain("尚未配置准入策略");
  });

  it("没有入口范围时先明确选择，不以其他学校常见范围代替", async () => {
    await render({ canManageEvaluations: false, canReadDisagreements: false });
    expect(mocks.api.getAIEligibilityPolicy).not.toHaveBeenCalled();
    expect(host.textContent).toContain("完整选择前不会发起查询");
    expect(button("查询当前范围").disabled).toBe(true);
  });

  it("切换真实范围后丢弃迟到策略，读取失败保持可重试且不能用旧版本维护", async () => {
    const previous = deferred<{ policy: AIEligibilityPolicy }>();
    const current = deferred<{ policy: AIEligibilityPolicy }>();
    mocks.api.getAIEligibilityPolicy.mockReturnValueOnce(previous.promise).mockReturnValueOnce(current.promise);
    await render({ initialAxis: { subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3" }, canManageEvaluations: false, canReadDisagreements: false });
    await changeScope("学科", "physics");
    expect(mocks.api.getAIEligibilityPolicy).toHaveBeenLastCalledWith({ subject_code: "physics", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3" });
    // 先返回上一学科成功结果，再让当前学科失败；旧成功不能消除当前错误或携带原策略版本。
    await act(async () => previous.resolve({ policy: { version: 777, subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3", allowed_modes: [] } as unknown as AIEligibilityPolicy }));
    expect(host.textContent).not.toContain("V777");
    await act(async () => current.reject(new Error("current scope unavailable")));
    expect(host.textContent).toContain("当前范围尚未读取成功");
    expect(button("维护当前范围策略").disabled).toBe(true);
    mocks.api.getAIEligibilityPolicy.mockRejectedValueOnce(new ApiClientError(404, "not_found", "not found"));
    await act(async () => button("重新读取当前范围").click());
    expect(button("维护当前范围策略").disabled).toBe(false);
    await act(async () => button("维护当前范围策略").click());
    expect(form(1).values).toMatchObject({ subject_code: "physics", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3", expected_version: 0 });
    expect(host.querySelector<HTMLSelectElement>('[aria-label="学科"]')!.disabled).toBe(true);
  });

  it("入口或学校改变重建范围，旧请求完成也不能覆盖新对象", async () => {
    const previous = deferred<{ policy: AIEligibilityPolicy }>();
    mocks.api.getAIEligibilityPolicy.mockReturnValueOnce(previous.promise);
    await render({ initialAxis: { subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3" }, canManageEvaluations: false, canReadDisagreements: false });
    await render({ tenantID: "school-b", initialAxis: { subject_code: "physics", education_stage: "junior", archetype_code: "structured_steps" }, canManageEvaluations: false, canReadDisagreements: false });
    expect(mocks.api.getAIEligibilityPolicy).toHaveBeenCalledTimes(1);
    await act(async () => previous.resolve({ policy: { version: 999, subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3", allowed_modes: [] } as unknown as AIEligibilityPolicy }));
    expect(host.textContent).not.toContain("V999");
    expect(host.textContent).toContain("当前检查范围：初中 · 物理 · 步骤题 · 风险级别待选择");
    await changeScope("风险级别", "R2");
    expect(mocks.api.getAIEligibilityPolicy).toHaveBeenLastCalledWith({ subject_code: "physics", education_stage: "junior", archetype_code: "structured_steps", risk_tier: "R2" });
  });

  it("维护提交保留已读取的具体范围和版本，R3长作答新策略默认不允许自动形成分数", async () => {
    const axis = { subject_code: "english", education_stage: "senior", archetype_code: "extended_response", risk_tier: "R3" } as const;
    await render({ initialAxis: axis, canManageEvaluations: false, canReadDisagreements: false });
    await act(async () => button("维护当前范围策略").click());
    expect(form(1).values).toMatchObject({ ...axis, expected_version: 0, allowed_modes: ["AI_ASSIST", "HUMAN_PRIMARY"] });
    const saved = { ...axis, id: "policy", tenant_id: "school", min_ocr_quality: 0.9, min_parser_quality: 0.9, min_eval_n: 30, max_severe_error_rate: 0.02, allowed_modes: ["AI_ASSIST", "HUMAN_PRIMARY"], status: "active", version: 1, created_at: "" } as AIEligibilityPolicy;
    mocks.api.putAIEligibilityPolicy.mockResolvedValue({ policy: saved });
    await act(async () => button("保存新版本").click());
    expect(mocks.api.putAIEligibilityPolicy).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ ...axis, expected_version: 0, allowed_modes: ["AI_ASSIST", "HUMAN_PRIMARY"] }));
    expect(host.textContent).toContain("V1");
    expect(host.textContent).toContain("当前检查范围：高中 · 英语 · 长作答 · 风险等级 3（R3）");
  });

  it("只读分歧保持分类文本与计数，不显示操作按钮", async () => {
    mocks.api.listAIHumanDisagreements.mockResolvedValue({ disagreements: [disagreement(), disagreement({ id: "classified", status: "classified" }), disagreement({ id: "routed", status: "routed" })] });
    await render({ canReadEligibility: false, canManageEvaluations: false, canManageDisagreements: false });
    expect(host.textContent).toContain("待审阅严重分歧 1"); expect(host.textContent).toContain("已分类"); expect(host.textContent).toContain("已关联任务");
    expect([...host.querySelectorAll("button")].some((item) => item.textContent === "分类")).toBe(false); expect(mocks.api.listAIHumanDisagreements).toHaveBeenCalledWith({ limit: 100 });
  });

  it("刷新保留选中评测和校准，移除时回退首项", async () => {
    await render({ canReadEligibility: false, canReadDisagreements: false });
    await act(async () => { selectRow("评测", "B"); selectRow("校准", "E"); });
    await act(async () => button("刷新").click());
    expect(detail(0).textContent).toContain("评测-B"); expect(mocks.api.listModelCalibrationEvidence).toHaveBeenLastCalledWith("E");
    mocks.api.listGradingEvaluations.mockResolvedValueOnce({ evaluation_runs: [evaluation("A")] }); mocks.api.listModelCalibrations.mockResolvedValueOnce({ calibrations: [calibration("C")] });
    await act(async () => button("刷新").click());
    expect(detail(0).textContent).toContain("评测-A"); expect(mocks.api.listModelCalibrationEvidence).toHaveBeenLastCalledWith("C");
  });

  it("切换后旧评测和校准详情不能覆盖当前证据", async () => {
    // 挂住 A/C 的详情，让 B/E 先完成，再放行旧请求验证各自 active 清理。
    const oldQuality = deferred<{ quality_summary: GradingEvaluationQualitySummary }>(); const oldEvidence = deferred<{ evidence: ModelCalibrationEvidence[] }>();
    mocks.api.getGradingEvaluationQualitySummary.mockReturnValueOnce(oldQuality.promise); mocks.api.listModelCalibrationEvidence.mockReturnValueOnce(oldEvidence.promise);
    await render({ canReadEligibility: false, canReadDisagreements: false });
    await act(async () => { selectRow("评测", "B"); selectRow("校准", "E"); });
    await act(async () => { oldQuality.resolve({ quality_summary: { ...quality(), page_match_accuracy: { rate: 0.999, observed_count: 999 } } }); oldEvidence.resolve({ evidence: [{ id: "late" } as ModelCalibrationEvidence] }); });
    expect(detail(0).textContent).not.toContain("n=999"); expect(detail(1).textContent).toContain("证据：0 条");
  });

  it("评测和校准状态保持原操作入口与禁用条件", async () => {
    mocks.api.listGradingEvaluations.mockResolvedValue({ evaluation_runs: [evaluation("A", { observation_count: 0 }), evaluation("B", { status: "completed" }), evaluation("I", { status: "invalidated" })] });
    mocks.api.listModelCalibrations.mockResolvedValue({ calibrations: [calibration("C"), calibration("E", { status: "completed" }), calibration("P", { status: "approved" }), calibration("X", { status: "invalidated" })] });
    await render({ canReadEligibility: false, canReadDisagreements: false });
    expect(button("冻结评测", detail(0)).disabled).toBe(true); expect(button("基于评测创建校准", detail(0)).disabled).toBe(true); expect(button("完成校准", detail(1)).disabled).toBe(true);
    await act(async () => { selectRow("评测", "B"); selectRow("校准", "E"); });
    expect(button("基于评测创建校准", detail(0)).disabled).toBe(false); expect(button("批准校准", detail(1))).toBeDefined(); expect(detail(1).querySelector("form")).toBeNull();
    await act(async () => { selectRow("评测", "I"); selectRow("校准", "X"); });
    expect(detail(0).textContent).not.toContain("标记失效"); expect(detail(1).textContent).not.toContain("标记失效");
  });

  it("校准三种转换保持精确请求和反馈，失效原因取消不请求且有效值 trim", async () => {
    await render({ canReadEligibility: false, canReadDisagreements: false });
    mocks.api.listModelCalibrationEvidence.mockResolvedValue({ evidence: [{ id: "evidence" } as ModelCalibrationEvidence] });
    await act(async () => selectRow("校准", "E"));
    await act(async () => button("完成校准", detail(1)).click()); expect(mocks.api.completeModelCalibration).toHaveBeenCalledWith("E"); expect(mocks.success).toHaveBeenLastCalledWith("校准工件已完成。");
    mocks.api.listModelCalibrations.mockResolvedValueOnce({ calibrations: [calibration("E", { status: "completed" })] });
    await act(async () => button("刷新").click()); await act(async () => button("批准校准", detail(1)).click());
    expect(mocks.api.approveModelCalibration).toHaveBeenCalledWith("E"); expect(mocks.success).toHaveBeenLastCalledWith("校准工件已批准；仅精确版本轴可引用。");
    const prompt = vi.spyOn(window, "prompt").mockReturnValueOnce(null).mockReturnValueOnce("  ").mockReturnValueOnce("  obsolete  ");
    await act(async () => button("标记失效", detail(1)).click()); await act(async () => button("标记失效", detail(1)).click()); expect(mocks.api.invalidateModelCalibration).not.toHaveBeenCalled();
    await act(async () => button("标记失效", detail(1)).click()); expect(mocks.api.invalidateModelCalibration).toHaveBeenCalledWith("E", "obsolete"); expect(mocks.success).toHaveBeenLastCalledWith("校准工件已失效。"); expect(prompt).toHaveBeenCalledTimes(3);
  });

  it("证据写入后先重读证据再刷新，忙碌重渲染保留输入和焦点", async () => {
    await render({ canReadEligibility: false, canReadDisagreements: false }); form(5).values = { response_key: "answer", raw_confidence: 0.5 };
    const pending = deferred<{ evidence: ModelCalibrationEvidence[] }>(); mocks.api.listModelCalibrationEvidence.mockReturnValueOnce(pending.promise);
    const input = detail(1).querySelector<HTMLInputElement>('[placeholder="脱敏答卷键"]')!; input.focus();
    await act(async () => button("加入证据", detail(1)).click());
    expect(mocks.api.addModelCalibrationEvidence).toHaveBeenCalledWith("C", { response_key: "answer", raw_confidence: 0.5 }); expect(mocks.api.listGradingEvaluations).toHaveBeenCalledTimes(1); expect(mocks.success).not.toHaveBeenCalled();
    expect(detail(1).querySelector('[placeholder="脱敏答卷键"]')).toBe(input); expect(document.activeElement).toBe(input); expect(button("加入证据", detail(1)).getAttribute("data-loading")).toBe("true");
    await act(async () => pending.resolve({ evidence: [] })); expect(mocks.api.listGradingEvaluations).toHaveBeenCalledTimes(2); expect(mocks.success).toHaveBeenLastCalledWith("置信度观察已加入校准草稿。");
  });

  it("分类和路由使用打开时的 revision，保存等待队列后反馈", async () => {
    await render({ canReadEligibility: false, canManageEvaluations: false }); await act(async () => button("分类").click());
    expect(form(6).setFieldsValue).toHaveBeenCalledWith({ taxonomy: "ai_scoring_error", notes: undefined }); form(6).values = { taxonomy: "rubric_ambiguity", notes: "note" };
    const pending = deferred<{ disagreements: AIHumanDisagreement[] }>(); mocks.api.listAIHumanDisagreements.mockReturnValueOnce(pending.promise);
    await act(async () => button("保存分类").click()); expect(mocks.api.classifyAIHumanDisagreement).toHaveBeenCalledWith("D", { taxonomy: "rubric_ambiguity", notes: "note", expected_revision: 7 }); expect(mocks.success).not.toHaveBeenCalled();
    await act(async () => pending.resolve({ disagreements: [disagreement({ revision: 8 })] })); expect(mocks.success).toHaveBeenLastCalledWith("已记录人工分类；它不会自动更改分数或认定责任。");
    await act(async () => button("关联人工任务").click()); expect(form(7).resetFields).toHaveBeenCalledTimes(1); form(7).values = { review_task_id: "review" };
    await act(async () => button("关联任务").click()); expect(mocks.api.routeAIHumanDisagreement).toHaveBeenCalledWith("D", { review_task_id: "review", expected_revision: 8 }); expect(mocks.success).toHaveBeenLastCalledWith("分歧已关联人工阅卷任务；不会创建或直接修改最终成绩。");
  });

  it("新建评测等待列表刷新后选中新 id，保存失败恢复 busy", async () => {
    await render({ canReadEligibility: false, canReadDisagreements: false }); form(2).values = { key: "new" };
    mocks.api.createGradingEvaluation.mockResolvedValue({ evaluation_run: evaluation("N") });
    const pending = deferred<{ evaluation_runs: GradingEvaluationRun[] }>(); mocks.api.listGradingEvaluations.mockReturnValueOnce(pending.promise);
    // 先通过真实入口打开草稿；关闭状态下保留的测试DOM不能充当可提交的编辑会话。
    await act(async () => button("登记评分验证报告").click());
    await act(async () => button("登记报告", host.querySelector('[data-drawer="登记已有评分验证报告"]')!).click());
    expect(form(2).resetFields).toHaveBeenCalledTimes(1); expect(mocks.success).not.toHaveBeenCalled(); expect(mocks.drawers.get("登记已有评分验证报告")?.open).toBe(false);
    await act(async () => pending.resolve({ evaluation_runs: [evaluation("A"), evaluation("N")] })); expect(detail(0).textContent).toContain("评测-N"); expect(mocks.api.getGradingEvaluationQualitySummary).toHaveBeenLastCalledWith("N");
    mocks.api.addGradingEvaluationObservation.mockRejectedValueOnce(new Error("write failed")); form(3).values = { response_key: "answer" };
    await act(async () => button("录入对齐观察", detail(0)).click());
    await act(async () => button("保存观察").click()); expect(mocks.api.addGradingEvaluationObservation).toHaveBeenCalledWith("N", { response_key: "answer" }); expect(button("保存观察").getAttribute("data-loading")).toBe("false"); expect(mocks.error).toHaveBeenCalled();
  });

  it("质量缺值与两个列表截取数量保持原显示", async () => {
    mocks.api.listGradingEvaluationSliceMetrics.mockResolvedValue({ slice_metrics: Array.from({ length: 13 }, (_, index) => ({ id: String(index), dimension: "slice", value: `slice-${index}`, metrics: { sample_count: 1, severe_error_rate: 0 } })) });
    mocks.api.listGradingEvaluationResponseDifficulty.mockResolvedValue({ response_difficulty: Array.from({ length: 9 }, (_, index) => ({ id: String(index), difficulty_band: `difficulty-${index}`, response_key: `answer-${index}`, severe_error: false, normalized_error: 0.5 })) });
    await render({ canReadEligibility: false, canReadDisagreements: false });
    expect(detail(0).textContent).toContain("未采集 · n=0"); expect(detail(0).textContent).toContain("slice-11"); expect(detail(0).textContent).not.toContain("slice-12"); expect(detail(0).textContent).toContain("difficulty-7"); expect(detail(0).textContent).not.toContain("difficulty-8");
    expect([...mocks.drawers.values()].map((item) => item.width)).toEqual([560, 620, 680, 650, 620, 520, 520]); expect(mocks.forms.size).toBe(7);
  });
});
