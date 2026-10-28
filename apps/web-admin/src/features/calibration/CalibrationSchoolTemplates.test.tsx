// @vitest-environment jsdom
import type { ChangeEvent, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi, type Mock } from "vitest";
import { CalibrationSchoolTemplates } from "./CalibrationSchoolTemplates";
import type { CalibrationPolicy, CalibrationSchoolTemplate, CalibrationSchoolTemplateDirectory, PutCalibrationPolicyRequest } from "../../api/calibration";

const api = vi.hoisted(() => ({ list: vi.fn(), approve: vi.fn(), withdraw: vi.fn(), session: { generation: 1, scope: "school:manager" } }));
vi.mock("../../api/calibration", () => ({ listCalibrationSchoolTemplates: api.list, approveCalibrationSchoolTemplate: api.approve, withdrawCalibrationSchoolTemplate: api.withdraw }));
vi.mock("../../api/businessCommand", () => ({ getBusinessCommandSession: () => api.session }));
vi.mock("../../api/client", () => ({ apiClient: {}, getUserErrorMessage: (cause: Error) => cause.message }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Alert: ({ message }: { message: string }) => <p role="alert">{message}</p>,
  Input: ({ value, onChange, disabled, ...rest }: { value: string; onChange: (event: ChangeEvent<HTMLInputElement>) => void; disabled?: boolean; "aria-label": string }) => <input aria-label={rest["aria-label"]} value={value} onChange={onChange} disabled={disabled} />,
  Select: ({ options, value, onChange, disabled }: { options: { value: string; label: string }[]; value?: string; onChange: (id: string) => void; disabled?: boolean }) => <select aria-label="学校已批准试评标准" value={value ?? ""} onChange={event => onChange(event.target.value)} disabled={disabled}><option value="" />{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));

const sourcePolicy: CalibrationPolicy = { id: "source-policy", exam_id: "source-exam", question_id: "source-question", archetype_code: "structured_steps", max_score: 8,
  minimum_samples: 5, maximum_mae: 0, minimum_exact_agreement: 1, minimum_within_one_agreement: 1,
  minimum_criterion_agreement: 1, maximum_severe_rate: 0, severe_error_threshold: 2, qualification_validity_days: 7,
  revision: 9, created_at: "", updated_at: "" };
const template: CalibrationSchoolTemplate = { id: "template", school_id: "school", subject: "math", name: "数学分步题标准", source_exam_id: "source-exam", source_question_id: "source-question", source_policy_revision: 9,
  policy: sourcePolicy, status: "approved", revision: 1, approved_by: "admin", approved_at: "2026-10-28T00:00:00Z" };
const directory: CalibrationSchoolTemplateDirectory = { templates: [template], capabilities: { can_approve: true, can_withdraw: true }, question: { school_id: "school", subject: "math", archetype_code: "structured_steps", max_score: 8 } };
let values: Partial<PutCalibrationPolicyRequest>;
type QuestionFacts = { archetype_code?: string; max_score?: number };
let apply: Mock<(values: PutCalibrationPolicyRequest) => void>;
let known: Mock<(facts: QuestionFacts) => void>;
let working: Mock<(working: boolean) => void>;
beforeEach(() => {
  vi.resetAllMocks(); api.session = { generation: 1, scope: "school:manager" };
  api.list.mockResolvedValue(directory); api.approve.mockResolvedValue({ template }); api.withdraw.mockResolvedValue({ template: { ...template, status: "withdrawn", revision: 2 } });
  values = { archetype_code: "structured_steps", max_score: 8, expected_revision: 4, minimum_samples: 3 };
  apply = vi.fn<(values: PutCalibrationPolicyRequest) => void>(); working = vi.fn<(working: boolean) => void>(); known = vi.fn<(facts: QuestionFacts) => void>(facts => { values = { ...values, ...facts }; });
});
afterEach(cleanup);
const view = (overrides: Partial<Parameters<typeof CalibrationSchoolTemplates>[0]> = {}) => <CalibrationSchoolTemplates examId="exam" questionId="target" disabled={false} policy={sourcePolicy} hasUnsavedChanges={false}
  readCurrent={() => values} onKnownQuestion={known} onApply={apply} onWorkingChange={working} {...overrides} />;
async function ready() { await waitFor(() => expect((screen.getByRole("button", { name: "刷新学校标准" }) as HTMLButtonElement).disabled).toBe(false)); }
function choose() { fireEvent.change(screen.getByLabelText("学校已批准试评标准"), { target: { value: template.id } }); }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }

it("只带入最新仍批准的同题型同满分标准，保留目标修订和来源版本，不自动保存", async () => {
  render(view()); await ready(); choose(); fireEvent.click(screen.getByRole("button", { name: "带入学校标准" }));
  await screen.findByText("已带入“数学分步题标准”第 1 版，请保存本题。");
  expect(api.list).toHaveBeenCalledTimes(2);
  expect(apply).toHaveBeenCalledWith(expect.objectContaining({ expected_revision: 4, minimum_samples: 5, maximum_mae: 0, school_template_id: "template", school_template_revision: 1 }));
  expect(apply.mock.calls[0][0]).not.toHaveProperty("id"); expect(apply.mock.calls[0][0]).not.toHaveProperty("question_id");
  expect(api.approve).not.toHaveBeenCalled(); expect(working).toHaveBeenLastCalledWith(false);
});

it.each(["撤回", "修订变化", "题目分值改变"])("应用前发现%s时不覆盖本题设置", async scenario => {
  render(view()); await ready(); choose();
  if (scenario === "撤回") api.list.mockResolvedValueOnce({ ...directory, templates: [] });
  if (scenario === "修订变化") api.list.mockResolvedValueOnce({ ...directory, templates: [{ ...template, revision: 2 }] });
  if (scenario === "题目分值改变") values = { ...values, max_score: 10 };
  fireEvent.click(screen.getByRole("button", { name: "带入学校标准" }));
  await screen.findByRole("alert"); expect(apply).not.toHaveBeenCalled();
});

it("重验模板期间本题有新修改时保留输入，不能用迟到结果覆盖", async () => {
  const pending = deferred<CalibrationSchoolTemplateDirectory>();
  render(view()); await ready(); choose(); api.list.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "带入学校标准" }));
  values = { ...values, minimum_samples: 17 }; pending.resolve(directory);
  await screen.findByText("读取期间本题设置已有修改，已保留输入，请重新带入");
  expect(apply).not.toHaveBeenCalled(); expect(values.minimum_samples).toBe(17);
});

it("权威目录只补尚未知的题目事实，不覆盖等待期间输入的新类型或分值", async () => {
  const pending = deferred<CalibrationSchoolTemplateDirectory>(); api.list.mockReturnValueOnce(pending.promise); values = { expected_revision: 0 };
  render(view()); values = { ...values, max_score: 12 }; pending.resolve(directory); await ready();
  expect(known).toHaveBeenCalledWith({ archetype_code: "structured_steps" }); expect(values.max_score).toBe(12); expect(values.expected_revision).toBe(0);
});

it("管理能力只认服务端，不以能改本题推断能批准学校模板", async () => {
  api.list.mockResolvedValue({ ...directory, capabilities: { can_approve: false, can_withdraw: false } });
  render(view()); await ready();
  expect(screen.queryByText("管理学校标准")).toBeNull(); expect(screen.queryByRole("button", { name: "批准已保存标准" })).toBeNull();
  expect(api.approve).not.toHaveBeenCalled();
});

it("未保存的本题修改不能批准，批准只发送已保存策略修订且不重复并发", async () => {
  const rendered = render(view({ hasUnsavedChanges: true })); await ready();
  fireEvent.change(screen.getByLabelText("学校试评模板名称"), { target: { value: "本校数学标准" } });
  expect((screen.getByRole("button", { name: "批准已保存标准" }) as HTMLButtonElement).disabled).toBe(true);
  rendered.rerender(view()); const pending = deferred<{ template: CalibrationSchoolTemplate }>(); api.approve.mockReturnValueOnce(pending.promise);
  const button = screen.getByRole("button", { name: "批准已保存标准" }); fireEvent.click(button); fireEvent.click(button);
  expect(api.approve).toHaveBeenCalledExactlyOnceWith("exam", "target", { name: "本校数学标准", source_policy_revision: 9 });
  pending.resolve({ template }); await screen.findByText("本题已保存标准已批准为学校模板。");
});

it("撤回使用所选模板的当前修订和明确原因，成功后移除可用模板", async () => {
  render(view()); await ready(); choose();
  fireEvent.change(screen.getByLabelText("撤回学校标准原因"), { target: { value: "统计要求调整" } });
  api.list.mockResolvedValueOnce({ ...directory, templates: [] }); fireEvent.click(screen.getByRole("button", { name: "撤回所选模板" }));
  await screen.findByText("学校模板已撤回，不能继续用于新的题目设置。");
  expect(api.withdraw).toHaveBeenCalledWith("exam", "target", "template", { expected_revision: 1, reason: "统计要求调整" }); expect(apply).not.toHaveBeenCalled();
});

it("切题和会话变化废弃旧目录，旧会话不能继续批准或应用", async () => {
  const pending = deferred<CalibrationSchoolTemplateDirectory>(); api.list.mockReturnValueOnce(pending.promise); values = { expected_revision: 0 };
  const rendered = render(view()); rendered.rerender(view({ questionId: "other" })); await ready(); pending.resolve({ ...directory, question: { ...directory.question, max_score: 99 } });
  await waitFor(() => expect(known).toHaveBeenCalledTimes(1)); expect(values.max_score).toBe(8);
  api.session = { generation: 2, scope: "school:other-user" }; fireEvent(window, new Event("business-command-scope-changed"));
  await screen.findByText("登录状态已变化，请重新打开本题后处理学校标准");
  expect((screen.getByRole("button", { name: "带入学校标准" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByText("管理学校标准")).toBeNull(); expect(api.approve).not.toHaveBeenCalled();
});

it("读取失败可重试，不能把失败误认为没有学校标准", async () => {
  api.list.mockRejectedValueOnce(new Error("目录暂不可用")); render(view()); await screen.findByText("目录暂不可用");
  fireEvent.click(screen.getByRole("button", { name: "刷新学校标准" })); await ready();
  expect(screen.getByText("数学分步题标准 · 第 1 版")).toBeTruthy();
});
