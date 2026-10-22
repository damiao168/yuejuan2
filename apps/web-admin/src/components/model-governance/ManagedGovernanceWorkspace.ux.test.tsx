// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TenantModelPolicy } from "../../api/modelGovernance";
import { ManagedGovernanceWorkspace } from "./ManagedGovernanceWorkspace";

const mocks = vi.hoisted(() => ({ policy: vi.fn(), update: vi.fn(), form: { values: {} as Record<string, unknown>, setFieldsValue: vi.fn(), validateFields: vi.fn() } }));
vi.mock("../../api/modelGovernance", () => ({ getModelPolicy: mocks.policy, updateModelPolicy: mocks.update, listModelEvaluationRuns: async () => ({ evaluation_runs: [] }), listModelApprovals: async () => ({ model_approvals: [] }) }));
vi.mock("./ModelEvaluationWorkspace", () => ({ ModelEvaluationWorkspace: ({ onRefresh }: { onRefresh: () => Promise<void> }) => <button type="button" onClick={() => void onRefresh()}>刷新证据列表</button> }));
vi.mock("./ScoringAssuranceWorkspace", () => ({ ScoringAssuranceWorkspace: () => null }));
vi.mock("./ModelApprovalWorkspace", () => ({ ModelApprovalWorkspace: () => null }));
vi.mock("./PromptVersionWorkspace", () => ({ PromptVersionWorkspace: () => null }));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn() } }) },
    Empty: () => null, Alert: () => null, Space: Box, Tag: Box,
    Form: Object.assign(({ children, onFinish, onValuesChange }: { children?: ReactNode; onFinish: () => void; onValuesChange: () => void }) => <form aria-label="使用策略" onChange={onValuesChange} onSubmit={(event) => { event.preventDefault(); onFinish(); }}>{children}<input aria-label="编辑策略" /></form>, { Item: Box, useForm: () => [mocks.form] }),
    Button: ({ children, onClick, disabled, htmlType }: { children?: ReactNode; onClick?: () => void; disabled?: boolean; htmlType?: "submit" }) => <button type={htmlType ?? "button"} disabled={disabled} onClick={onClick}>{children}</button>,
    Input: Object.assign(() => null, { TextArea: () => null }), InputNumber: () => null, Select: () => null, Switch: () => null,
    Segmented: ({ value, onChange, options }: { value: string; onChange: (value: string) => void; options: { label: string; value: string }[] }) => <select aria-label="治理视图" value={value} onChange={(event) => onChange(event.target.value)}>{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>,
  };
});

function policy(version: number): TenantModelPolicy {
  return { id: "policy", tenant_id: "school-a", policy_key: "default", display_name: "学校使用策略", mode: "local_only", external_enabled: false, text_export_enabled: false, image_export_enabled: false, allowed_deployments: [], allowed_model_config_ids: [], max_cost_micros_per_question: 1000000, max_cost_micros_per_exam: 100000000, fallback_mode: "manual_only", status: "active", version, updated_at: "" };
}
beforeEach(() => {
  vi.clearAllMocks(); window.history.replaceState({ preserved: true }, "", "/admin/platform/model-config?tenant_id=school-a&tab=governance&view=policy");
  mocks.form.values = {};
  mocks.form.setFieldsValue.mockImplementation((values: Record<string, unknown>) => Object.assign(mocks.form.values, values));
  mocks.form.validateFields.mockImplementation(async () => ({ ...mocks.form.values }));
  mocks.policy.mockResolvedValue({ policy: policy(7) }); mocks.update.mockResolvedValue({});
});
afterEach(cleanup);

describe("学校使用策略实际提交", () => {
  it("切换治理视图保留学校、范围参数和浏览历史状态，地址不含 #", async () => {
    window.history.replaceState({ preserved: true }, "", "/admin/platform/model-config?tenant_id=school-a&tab=governance&view=policy&subject=english");
    render(<ManagedGovernanceWorkspace tenantID="school-a" configs={[]} />);
    await screen.findByText("学校使用策略");
    fireEvent.change(screen.getByLabelText("治理视图"), { target: { value: "prompts" } });
    expect(window.location.pathname).toBe("/admin/platform/model-config");
    expect(window.location.hash).toBe("");
    expect(new URLSearchParams(window.location.search).get("tenant_id")).toBe("school-a");
    expect(new URLSearchParams(window.location.search).get("subject")).toBe("english");
    expect(new URLSearchParams(window.location.search).get("view")).toBe("prompts");
    expect(window.history.state).toEqual({ preserved: true });
  });

  it("美元输入精确保存；另一报告刷新不覆盖正在编辑的原策略版本，关闭外部模型提交空名单", async () => {
    const onSaving = vi.fn();
    render(<ManagedGovernanceWorkspace tenantID="school-a" configs={[]} onSavingChange={onSaving} />);
    await screen.findByText("学校使用策略");
    expect(mocks.form.values.max_cost_micros_per_question).toBe("1");
    Object.assign(mocks.form.values, { max_cost_micros_per_question: "0.123456", max_cost_micros_per_exam: "100", external_enabled: false, allowed_model_config_ids: ["previous-model"], reason: "预算调整" });
    fireEvent.change(screen.getByLabelText("编辑策略"), { target: { value: "changed" } });
    mocks.policy.mockResolvedValue({ policy: policy(8) });
    fireEvent.click(screen.getByRole("button", { name: "刷新证据列表", hidden: true }));
    await waitFor(() => expect(mocks.policy).toHaveBeenCalledTimes(2));
    expect(mocks.form.values.max_cost_micros_per_question).toBe("0.123456");
    fireEvent.submit(screen.getByRole("form", { name: "使用策略" }));
    await waitFor(() => expect(mocks.update).toHaveBeenCalledWith("school-a", expect.objectContaining({ max_cost_micros_per_question: 123456, max_cost_micros_per_exam: 100000000, expected_version: 7, allowed_model_config_ids: [] })));
    expect(onSaving).toHaveBeenCalledWith(true);
    await waitFor(() => expect(onSaving).toHaveBeenLastCalledWith(false));
  });
});
