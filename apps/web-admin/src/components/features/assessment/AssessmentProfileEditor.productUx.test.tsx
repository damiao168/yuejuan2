// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AssessmentProfileEditor } from "./AssessmentProfileEditor";
import { ApiClientError } from "../../../api/client";
import type { AssessmentProfile, AssessmentSnapshot, EducationStage, EvidenceType, QuestionArchetype, SubjectCode, SubjectProfile } from "../../../api/assessment";
import type { Question } from "../../../api/papers";

const api = vi.hoisted(() => ({ profile: vi.fn(), snapshot: vi.fn(), profiles: vi.fn(), archetypes: vi.fn(), put: vi.fn(), error: vi.fn(), success: vi.fn(), changed: vi.fn() }));
const fields = vi.hoisted(() => ({ values: {} as Record<string, unknown>, listeners: new Set<() => void>(), validate: vi.fn(), actions: {} as Record<string, () => void> }));
vi.mock("../../../api/assessment", () => ({ getQuestionAssessmentProfile: api.profile, getQuestionAssessmentSnapshot: api.snapshot, listSubjectProfiles: api.profiles, listQuestionArchetypes: api.archetypes, updateQuestionAssessmentProfile: api.put }));
vi.mock("../../StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
// 表单替身保留字段订阅、Select 显式 disabled 优先于 Form 的合并规则，以及字段先更新再回调的顺序。
// Ant Design 的实际表单注册、下拉和弹窗行为由浏览器夹具另行验收，不能由这些测试代替。
vi.mock("antd", async () => {
  const React = await import("react");
  const fieldName = React.createContext(""); const formDisabled = React.createContext(false);
  const update = (values: Record<string, unknown>) => { fields.values = { ...fields.values, ...values }; for (const notify of fields.listeners) notify(); };
  const form = { getFieldValue: (key: string) => fields.values[key], setFieldValue: (key: string, value: unknown) => update({ [key]: value }), setFieldsValue: update,
    resetFields: () => { fields.values = {}; for (const notify of fields.listeners) notify(); }, validateFields: () => fields.validate() };
  const Form = Object.assign(({ children, disabled }: { children: ReactNode; disabled?: boolean }) => <formDisabled.Provider value={Boolean(disabled)}>{children}</formDisabled.Provider>, {
    useForm: () => [form], useWatch: (key: string) => React.useSyncExternalStore((notify) => { fields.listeners.add(notify); return () => fields.listeners.delete(notify); }, () => fields.values[key]),
    Item: ({ name, label, children }: { name: string; label: string; children: ReactNode }) => <fieldName.Provider value={name}><label>{label}{children}</label></fieldName.Provider>
  });
  return { App: { useApp: () => ({ message: { error: api.error, success: api.success } }) }, Form,
    Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => { if (typeof children === "string") fields.actions[children] = onClick; return <button type="button" disabled={disabled || loading} onClick={onClick}>{children}</button>; },
    Select: ({ options, onChange, mode, disabled: explicitlyDisabled }: { options: Array<{ label: string; value: string }>; onChange?: (value: never) => void; mode?: string; disabled?: boolean }) => {
      const name = React.useContext(fieldName); const inheritedDisabled = React.useContext(formDisabled); const disabled = explicitlyDisabled ?? inheritedDisabled; const value = fields.values[name] ?? (mode === "multiple" ? [] : "");
      return <select aria-label={name} disabled={disabled} multiple={mode === "multiple"} value={value as string | string[]} onChange={(event) => {
        const next = mode === "multiple" ? [...event.target.selectedOptions].map((item) => item.value) : event.target.value;
        update({ [name]: next }); onChange?.(next as never);
      }}>{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>;
    },
    Drawer: ({ open, children, footer, title, onClose }: { open: boolean; children: ReactNode; footer: ReactNode; title: ReactNode; onClose: () => void }) => { fields.actions.drawerClose = onClose; return open ? <section role="dialog">{title}{children}{footer}</section> : null; },
    Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>, Spin: () => <span>loading</span>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div> };
});

const mathEvidence: EvidenceType[] = ["selected_option", "exact_text", "text_span", "numeric_value", "math_expression", "math_step", "unit_value", "diagram_feature"];
function profile(id: string, allowed = mathEvidence, stage: EducationStage = "junior", subject: SubjectCode = "mathematics"): SubjectProfile {
  return { id, tenant_id: "tenant-1", code: id, education_stage: stage, subject_code: subject, version: 1, status: "active", parser_policy: {}, created_at: "2026-10-06", updated_at: "2026-10-06", evidence_policy: { allowed_types: allowed }, scoring_default: { mode: "AI_ASSIST", require_evidence: true, human_review_below_confidence: true } };
}
const archetypes: QuestionArchetype[] = [
  { code: "selected_response", default_scoring_mode: "RULE_AUTO", evidence_types: ["selected_option"], response_schema: {} },
  { code: "exact_text", default_scoring_mode: "RULE_AUTO", evidence_types: ["exact_text", "text_span"], response_schema: {} },
  { code: "numeric_expression", default_scoring_mode: "RULE_AUTO", evidence_types: ["numeric_value", "math_expression", "unit_value"], response_schema: {} },
  { code: "short_constructed", default_scoring_mode: "AI_ASSIST", evidence_types: ["text_span", "concept", "relation"], response_schema: {} },
  { code: "structured_steps", default_scoring_mode: "AI_ASSIST", evidence_types: ["text_span", "math_expression", "math_step", "unit_value", "chemical_equation"], response_schema: {} },
  { code: "extended_response", default_scoring_mode: "HUMAN_PRIMARY", evidence_types: ["text_span", "concept", "relation"], response_schema: {} }
];
function saved(overrides: Partial<AssessmentProfile> = {}): AssessmentProfile {
  return { subject_profile_id: "math-v1", education_stage: "junior", subject_code: "mathematics", archetype_code: "short_constructed", allowed_evidence_types: ["text_span"], risk_tier: "R2", scoring_policy: { mode: "MANUAL_ONLY", require_evidence: true }, revision: 7, ...overrides } as AssessmentProfile;
}
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }

describe("逐题评分配置的合法默认与恢复", () => {
  let root: Root; let host: HTMLDivElement;
  const question = (type = "short_answer", id = "q1") => ({ id, question_no: id, question_type: type }) as Question;
  const render = async (props: Partial<Parameters<typeof AssessmentProfileEditor>[0]> = {}) => { await act(async () => root.render(<AssessmentProfileEditor examId="exam-1" examSubject="数学" examStatus="configured" examGradingMode="ai_assisted" question={question()} canManage onChanged={api.changed} {...props} />)); };
  const button = (label: string) => [...host.querySelectorAll("button")].find((item) => item.textContent === label)!;
  // 既有证据/并发用例明确模拟选择初中，不再依赖未知学段被默认成初中。
  // 下方未知学段用例直接调用配置入口，独立验证必须人工选择的边界。
  const click = async (label: string) => {
    await act(async () => { expect(button(label)).toBeDefined(); button(label).click(); });
    const stage = host.querySelector<HTMLSelectElement>('select[aria-label="education_stage"]');
    if (["配置", "重试"].includes(label) && fields.values.education_stage == null && stage && !stage.disabled) {
      await act(async () => { stage.value = "junior"; stage.dispatchEvent(new Event("change", { bubbles: true })); });
    }
  };
  const force = async (label: string) => { await act(async () => fields.actions[label]()); };
  const select = async (name: string, values: string | string[]) => { await act(async () => {
    const node = host.querySelector<HTMLSelectElement>(`select[aria-label="${name}"]`)!; expect(node).not.toBeNull();
    if (Array.isArray(values)) for (const option of node.options) option.selected = values.includes(option.value); else node.value = values;
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }); };
  const options = (name: string) => [...host.querySelectorAll<HTMLOptionElement>(`select[aria-label="${name}"] option`)].map((item) => item.value);
  beforeEach(() => {
    vi.resetAllMocks(); fields.values = {}; fields.actions = {}; Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    fields.validate.mockImplementation(async () => ({ ...fields.values }));
    api.profile.mockRejectedValue(new ApiClientError(404, "assessment_not_found", "missing")); api.snapshot.mockRejectedValue(new ApiClientError(404, "assessment_not_found", "missing"));
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1")] }); api.archetypes.mockResolvedValue({ question_archetypes: archetypes });
    api.put.mockImplementation(async (_exam, _question, values) => ({ assessment_profile: { ...values, revision: 8 } }));
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("未保存题目时保留配置说明并禁用入口，不读取模板", async () => {
    await render({ question: null });
    expect(host.textContent).toContain("保存题目后可配置学科评分策略。");
    expect(button("配置").disabled).toBe(true);
    expect(api.profiles).not.toHaveBeenCalled();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it.each(["none", "saved", "snapshot"])("标题区保留 %s 配置状态，不改变打开后的字段", async (source) => {
    if (source === "saved") api.profile.mockResolvedValue({ assessment_profile: saved() });
    if (source === "snapshot") api.snapshot.mockResolvedValue({ assessment_snapshot: { ...saved(), scoring_policy_snapshot: saved().scoring_policy } as unknown as AssessmentSnapshot });
    await render(); await click("配置");
    const heading = host.querySelector(".assessment-profile-section > div")!;
    expect(heading.textContent?.includes("已配置")).toBe(source === "saved");
    expect(heading.textContent?.includes("已冻结")).toBe(source === "snapshot");
    expect(host.querySelectorAll(".assessment-form-section")).toHaveLength(3);
    expect(fields.values.evidence_types).toEqual(["text_span"]);
  });

  it("冻结快照与无管理权限提醒同时展示，历史证据保持只读", async () => {
    const historic = saved({ allowed_evidence_types: ["concept"] });
    api.snapshot.mockResolvedValue({ assessment_snapshot: { ...historic, scoring_policy_snapshot: historic.scoring_policy } as unknown as AssessmentSnapshot });
    await render({ canManage: false }); await click("查看配置");
    expect(host.textContent).toContain("考试配置已冻结");
    expect(host.textContent).toContain("当前为只读模板");
    expect(options("evidence_types")).toContain("concept");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="evidence_types"]')?.disabled).toBe(true);
    expect(button("保存配置")).toBeUndefined(); expect(api.put).not.toHaveBeenCalled();
  });

  it("未知学段不默认初中，明确选择高中后只使用高中模板", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("senior-v1", mathEvidence, "senior")] });
    await render(); await force("配置");
    expect(fields.values.education_stage).toBeUndefined(); expect(fields.values.subject_profile_id).toBe("");
    expect(fields.values.evidence_types).toEqual([]); expect(button("保存配置").disabled).toBe(true);
    await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
    await select("education_stage", "senior"); expect(fields.values.subject_profile_id).toBe("senior-v1");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({subject_profile_id:"senior-v1"}));
  });

  it.each([
    ["short_answer", ["text_span"]],
    ["calculation", ["text_span", "math_expression", "math_step", "unit_value"]]
  ])("数学 %s 默认与可选证据只取学科题型交集，并能直接保存", async (type, evidence) => {
    await render({ question: question(type as string) }); await click("配置");
    expect(fields.values.evidence_types).toEqual(evidence); expect(options("evidence_types")).toEqual(evidence);
    expect(button("保存配置").disabled).toBe(false); await click("保存配置");
    expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ allowed_evidence_types: evidence, expected_revision: 0 })); expect(api.changed).toHaveBeenCalledTimes(1);
  });

  it.each(["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"])("普通 %s 默认 RULE_AUTO，学科 AI_ASSIST 不覆盖题型", async (type) => {
    await render({ question: question(type) }); await click("配置"); expect(fields.values.scoring_mode).toBe("RULE_AUTO");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ scoring_policy: expect.objectContaining({ mode: "RULE_AUTO" }) }));
  });

  it.each(["double_mark", "blind_double_mark"])("%s 默认双评，切换题型仍保留双评默认", async (mode) => {
    await render({ examGradingMode: mode }); await click("配置"); expect(fields.values.scoring_mode).toBe("DUAL_HUMAN");
    await select("question_archetype", "selected_response"); expect(fields.values.scoring_mode).toBe("DUAL_HUMAN");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ scoring_policy: expect.objectContaining({ mode: "DUAL_HUMAN" }) }));
  });

  it("已保存模式与证据优先，读取不会重选全部默认证据", async () => {
    api.profile.mockResolvedValue({ assessment_profile: saved() }); await render({ examGradingMode: "double_mark" }); await click("配置");
    expect(fields.values.scoring_mode).toBe("MANUAL_ONLY"); expect(fields.values.evidence_types).toEqual(["text_span"]);
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ expected_revision: 7, scoring_policy: expect.objectContaining({ mode: "MANUAL_ONLY" }) }));
  });

  it("冻结快照高于已保存配置和默认，历史证据保留展示且不能保存", async () => {
    api.profile.mockResolvedValue({ assessment_profile: saved() });
    api.snapshot.mockResolvedValue({ assessment_snapshot: { ...saved({ allowed_evidence_types: ["concept"] }), scoring_policy_snapshot: { mode: "DUAL_HUMAN" } } as unknown as AssessmentSnapshot });
    await render(); await click("配置"); expect(fields.values.scoring_mode).toBe("DUAL_HUMAN"); expect(fields.values.evidence_types).toEqual(["concept"]);
    expect(options("evidence_types")).toContain("concept"); expect(button("保存配置")).toBeUndefined(); await force("drawerClose"); expect(api.put).not.toHaveBeenCalled();
  });

  it("只读已保存不兼容证据仍按原配置展示，不自动清理", async () => {
    api.profile.mockResolvedValue({ assessment_profile: saved({ allowed_evidence_types: ["concept"] }) }); await render({ canManage: false }); await click("查看配置");
    expect(fields.values.evidence_types).toEqual(["concept"]); expect(options("evidence_types")).toContain("concept"); expect(button("保存配置")).toBeUndefined();
  });

  it("综合考试的模板切换清理失效证据并保留合法选择，学段和学科切换使用新模板交集", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("math-v2", ["unit_value"]), profile("senior-v1", ["math_step"], "senior"), profile("english-v1", ["text_span"], "senior", "english")] });
    await render({ examSubject: "综合", question: question("calculation") }); await click("配置"); await select("subject_code", "mathematics"); await select("subject_profile_id", "math-v2");
    expect(fields.values.evidence_types).toEqual(["unit_value"]); expect(options("evidence_types")).toEqual(["unit_value"]); expect(host.textContent).toContain("已移除");
    await select("education_stage", "senior"); expect(fields.values.subject_profile_id).toBe("senior-v1"); expect(fields.values.evidence_types).toEqual(["math_step"]);
    await select("subject_code", "english"); expect(fields.values.subject_profile_id).toBe("english-v1"); expect(fields.values.evidence_types).toEqual(["text_span"]);
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "english-v1", allowed_evidence_types: ["text_span"] }));
  });

  it("题型切换使用合法交集与题型默认模式，明确提示重新核对", async () => {
    await render(); await click("配置"); await select("question_archetype", "numeric_expression");
    expect(fields.values.evidence_types).toEqual(["numeric_value", "math_expression", "unit_value"]); expect(fields.values.scoring_mode).toBe("RULE_AUTO"); expect(host.textContent).toContain("请重新核对证据选择");
  });

  it("空交集和无匹配模板禁用保存，强制回调也不提交", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1", ["selected_option"])] }); await render(); await click("配置");
    expect(fields.values.evidence_types).toEqual([]); expect(button("保存配置").disabled).toBe(true); expect(host.textContent).toContain("没有可用的评分证据");
    await force("保存配置"); expect(api.put).not.toHaveBeenCalled(); await select("education_stage", "senior"); expect(fields.values.subject_profile_id).toBe(""); await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
  });

  it("保存前按提交值再次校验失效证据，不依赖上一次按钮重绘", async () => {
    await render(); await click("配置"); fields.values = { ...fields.values, evidence_types: ["concept"] }; await force("保存配置");
    expect(api.put).not.toHaveBeenCalled(); expect(api.error).toHaveBeenCalledWith(expect.stringContaining("合法证据"));
  });

  it("客观题显式选择 AI 模式显示会转人工的提示，已有策略不被自动覆盖", async () => {
    await render({ question: question("single_choice") }); await click("配置"); await select("scoring_mode", "AI_ASSIST");
    expect(host.textContent).toContain("会转入人工评分"); expect(fields.values.scoring_mode).toBe("AI_ASSIST");
    expect(host.textContent).toContain("如需自动判分，请选择规则自动评分");
  });

  it.each(["short_answer", "calculation", "essay", "discussion"])("%s 显式选择规则模式先说明人工路径，保存不改写选择", async (type) => {
    await render({ question: question(type) }); await click("配置"); await select("scoring_mode", "RULE_AUTO");
    expect(host.textContent).toContain("会转入人工评分");
    expect(host.textContent).toMatch(/规则.*不会自动/);
    expect(fields.values.scoring_mode).toBe("RULE_AUTO");
    expect(button("保存配置").disabled).toBe(false); await click("保存配置");
    expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ scoring_policy: expect.objectContaining({ mode: "RULE_AUTO" }) }));
  });

  it.each(["saved", "snapshot"])("%s 的主观规则策略按原值展示人工提示，只读不改写历史", async (source) => {
    const historic = saved({ scoring_policy: { mode: "RULE_AUTO", require_evidence: true } });
    if (source === "snapshot") api.snapshot.mockResolvedValue({ assessment_snapshot: { ...historic, scoring_policy_snapshot: historic.scoring_policy } as unknown as AssessmentSnapshot });
    else api.profile.mockResolvedValue({ assessment_profile: historic });
    await render({ canManage: false }); await click("查看配置");
    expect(host.textContent).toContain("会转入人工评分"); expect(fields.values.scoring_mode).toBe("RULE_AUTO");
    expect(button("保存配置")).toBeUndefined(); expect(api.put).not.toHaveBeenCalled();
  });

  it("主观题切回 AI 辅助或人工时清除规则能力警示，不改变既有风险门禁", async () => {
    await render({ question: question("essay") }); await click("配置"); await select("scoring_mode", "RULE_AUTO");
    expect(host.textContent).toContain("会转入人工评分");
    await select("scoring_mode", "AI_ASSIST"); expect(host.textContent).not.toContain("会转入人工评分");
    await select("scoring_mode", "HUMAN_PRIMARY"); expect(host.textContent).not.toContain("会转入人工评分");
    await select("risk_tier", "R3"); await select("scoring_mode", "AI_FAST_CONFIRM");
    expect(host.textContent).toContain("该组合不允许保存"); expect(button("保存配置").disabled).toBe(true);
    await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
  });

  it("数学考试只允许数学模板，学科由考试决定但仍可切换学段", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("math-senior", mathEvidence, "senior"), profile("english-v1", ["text_span"], "junior", "english")] });
    await render(); await click("配置");
    expect(options("subject_code")).toEqual(["mathematics"]);
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="subject_code"]')?.disabled).toBe(true);
    expect(options("subject_profile_id")).toEqual(["math-v1"]); expect(host.textContent).toContain("学科由考试设置确定：数学");
    await select("education_stage", "senior"); expect(fields.values.subject_profile_id).toBe("math-senior");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "math-senior" }));
  });

  it.each([[" Math ", "mathematics"], [" MATHEMATICS ", "mathematics"], ["civics", "ethics_politics"], ["政治", "ethics_politics"], ["ENGLISH", "english"]] as const)("考试学科 %s 使用后台识别的规范学科 %s", async (examSubject, canonical) => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("matching-v1", ["text_span"], "junior", canonical)] });
    await render({ examSubject }); await click("配置");
    expect(fields.values.subject_code).toBe(canonical); expect(fields.values.subject_profile_id).toBe("matching-v1"); expect(options("subject_code")).toEqual([canonical]);
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "matching-v1" }));
  });

  it.each(["综合", "unknown_subject", ""])("未知考试学科 %s 不默认数学，明确选择英语后仍可保存", async (examSubject) => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("english-v1", ["text_span"], "junior", "english")] });
    await render({ examSubject }); await click("配置");
    expect(fields.values.subject_code).toBeUndefined(); expect(fields.values.subject_profile_id).toBe(""); expect(button("保存配置").disabled).toBe(true);
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="subject_code"]')?.disabled).toBe(false); expect(options("subject_code")).toContain("english");
    await select("subject_code", "english"); expect(fields.values.subject_profile_id).toBe("english-v1"); await click("保存配置");
    expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "english-v1", allowed_evidence_types: ["text_span"] }));
  });

  it("综合考试的既有英语模板和评分意图保留，可以按原配置保存", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("english-v1", ["text_span"], "junior", "english")] });
    api.profile.mockResolvedValue({ assessment_profile: saved({ subject_profile_id: "english-v1", subject_code: "english" }) });
    await render({ examSubject: "综合" }); await click("配置"); expect(fields.values.subject_code).toBe("english"); expect(fields.values.scoring_mode).toBe("MANUAL_ONLY");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "english-v1", expected_revision: 7 }));
  });

  it("学科合法证据不能绕过考试模板约束，强制提交异学科不会发出请求", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("english-v1", ["text_span"], "junior", "english")] });
    await render(); await click("配置");
    // 模拟验证期间被改写的值，让同一模板和证据仍合法；保存必须另查考试学科，不能只检查交集。
    fields.values = { ...fields.values, subject_code: "english", subject_profile_id: "english-v1", evidence_types: ["text_span"] };
    await force("保存配置"); expect(api.put).not.toHaveBeenCalled(); expect(api.error).toHaveBeenCalledWith(expect.stringContaining("本场考试学科为数学")); expect(api.success).not.toHaveBeenCalled();
  });

  it("数学考试的旧英语配置保留展示，明确改用考试学科后才能保存", async () => {
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("english-v1", ["text_span"], "junior", "english")] });
    api.profile.mockResolvedValue({ assessment_profile: saved({ subject_profile_id: "english-v1", subject_code: "english" }) });
    await render(); await click("配置");
    expect(fields.values.subject_code).toBe("english"); expect(fields.values.subject_profile_id).toBe("english-v1"); expect(host.textContent).toContain("已有配置学科与考试不一致"); expect(button("保存配置").disabled).toBe(true);
    await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
    await click("改用考试学科"); expect(fields.values.subject_code).toBe("mathematics"); expect(fields.values.subject_profile_id).toBe("math-v1"); expect(fields.values.scoring_mode).toBe("MANUAL_ONLY");
    await click("保存配置"); expect(api.put).toHaveBeenCalledWith("exam-1", "q1", expect.objectContaining({ subject_profile_id: "math-v1", expected_revision: 7 }));
  });

  it.each(["snapshot", "readonly"])("%s 的历史异学科配置只读保留，不能改用考试学科或提交", async (mode) => {
    const legacy = saved({ subject_profile_id: "english-v1", subject_code: "english" });
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("english-v1", ["text_span"], "junior", "english")] });
    if (mode === "snapshot") api.snapshot.mockResolvedValue({ assessment_snapshot: { ...legacy, scoring_policy_snapshot: legacy.scoring_policy } as unknown as AssessmentSnapshot });
    else api.profile.mockResolvedValue({ assessment_profile: legacy });
    await render({ canManage: mode === "snapshot" }); await click(mode === "snapshot" ? "配置" : "查看配置");
    expect(fields.values.subject_code).toBe("english"); expect(options("subject_code")).toContain("english"); expect(fields.values.subject_profile_id).toBe("english-v1");
    expect(button("改用考试学科")).toBeUndefined(); expect(button("保存配置")).toBeUndefined(); expect(api.put).not.toHaveBeenCalled();
  });

  it.each(["snapshot", "readonly", "status"])("未知学科 %s 仍继承只读表单状态，学科下拉不能操作", async (mode) => {
    const legacy = saved(); api.profile.mockResolvedValue({ assessment_profile: legacy });
    if (mode === "snapshot") api.snapshot.mockResolvedValue({ assessment_snapshot: { ...legacy, scoring_policy_snapshot: legacy.scoring_policy } as unknown as AssessmentSnapshot });
    await render({ examSubject: "综合", canManage: mode !== "readonly", examStatus: mode === "status" ? "published" : "configured" });
    await click(mode === "snapshot" ? "配置" : "查看配置");
    expect(fields.values.subject_code).toBe("mathematics");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="subject_code"]')?.disabled).toBe(true);
    expect(button("保存配置")).toBeUndefined(); expect(api.put).not.toHaveBeenCalled();
  });

  it("综合考试提交等待回执时学科下拉仍禁用，失败后按原选择恢复编辑", async () => {
    const pending = deferred<unknown>(); api.put.mockReturnValueOnce(pending.promise);
    api.profiles.mockResolvedValue({ subject_profiles: [profile("math-v1"), profile("english-v1", ["text_span"], "junior", "english")] });
    await render({ examSubject: "综合" }); await click("配置"); await select("subject_code", "english"); await click("保存配置");
    expect(api.put).toHaveBeenCalledTimes(1); expect(fields.values.subject_code).toBe("english");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="subject_code"]')?.disabled).toBe(true);
    await act(async () => pending.reject(new Error("保存暂时失败")));
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="subject_code"]')?.disabled).toBe(false);
    expect(fields.values.subject_code).toBe("english"); expect(fields.values.subject_profile_id).toBe("english-v1");
  });

  it.each(["AI_ASSIST", "AI_FAST_CONFIRM", "RULE_AUTO", "HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY"])("公式题在 %s 下明确需要人工，不再建议规则自动评分", async (mode) => {
    await render({ question: question("formula") }); await click("配置"); await select("scoring_mode", mode);
    expect(host.textContent).toContain("本题需要教师人工评分"); expect(host.textContent).toContain("公式题目前需要教师人工评分");
    expect(host.textContent).not.toContain("如需自动判分，请选择规则自动评分"); expect(fields.values.scoring_mode).toBe(mode);
  });

  it("编程题保留已有评分策略并显示人工要求", async () => {
    api.profile.mockResolvedValue({ assessment_profile: saved({ archetype_code: "extended_response", scoring_policy: { mode: "RULE_AUTO", require_evidence: true } }) });
    await render({ question: question("coding") }); await click("配置");
    expect(host.textContent).toContain("编程题目前需要教师人工评分"); expect(fields.values.scoring_mode).toBe("RULE_AUTO"); expect(host.textContent).not.toContain("如需自动判分，请选择规则自动评分");
  });

  it("读取失败或仍在读取时禁止旧表单保存，重试成功后恢复", async () => {
    const pending = deferred<{ subject_profiles: SubjectProfile[] }>(); api.profiles.mockReturnValueOnce(pending.promise);
    await render(); await click("配置"); expect(button("保存配置").disabled).toBe(true); await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
    expect(host.textContent).toContain("正在读取评分模板"); expect(host.querySelector("select")).toBeNull(); expect(host.querySelector(".assessment-form-section")).toBeNull();
    await act(async () => pending.reject(new Error("模板暂不可用"))); expect(host.textContent).toContain("配置加载失败"); await force("保存配置"); expect(api.put).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("正在读取评分模板"); expect(host.querySelector("select")).toBeNull();
    await click("重试"); expect(button("保存配置").disabled).toBe(false); expect(host.querySelectorAll(".assessment-form-section")).toHaveLength(3);
    expect(fields.values.evidence_types).toEqual(["text_span"]); await click("保存配置"); expect(api.put).toHaveBeenCalledTimes(1);
  });

  it("关闭重开后旧读取不能覆盖新模板，旧关闭回调也不能关闭新窗口", async () => {
    const pending = deferred<{ subject_profiles: SubjectProfile[] }>(); api.profiles.mockReturnValueOnce(pending.promise);
    await render(); await click("配置"); const oldClose = fields.actions.drawerClose; await click("关闭"); await click("配置");
    await act(async () => { oldClose(); pending.resolve({ subject_profiles: [profile("old-math", ["concept"])] }); });
    expect(host.querySelector('[role="dialog"]')).not.toBeNull(); expect(fields.values.subject_profile_id).toBe("math-v1"); expect(fields.values.evidence_types).toEqual(["text_span"]);
  });

  it("题目 A→B→A 的旧读取不能恢复旧配置", async () => {
    const pending = deferred<{ assessment_profile: AssessmentProfile }>(); api.profile.mockReturnValueOnce(pending.promise);
    await render(); await click("配置"); await render({ question: question("single_choice", "q2") }); await render(); await click("配置");
    await act(async () => pending.resolve({ assessment_profile: saved({ allowed_evidence_types: ["concept"] }) }));
    expect(fields.values.evidence_types).toEqual(["text_span"]); expect(fields.values.scoring_mode).toBe("AI_ASSIST");
  });

  it("异步表单校验期间切换考试，旧校验不能提交到任何考试", async () => {
    const pending = deferred<Record<string, unknown>>(); await render(); await click("配置"); const previous = { ...fields.values }; fields.validate.mockReturnValueOnce(pending.promise);
    await force("保存配置"); await render({ examId: "exam-2" }); await act(async () => pending.resolve(previous)); expect(api.put).not.toHaveBeenCalled();
  });

  it("保存开始的同一事件重复回调只提交一次，失败保留草稿并允许重试", async () => {
    const pending = deferred<unknown>(); api.put.mockReturnValueOnce(pending.promise); await render(); await click("配置");
    await act(async () => { fields.actions["保存配置"](); fields.actions["保存配置"](); fields.actions.drawerClose(); }); expect(api.put).toHaveBeenCalledTimes(1); expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    await act(async () => pending.reject(new Error("暂时失败"))); expect(fields.values.evidence_types).toEqual(["text_span"]); expect(button("保存配置").disabled).toBe(false);
    await click("保存配置"); expect(api.put).toHaveBeenCalledTimes(2); expect(api.changed).toHaveBeenCalledTimes(1);
  });

  it("切换题目后旧保存完成不能关闭新窗口或写入旧状态", async () => {
    const pending = deferred<{ assessment_profile: AssessmentProfile }>(); api.put.mockReturnValueOnce(pending.promise); await render(); await click("配置"); await click("保存配置");
    await render({ question: question("single_choice", "q2") }); await click("配置"); await act(async () => pending.resolve({ assessment_profile: saved() }));
    expect(fields.values.scoring_mode).toBe("RULE_AUTO"); expect(host.querySelector('[role="dialog"]')).not.toBeNull(); expect(api.changed).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled();
  });
});
