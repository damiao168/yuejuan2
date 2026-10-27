// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "antd";
import { ModelApprovalWorkspace } from "./ModelApprovalWorkspace";
const mocks = vi.hoisted(() => ({ create: vi.fn(), revoke: vi.fn(), forms: [] as any[] }));
vi.mock("../../api/modelGovernance", () => ({ createModelApproval: mocks.create, revokeModelApproval: mocks.revoke }));
vi.mock("../ResponsiveTable", () => ({ ResponsiveTable: () => null }));
vi.mock("antd", async () => {
  const real = await vi.importActual<typeof import("antd")>("antd");
  const React = await vi.importActual<typeof import("react")>("react");
  // 观察真实 Form 实例填写合成字段；校验和Input保留，Drawer/Select/InputNumber仅作薄展示替身。
  const Form = Object.assign((props: any) => React.createElement(real.Form, props), real.Form, {
    useForm: (...args: any[]) => {
      const result = real.Form.useForm(...args);
      if (!mocks.forms.includes(result[0])) mocks.forms.push(result[0]);
      return result;
    },
  });
  const textarea = real.Input.TextArea; const input = Object.assign(React.forwardRef((props: any, ref: any) => React.createElement(real.Input, { ...props, ref })), real.Input, { TextArea: React.forwardRef((props: any, ref: any) => React.createElement(textarea, { ...props, ref, autoSize: false })) });
  return { ...real, Form, Input: input, Select: () => null, InputNumber: () => null, Modal: () => null, Drawer: ({ open, title, children, extra, onClose }: any) => open ? React.createElement("section", { role: "dialog", "aria-label": title }, React.createElement("button", { onClick: onClose }, "Close"), children, extra) : null };
});
function deferred() { let resolve!: (value: any) => void; const promise = new Promise(finish => { resolve = finish; }); return { promise, resolve }; }
const run = { id: "run-a", display_name: "Synthetic frozen evaluation", status: "completed", evidence_class: "authorized_frozen_set", subject: "mathematics", grade: "senior", question_type: "short_answer", candidates: [{ model_config_id: "model-a", model_name: "Synthetic model", model_version: "v1", metrics: { teacher_acceptance_rate: 1 } }] } as any;
beforeEach(() => { vi.clearAllMocks(); mocks.forms.length = 0; window.matchMedia = vi.fn().mockImplementation(() => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent() { return false; } })); });
afterEach(cleanup);
it("真实批准组件与表单（仅Drawer薄展示）：保存期间冻结草稿关闭和新建，回执只能结束原草稿", async () => {
  const pending = deferred(); mocks.create.mockReturnValue(pending.promise);
  const refresh = vi.fn().mockResolvedValue(undefined);
  render(<App><ModelApprovalWorkspace tenantID="school-a" approvals={[]} evaluationRuns={[run]} canManage onRefresh={refresh} /></App>);
  fireEvent.click(screen.getByRole("button", { name: "新建批准" }));
  await screen.findByRole("dialog", { name: "新建题型级模型批准" });
  await act(async () => mocks.forms[0].setFieldsValue({ evaluation_run_id: "run-a", model_config_id: "model-a", manual_review_percent: 100, decision_reference: "synthetic-first", expires_at: "2030-01-01T12:00", reason: "第一份合成批准原因" }));
  fireEvent.click(screen.getByRole("button", { name: "确认批准" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
  // 即使调用公开关闭回调，保存期间也必须保留同一个草稿；新建入口同时冻结。
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(screen.getByRole("dialog", { name: "新建题型级模型批准" })).toBeTruthy();
  expect((screen.getByRole("button", { name: "新建批准" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("批准原因") as HTMLTextAreaElement).disabled).toBe(true);
  expect(mocks.forms[0].getFieldValue("reason")).toBe("第一份合成批准原因");
  await act(async () => { pending.resolve({}); await pending.promise; });
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "新建题型级模型批准" })).toBeNull());
  expect(mocks.create).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "新建批准" }));
  await screen.findByRole("dialog", { name: "新建题型级模型批准" });
  expect((screen.getByLabelText("批准原因") as HTMLTextAreaElement).value).toBe("");
  expect(refresh).toHaveBeenCalledOnce();
});
