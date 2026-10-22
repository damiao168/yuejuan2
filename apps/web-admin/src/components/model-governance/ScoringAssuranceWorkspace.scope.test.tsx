// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { AIHumanDisagreement, GradingEvaluationRun } from "../../api/scoringAssurance";
import { ScoringAssuranceWorkspace } from "./ScoringAssuranceWorkspace";

const mocks = vi.hoisted(() => ({
  form: { validateFields: vi.fn(), setFieldsValue: vi.fn(), resetFields: vi.fn() },
  create: vi.fn(), classify: vi.fn(), route: vi.fn(), disagreements: vi.fn(), success: vi.fn(), error: vi.fn()
}));
const appContext = vi.hoisted(() => ({ message: { success: mocks.success, error: mocks.error } }));
vi.mock("../../api/scoringAssurance", async () => ({
  ...await vi.importActual<typeof import("../../api/scoringAssurance")>("../../api/scoringAssurance"),
  setScoringAssuranceSchool: vi.fn(), listGradingEvaluations: async () => ({ evaluation_runs: [] }),
  listModelCalibrations: async () => ({ calibrations: [] }), listAIHumanDisagreements: mocks.disagreements,
  createGradingEvaluation: mocks.create, classifyAIHumanDisagreement: mocks.classify, routeAIHumanDisagreement: mocks.route
}));
vi.mock("./ScoringAssuranceViews", () => ({
  CalibrationDetail: () => null, EvaluationDetail: () => null, EligibilityPolicyPanel: () => null,
  DisagreementQueue: ({ disagreements, onClassify, onRoute }: { disagreements: AIHumanDisagreement[]; onClassify: (item: AIHumanDisagreement) => void; onRoute: (item: AIHumanDisagreement) => void }) => <div>{disagreements.map((item) => <div key={item.id}><button onClick={() => onClassify(item)}>分类-{item.id}</button><button onClick={() => onRoute(item)}>关联-{item.id}</button></div>)}</div>
}));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    App: { useApp: () => appContext },
    Button: ({ children, disabled, onClick }: { children?: ReactNode; disabled?: boolean; onClick?: () => void }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>,
    Drawer: ({ title, children, extra, open, onClose }: { title: string; children?: ReactNode; extra?: ReactNode; open: boolean; onClose: () => void }) => open ? <section role="dialog" aria-label={title}><button onClick={onClose}>关闭当前草稿</button>{children}{extra}</section> : null,
    Form: Object.assign(Box, { Item: Box, useForm: () => [mocks.form] }),
    Card: ({ children, extra }: { children?: ReactNode; extra?: ReactNode }) => <div>{extra}{children}</div>,
    Space: Object.assign(Box, { Compact: Box }), Divider: Box, Alert: () => null,
    Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Input: Object.assign(() => null, { TextArea: () => null }), InputNumber: () => null, Select: () => null,
    Table: () => null, Tag: Box, Typography: { Text: Box }, Descriptions: () => null
  };
});
function disagreement(id: string): AIHumanDisagreement {
  return { id, exam_id: "exam", question_id: "question", submission_id: "submission", answer_segment_id: "answer", ai_candidate_id: "candidate", human_grade_id: "grade", ai_candidate_score: 0, human_score: 1, max_score: 10, delta: 1, absolute_delta: 1, difference_type: "score", severity: "severe", risk_tier: "R2", trigger_rules: [], evidence_summary: { ai_evidence_count: 0, ai_matched_criterion_count: 0, human_criterion_count: 1 }, status: "needs_review", revision: 7, created_at: "", updated_at: "" };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((finish) => { resolve = finish; });
  return { promise, resolve };
}
const props = () => ({ tenantID: "school", canReadEligibility: false, canManageEligibility: false, canManageEvaluations: true, canReadDisagreements: true, canManageDisagreements: true, onDirtyChange: vi.fn(), onSavingChange: vi.fn() });
beforeEach(() => {
  vi.clearAllMocks(); mocks.form.validateFields.mockResolvedValue({ key: "new", taxonomy: "ai_scoring_error", review_task_id: "review-task" });
  mocks.disagreements.mockResolvedValue({ disagreements: [disagreement("A"), disagreement("B")] });
});
afterEach(cleanup);

// 运行真实工作台的草稿与写回执，只替代网络和展示层；不声称验证后端事务或真实模型。
it("登记评分报告关闭并重开后，旧成功回执不能关闭或重置新草稿", async () => {
  const request = deferred<{ evaluation_run: GradingEvaluationRun }>(); mocks.create.mockReturnValue(request.promise);
  const input = props(); render(<ScoringAssuranceWorkspace {...input} />);
  fireEvent.click(screen.getByRole("button", { name: "登记评分验证报告" }));
  fireEvent.click(screen.getByRole("button", { name: "登记报告" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "关闭当前草稿" }));
  fireEvent.click(screen.getByRole("button", { name: "登记评分验证报告" }));
  const resets = mocks.form.resetFields.mock.calls.length;
  await act(async () => { request.resolve({ evaluation_run: {
    id: "created-a", key: "created-a", display_name: "新评分报告", model_reference: "model-v1",
    prompt_version: "prompt-v1", rubric_version: "rubric-v1", dataset_reference: "dataset",
    dataset_sha256: "a".repeat(64), status: "draft", observation_count: 0, created_at: ""
  } }); await request.promise; });
  expect(screen.getByRole("dialog", { name: "登记已有评分验证报告" })).toBeTruthy();
  expect(mocks.form.resetFields).toHaveBeenCalledTimes(resets);
  expect(input.onDirtyChange).toHaveBeenLastCalledWith(true);
});

it.each([
  { open: "分类", save: "保存分类", title: "人工分类人机分歧", mock: mocks.classify },
  { open: "关联", save: "关联任务", title: "关联人工阅卷任务", mock: mocks.route }
])("$open A保存期间打开B草稿，A旧回执不能关闭B", async ({ open, save, title, mock }) => {
  const request = deferred<void>(); mock.mockReturnValue(request.promise);
  const input = props(); render(<ScoringAssuranceWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: `${open}-A` }));
  fireEvent.click(screen.getByRole("button", { name: save }));
  await waitFor(() => expect(mock).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: `${open}-B` }));
  await act(async () => { request.resolve(); await request.promise; });
  expect(screen.getByRole("dialog", { name: title })).toBeTruthy();
  expect(input.onDirtyChange).toHaveBeenLastCalledWith(true);
});
