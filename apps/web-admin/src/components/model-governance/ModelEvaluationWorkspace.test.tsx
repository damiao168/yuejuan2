// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TableColumnsType } from "antd";
import type { EvaluationCandidate, EvaluationRun } from "../../api/modelGovernance";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { ModelEvaluationWorkspace } from "./ModelEvaluationWorkspace";

const mocks = vi.hoisted(() => ({
  form: { resetFields: vi.fn(), setFieldsValue: vi.fn(), validateFields: vi.fn() },
  addCandidate: vi.fn(), completeRun: vi.fn(), invalidateRun: vi.fn(), createRun: vi.fn()
}));
vi.mock("../../api/modelGovernance", () => ({
  addModelEvaluationCandidate: mocks.addCandidate, completeModelEvaluationRun: mocks.completeRun,
  invalidateModelEvaluationRun: mocks.invalidateRun, createModelEvaluationRun: mocks.createRun
}));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn() } }) },
    Button: ({ children, onClick, disabled }: { children?: ReactNode; onClick?: () => void; disabled?: boolean }) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Drawer: ({ open, title, children, extra }: { open: boolean; title: string; children?: ReactNode; extra?: ReactNode }) => open ? <aside aria-label={title}><h3>{title}</h3>{extra}{children}</aside> : null,
    Alert: ({ type, message, description }: { type: string; message: ReactNode; description?: ReactNode }) => <div data-alert-type={type}><span>{message}</span><p>{description}</p></div>,
    Form: Object.assign(passthrough, { Item: passthrough, useForm: () => [mocks.form] }),
    Input: Object.assign(() => null, { TextArea: () => null }), InputNumber: () => null,
    Select: () => null, Modal: ({ open, title, children, onOk, okText }: { open: boolean; title: string; children?: ReactNode; onOk: () => void; okText: string }) => open ? <section role="dialog" aria-label={title}>{children}<button onClick={onOk}>{okText}</button></section> : null, Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Space: passthrough, Tag: passthrough, Tooltip: passthrough
  };
});
vi.mock("../ResponsiveTable", () => ({
  ResponsiveTable: ({ columns, dataSource }: { columns: TableColumnsType<EvaluationCandidate>; dataSource: EvaluationCandidate[] }) => <div>{dataSource.map((candidate, index) => (
    <div key={candidate.id} data-testid={`candidate-${candidate.id}`}>
      {columns.filter((column) => column.title === "严重错误率").map((column) => "render" in column && column.render
        ? <div key={String(column.title)}>{column.render(candidate.metrics.serious_error_rate, candidate, index) as ReactNode}</div>
        : null)}
    </div>
  ))}</div>
}));

function run(overrides: Partial<EvaluationRun> = {}): EvaluationRun {
  return {
    id: "run", run_key: "batch", display_name: "数学评测", dataset_reference: "dataset", dataset_sha256: "hash",
    evidence_class: "protocol_fixture", subject: "数学", grade: "高一", question_type: "short_answer", modality: "text",
    sample_count: 2, repeat_count: 2, status: "draft", candidates: [], created_at: "", ...overrides
  };
}
function candidate(id: string, seriousErrorRate: number): EvaluationCandidate {
  return {
    id, run_id: "run", deployment_id: id, model_config_id: id, model_name: id, provider_key: "cloud", deployment_key: id,
    model_version: "v1", prompt_version: "v1", rubric_version: "v1", evaluated_samples: 2, teacher_reviewed_samples: 2,
    teacher_accepted_samples: 2, serious_error_samples: 1, evidence_valid_samples: 2, repeat_comparisons: 2,
    stable_repeat_samples: 2, p95_latency_ms: 10, total_cost_micros: 0, created_at: "",
    metrics: { teacher_acceptance_rate: 1, serious_error_rate: seriousErrorRate, evidence_validity_rate: 1, stability_rate: 1, average_cost_micros: 0 }
  };
}
const availableConfig: ManagedModelAPIConfig = {
  id: "available", tenant_id: "school", provider_key: "cloud", display_name: "可用模型", adapter_type: "openai_compatible",
  base_url: "https://example.test/v1", model_name: "model", model_version: "v1", region: "global", credential_configured: true,
  status: "active", is_default: false, last_test_status: "success", last_capability_status: "success",
  last_capability_probe_version: "structured-json-v3", config_source: "auto", created_at: "", updated_at: ""
};

beforeEach(() => { vi.clearAllMocks(); mocks.form.validateFields.mockResolvedValue({ reason: "人工核对证据" }); });
afterEach(cleanup);
describe("模型评测展示", () => {
  it.each<{ status: EvaluationRun["status"]; evidence: EvaluationRun["evidence_class"]; title: string; type: string }>([
    { status: "invalidated", evidence: "authorized_frozen_set", title: "这组评测证据已失效", type: "error" },
    { status: "draft", evidence: "authorized_frozen_set", title: "授权冻结集证据", type: "success" },
    { status: "draft", evidence: "protocol_fixture", title: "协议样例证据", type: "info" }
  ])("状态=$status、证据=$evidence 保留对应提示优先级", async ({ status, evidence, title, type }) => {
    render(<ModelEvaluationWorkspace runs={[run({ status, evidence_class: evidence })]} tenantID="school" configs={[]} loading={false} canManage={false} onRefresh={vi.fn()} />);
    const notice = await screen.findByText(title);
    expect(notice.closest("[data-alert-type]")?.getAttribute("data-alert-type")).toBe(type);
    const otherTitles = ["这组评测证据已失效", "授权冻结集证据", "协议样例证据"].filter((value) => value !== title);
    for (const otherTitle of otherTitles) expect(screen.queryByText(otherTitle)).toBeNull();
  });

  it("同批最小错误率仍大于零时标记相对最优，其他非零候选标记风险", async () => {
    render(<ModelEvaluationWorkspace runs={[run({ candidates: [candidate("best", 0.1), candidate("risk", 0.2)] })]} tenantID="school" configs={[]} loading={false} canManage={false} onRefresh={vi.fn()} />);
    await screen.findByRole("heading", { name: "数学评测" });
    expect(within(screen.getByTestId("candidate-best")).getByText("10.0%").className).toBe("model-evaluation-best");
    expect(within(screen.getByTestId("candidate-risk")).getByText("20.0%").className).toBe("model-evaluation-risk");
  });

  it("草稿候选抽屉显示原批次标题，冻结或失效后保留原操作展示边界", async () => {
    const props = { tenantID: "school", configs: [availableConfig], loading: false, canManage: true, onRefresh: vi.fn() };
    const selected = run({ candidates: [candidate("a", 0), candidate("b", 0)] });
    const view = render(<ModelEvaluationWorkspace {...props} runs={[selected]} />);
    await screen.findByRole("heading", { name: "数学评测" });
    const freeze = screen.getByRole("button", { name: "冻结完成" });
    expect(freeze.hasAttribute("disabled")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "添加候选" }));
    expect(screen.getByRole("heading", { name: "添加候选结果 · 数学评测" })).toBeTruthy();
    view.rerender(<ModelEvaluationWorkspace {...props} runs={[{ ...selected, status: "completed" }]} />);
    expect(screen.queryByRole("button", { name: "添加候选" })).toBeNull();
    expect(screen.queryByRole("button", { name: "冻结完成" })).toBeNull();
    expect(screen.getByRole("button", { name: "标记失效" })).toBeTruthy();
    view.rerender(<ModelEvaluationWorkspace {...props} runs={[{ ...selected, status: "invalidated" }]} />);
    expect(screen.queryByRole("button", { name: "标记失效" })).toBeNull();
  });
  it.each([
    { status: "draft", canManage: false }, { status: "completed", canManage: false }, { status: "invalidated", canManage: false },
    { status: "draft", canManage: true }, { status: "completed", canManage: true }, { status: "invalidated", canManage: true },
  ] as const)("权限/状态矩阵 canManage=$canManage status=$status 保留生命周期边界", async ({ status, canManage }) => {
    render(<ModelEvaluationWorkspace runs={[run({ status, candidates: [candidate("a", 0), candidate("b", 0)] })]} tenantID="school" configs={[availableConfig]} loading={false} canManage={canManage} onRefresh={vi.fn()} />);
    await screen.findByRole("heading", { name: "数学评测" });
    expect(Boolean(screen.queryByRole("button", { name: "登记已有评测报告" }))).toBe(canManage);
    expect(Boolean(screen.queryByRole("button", { name: "添加候选" }))).toBe(canManage && status === "draft");
    expect(Boolean(screen.queryByRole("button", { name: "冻结完成" }))).toBe(canManage && status === "draft");
    expect(Boolean(screen.queryByRole("button", { name: "标记失效" }))).toBe(canManage && status !== "invalidated");
    expect(mocks.addCandidate).not.toHaveBeenCalled(); expect(mocks.completeRun).not.toHaveBeenCalled(); expect(mocks.invalidateRun).not.toHaveBeenCalled();
  });

  it.each(["permission", "completed", "invalidated"] as const)("候选抽屉打开后 $0 更新，旧保存入口不能写入", async (change) => {
    const selected = run();
    const props = { tenantID: "school", configs: [availableConfig], loading: false, canManage: true, onRefresh: vi.fn() };
    const view = render(<ModelEvaluationWorkspace {...props} runs={[selected]} />);
    await screen.findByRole("button", { name: "添加候选" });
    fireEvent.click(screen.getByRole("button", { name: "添加候选" }));
    view.rerender(<ModelEvaluationWorkspace {...props} canManage={change !== "permission"} runs={[{ ...selected, status: change === "permission" ? "draft" : change }]} />);
    fireEvent.click(screen.getByRole("button", { name: "保存候选" }));
    expect(mocks.form.validateFields).not.toHaveBeenCalled();
    expect(mocks.addCandidate).not.toHaveBeenCalled();
  });

  it("冻结确认经过异步表单校验后再次核对最新状态", async () => {
    let finishValidation!: (values: { reason: string }) => void;
    mocks.form.validateFields.mockImplementation(() => new Promise((resolve) => { finishValidation = resolve; }));
    const selected = run({ candidates: [candidate("a", 0), candidate("b", 0)] });
    const props = { tenantID: "school", configs: [availableConfig], loading: false, canManage: true, onRefresh: vi.fn() };
    const view = render(<ModelEvaluationWorkspace {...props} runs={[selected]} />);
    await screen.findByRole("button", { name: "冻结完成" });
    fireEvent.click(screen.getByRole("button", { name: "冻结完成" }));
    fireEvent.click(screen.getByRole("button", { name: "确认冻结" }));
    await waitFor(() => expect(mocks.form.validateFields).toHaveBeenCalledOnce());
    view.rerender(<ModelEvaluationWorkspace {...props} runs={[{ ...selected, status: "completed" }]} />);
    finishValidation({ reason: "批次由其他负责人冻结" });
    await waitFor(() => expect(screen.queryByRole("button", { name: "冻结完成" })).toBeNull());
    expect(mocks.completeRun).not.toHaveBeenCalled();
  });

  it("候选输入属于打开时的学校，切换学校后旧抽屉不能借新范围保存", async () => {
    const props = { configs: [availableConfig], loading: false, canManage: true, onRefresh: vi.fn(), runs: [run()] };
    const view = render(<ModelEvaluationWorkspace {...props} tenantID="school-a" />);
    await screen.findByRole("button", { name: "添加候选" });
    fireEvent.click(screen.getByRole("button", { name: "添加候选" }));
    view.rerender(<ModelEvaluationWorkspace {...props} tenantID="school-b" />);
    fireEvent.click(screen.getByRole("button", { name: "保存候选" }));
    expect(mocks.form.validateFields).not.toHaveBeenCalled();
    expect(mocks.addCandidate).not.toHaveBeenCalled();
  });

});
