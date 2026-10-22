// @vitest-environment jsdom
import type { MouseEvent, ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { EvaluationRun } from "../../api/modelGovernance";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { ModelEvaluationWorkspace } from "./ModelEvaluationWorkspace";

const mocks = vi.hoisted(() => ({
  form: { resetFields: vi.fn(), setFieldsValue: vi.fn(), validateFields: vi.fn() },
  addCandidate: vi.fn(), completeRun: vi.fn(), invalidateRun: vi.fn(), createRun: vi.fn(),
  success: vi.fn(), error: vi.fn()
}));
vi.mock("../../api/modelGovernance", () => ({
  addModelEvaluationCandidate: mocks.addCandidate, completeModelEvaluationRun: mocks.completeRun,
  invalidateModelEvaluationRun: mocks.invalidateRun, createModelEvaluationRun: mocks.createRun
}));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  const field = ({ children }: { children?: ReactNode | ((form: { getFieldValue: () => string }) => ReactNode) }) =>
    <>{typeof children === "function" ? children({ getFieldValue: () => "protocol_fixture" }) : children}</>;
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error } }) },
    Button: ({ children, onClick, disabled }: { children?: ReactNode; onClick?: (event: MouseEvent<HTMLButtonElement>) => void; disabled?: boolean }) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Drawer: ({ open, title, children, extra, onClose }: { open: boolean; title: string; children?: ReactNode; extra?: ReactNode; onClose: () => void }) => open ? <aside aria-label={title}><h3>{title}</h3>{extra}<button onClick={onClose}>关闭当前抽屉</button>{children}</aside> : null,
    Alert: passthrough, Form: Object.assign(passthrough, { Item: field, useForm: () => [mocks.form] }),
    Input: Object.assign(() => null, { TextArea: () => null }), InputNumber: () => null, Select: () => null,
    Modal: ({ open, title, children, onOk, okText, onCancel }: { open: boolean; title: string; children?: ReactNode; onOk: () => void; okText: string; onCancel: () => void }) => open ? <section role="dialog" aria-label={title}>{children}<button onClick={onOk}>{okText}</button><button onClick={onCancel}>关闭当前确认</button></section> : null,
    Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Space: passthrough, Tag: passthrough, Tooltip: passthrough
  };
});
vi.mock("../ResponsiveTable", () => ({
  ResponsiveTable: ({ dataSource, onRow }: { dataSource: Array<{ id: string }>; onRow?: (row: { id: string }) => { onClick: () => void } }) => <div>{dataSource.map((row) => <button key={row.id} onClick={() => onRow?.(row).onClick()}>{row.id}</button>)}</div>
}));

function run(id: string): EvaluationRun {
  return {
    id, run_key: id, display_name: id, dataset_reference: "dataset", dataset_sha256: "hash",
    evidence_class: "protocol_fixture", subject: "数学", grade: "高一", question_type: "short_answer", modality: "text",
    sample_count: 2, repeat_count: 2, status: "draft", candidates: [], created_at: ""
  };
}
const config: ManagedModelAPIConfig = {
  id: "available", tenant_id: "school", provider_key: "cloud", display_name: "可用模型", adapter_type: "openai_compatible",
  base_url: "https://example.test/v1", model_name: "model", model_version: "v1", region: "global", credential_configured: true,
  status: "active", is_default: false, last_test_status: "success", last_capability_status: "success",
  last_capability_probe_version: "structured-json-v4", config_source: "auto", created_at: "", updated_at: ""
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((finish) => { resolve = finish; });
  return { promise, resolve };
}
const props = () => ({ tenantID: "school", configs: [config], runs: [run("run-a"), run("run-b")], loading: false, canManage: true, onRefresh: vi.fn().mockResolvedValue(undefined), onDirtyChange: vi.fn(), onSavingChange: vi.fn() });
beforeEach(() => {
  vi.clearAllMocks();
  mocks.form.validateFields.mockResolvedValue({ reason: "人工核对证据", total_cost_micros: 0, model_config_id: "available" });
});
afterEach(cleanup);

// 只延迟业务请求，组件、批次选择和抽屉状态均运行真实实现；不把旧回执当作新草稿的完成信号。
it("保存A候选期间打开B候选，A迟到回执不能关闭B或清除其未保存状态", async () => {
  const request = deferred<void>(); mocks.addCandidate.mockReturnValue(request.promise);
  const input = props(); render(<ModelEvaluationWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: "添加候选" }));
  fireEvent.click(screen.getByRole("button", { name: "保存候选" }));
  await waitFor(() => expect(mocks.addCandidate).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "run-b" }));
  fireEvent.click(screen.getByRole("button", { name: "添加候选" }));
  expect(screen.getByRole("heading", { name: "添加候选结果 · run-b" })).toBeTruthy();
  await act(async () => { request.resolve(); await request.promise; });
  expect(screen.getByRole("heading", { name: "添加候选结果 · run-b" })).toBeTruthy();
  expect(input.onDirtyChange).toHaveBeenLastCalledWith(true);
});

it("创建请求等待期间重新打开登记抽屉，旧成功回执不能关闭新草稿或抢回批次选择", async () => {
  const request = deferred<{ evaluation_run: EvaluationRun }>(); mocks.createRun.mockReturnValue(request.promise);
  const input = props(); render(<ModelEvaluationWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: "登记已有评测报告" }));
  fireEvent.click(screen.getByRole("button", { name: "创建批次" }));
  await waitFor(() => expect(mocks.createRun).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "关闭当前抽屉" }));
  fireEvent.click(screen.getByRole("button", { name: "run-b" }));
  fireEvent.click(screen.getByRole("button", { name: "登记已有评测报告" }));
  await act(async () => { request.resolve({ evaluation_run: run("created-a") }); await request.promise; });
  expect(screen.getByRole("heading", { name: "登记已有离线评测报告" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "run-b" })).toBeTruthy();
  expect(input.onDirtyChange).toHaveBeenLastCalledWith(true);
});

it("A失效请求等待时切换到B的新确认，旧回执不能关闭B或重置它的表单", async () => {
  const request = deferred<void>(); mocks.invalidateRun.mockReturnValue(request.promise);
  const input = props(); render(<ModelEvaluationWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: "标记失效" }));
  fireEvent.click(screen.getByRole("button", { name: "确认失效" }));
  await waitFor(() => expect(mocks.invalidateRun).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "关闭当前确认" }));
  fireEvent.click(screen.getByRole("button", { name: "run-b" }));
  fireEvent.click(screen.getByRole("button", { name: "标记失效" }));
  const resetCount = mocks.form.resetFields.mock.calls.length;
  await act(async () => { request.resolve(); await request.promise; });
  expect(screen.getByRole("dialog", { name: "标记评测证据失效" })).toBeTruthy();
  expect(mocks.form.resetFields).toHaveBeenCalledTimes(resetCount);
  expect(input.onDirtyChange).toHaveBeenLastCalledWith(true);
});

it("组件卸载后才到达的保存结果不再刷新或通知原学校", async () => {
  const request = deferred<void>(); mocks.addCandidate.mockReturnValue(request.promise);
  const input = props(); const view = render(<ModelEvaluationWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: "添加候选" }));
  fireEvent.click(screen.getByRole("button", { name: "保存候选" }));
  await waitFor(() => expect(mocks.addCandidate).toHaveBeenCalledOnce());
  view.unmount();
  await act(async () => { request.resolve(); await request.promise; });
  expect(input.onRefresh).not.toHaveBeenCalled();
  expect(mocks.success).not.toHaveBeenCalled();
});

it("同一帧双击在表单校验前互斥，关闭后旧校验结果不能再发送请求", async () => {
  const validation = deferred<{ reason: string; total_cost_micros: number }>(); mocks.form.validateFields.mockReturnValue(validation.promise);
  const input = props(); render(<ModelEvaluationWorkspace {...input} />);
  fireEvent.click(await screen.findByRole("button", { name: "添加候选" }));
  const save = screen.getByRole("button", { name: "保存候选" });
  fireEvent.click(save); fireEvent.click(save);
  expect(mocks.form.validateFields).toHaveBeenCalledOnce();
  expect(input.onSavingChange).toHaveBeenCalledWith(true);
  fireEvent.click(screen.getByRole("button", { name: "关闭当前抽屉" }));
  await act(async () => { validation.resolve({ reason: "本次取消提交", total_cost_micros: 0 }); await validation.promise; });
  expect(mocks.addCandidate).not.toHaveBeenCalled();
  expect(input.onSavingChange).toHaveBeenLastCalledWith(false);
});
