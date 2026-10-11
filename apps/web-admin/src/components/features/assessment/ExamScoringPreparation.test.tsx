// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { Exam } from "../../../api/exams";
import type { AssessmentProfile, AssessmentProfileInput, SubjectProfile } from "../../../api/assessment";
import { ExamScoringPreparation } from "./ExamScoringPreparation";

const api = vi.hoisted(() => ({ questions: vi.fn(), profiles: vi.fn(), types: vi.fn(), get: vi.fn(), put: vi.fn(), grades: vi.fn(), classes: vi.fn(), confirm: vi.fn(), error: vi.fn(), success: vi.fn(), changed: vi.fn() }));
vi.mock("../../../api/papers", () => ({ listQuestions: api.questions }));
vi.mock("../../../api/assessment", () => ({ listSubjectProfiles: api.profiles, listQuestionArchetypes: api.types, getQuestionAssessmentProfile: api.get, updateQuestionAssessmentProfile: api.put }));
vi.mock("../../../api/org", () => ({ listGrades: api.grades, listClasses: api.classes }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { error: api.error, success: api.success }, modal: { confirm: api.confirm } }) },
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <aside>{message}{description}</aside>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Select: ({ value, onChange, options, disabled, "aria-label": label }: { value?: string; onChange: (value: string) => void; options: { value: string; label: string }[]; disabled?: boolean; "aria-label": string }) => <select aria-label={label} value={value ?? ""} disabled={disabled} onChange={(event) => onChange(event.target.value)}><option value="">请选择</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Table: ({ dataSource, columns }: { dataSource: unknown[]; columns: { title: string; render: (_: undefined, row: never) => ReactNode }[] }) => <div>{dataSource.map((row, index) => <div key={index}>{columns.map((column) => <span key={column.title}>{column.render(undefined, row as never)}</span>)}</div>)}</div>
}));

describe("可见的开考评分配置", () => {
  let root: Root; let host: HTMLDivElement; let saved: Map<string, AssessmentProfile>;
  const exam = { id: "exam", school_id: "school", class_ids: ["class"], subject: "math", status: "configured", grading_mode: "double_mark" } as Exam;
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); saved = new Map();
    api.questions.mockResolvedValue({ questions: [1, 2].map((number) => ({ id: `q${number}`, question_no: String(number), question_type: "short_answer" })) });
    api.profiles.mockResolvedValue({ subject_profiles: [{ id: "senior-math", subject_code: "mathematics", education_stage: "senior", version: 1, evidence_policy: { allowed_types: ["text_span"] }, scoring_default: { mode: "AI_ASSIST" } }] as unknown as SubjectProfile[] });
    api.types.mockResolvedValue({ question_archetypes: [{ code: "short_constructed", evidence_types: ["text_span", "concept"], default_scoring_mode: "AI_ASSIST" }] });
    api.grades.mockResolvedValue({ grades: [{ id: "grade", school_id: "school", education_stage: "senior" }] });
    api.classes.mockResolvedValue({ classes: [{ id: "class", grade_id: "grade" }] });
    api.get.mockImplementation(async (_: string, id: string) => { if (saved.has(id)) return { assessment_profile: saved.get(id) }; throw new ApiClientError(404, "missing", "missing"); });
    api.put.mockImplementation(async (_: string, id: string, input: AssessmentProfileInput) => { const result = { ...input, scoring_policy: input.scoring_policy, revision: 1 } as unknown as AssessmentProfile; saved.set(id, result); return { assessment_profile: result }; });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(canManage = true) { await act(async () => root.render(<ExamScoringPreparation exam={exam} canManage={canManage} onChanged={api.changed} onNavigate={vi.fn()} />)); }
  async function selectProfile() { await act(async () => { const select = host.querySelector<HTMLSelectElement>('select[aria-label="本场评分模板"]')!; select.value = "senior-math"; select.dispatchEvent(new Event("change", { bubbles: true })); }); }
  async function apply() { await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === "核对并补齐评分方式")!.click()); await act(async () => api.confirm.mock.calls[api.confirm.mock.calls.length - 1][0].onOk()); }

  it("继承真实高中年级，二次确认后保存缺失策略，保持双评和合法证据交集", async () => {
    await render(); expect(host.querySelector<HTMLSelectElement>('select[aria-label="本场评分学段"]')!.value).toBe("senior");
    await selectProfile(); expect(api.put).not.toHaveBeenCalled(); await apply();
    expect(api.put).toHaveBeenCalledTimes(2); expect(api.put).toHaveBeenCalledWith("exam", "q1", expect.objectContaining({ subject_profile_id: "senior-math", allowed_evidence_types: ["text_span"], expected_revision: 0, scoring_policy: expect.objectContaining({ mode: "DUAL_HUMAN" }) }));
    expect(host.textContent).toContain("已核实 2 / 2 题");
  });
  it("部分失败保留成功结果，重试只补未保存题目", async () => {
    const normalPut = api.put.getMockImplementation()!;
    api.put.mockImplementation(async (...args) => { if (args[1] === "q2") throw new Error("保存暂时失败"); return normalPut(...args); });
    await render(); await selectProfile(); await apply(); expect(api.error).toHaveBeenCalledWith(expect.stringContaining("已保存 1 道题"));
    expect(host.textContent).toContain("已核实 1 / 2 题"); api.put.mockImplementation(normalPut); api.put.mockClear(); await apply();
    expect(api.put).toHaveBeenCalledTimes(1); expect(api.put.mock.calls[0][1]).toBe("q2");
  });
  it("教师只读界面解释下一步且不暴露保存权限", async () => {
    await render(false); expect(host.textContent).toContain("联系本场考试负责人或学校管理员"); expect(host.textContent).not.toContain("核对并补齐评分方式"); expect(api.put).not.toHaveBeenCalled();
  });
});
