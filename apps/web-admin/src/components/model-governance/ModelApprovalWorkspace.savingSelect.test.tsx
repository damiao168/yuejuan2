// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App, type DrawerProps } from "antd";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { EvaluationCandidate, EvaluationRun } from "../../api/modelGovernance";
import { ModelApprovalWorkspace } from "./ModelApprovalWorkspace";

const mocks = vi.hoisted(() => ({ create: vi.fn(), revoke: vi.fn() }));
vi.mock("../../api/modelGovernance", () => ({
  createModelApproval: mocks.create,
  revokeModelApproval: mocks.revoke
}));
vi.mock("../ResponsiveTable", () => ({ ResponsiveTable: () => null }));
vi.mock("antd", async () => {
  const real = await vi.importActual<typeof import("antd")>("antd");
  // 仅省去抽屉动效和无关撤销弹窗；Form、Select 及其禁用状态继承均使用真实 AntD。
  return {
    ...real,
    Modal: () => null,
    Drawer: ({ open, title, children, extra }: DrawerProps) => open ? (
      <section role="dialog" aria-label={typeof title === "string" ? title : undefined}>
        {children}{extra}
      </section>
    ) : null
  };
});

function candidate(id: string): EvaluationCandidate {
  return {
    id: `candidate-${id}`, run_id: "run-a", deployment_id: `deployment-${id}`,
    model_config_id: id, model_name: id, provider_key: "synthetic", deployment_key: id,
    model_version: "v1", prompt_version: "prompt-v1", rubric_version: "rubric-v1",
    evaluated_samples: 1, teacher_reviewed_samples: 1, teacher_accepted_samples: 1,
    serious_error_samples: 0, evidence_valid_samples: 1, repeat_comparisons: 1,
    stable_repeat_samples: 1, p95_latency_ms: 1, total_cost_micros: 0,
    metrics: {
      teacher_acceptance_rate: 1, serious_error_rate: 0, evidence_validity_rate: 1,
      stability_rate: 1, average_cost_micros: 0
    },
    created_at: "2026-10-01T00:00:00Z"
  };
}

const run: EvaluationRun = {
  id: "run-a", run_key: "synthetic-run", display_name: "合成冻结评测",
  dataset_reference: "synthetic-dataset", dataset_sha256: "synthetic-hash",
  authorization_reference: "synthetic-authorization", evidence_class: "authorized_frozen_set",
  subject: "数学", grade: "高一", question_type: "short_answer", modality: "text",
  sample_count: 1, repeat_count: 1, status: "completed",
  candidates: [candidate("model-a"), candidate("model-b")], created_at: "2026-10-01T00:00:00Z"
};

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((finish, fail) => { resolve = finish; reject = fail; });
  return { promise, resolve, reject };
}

const modelLabel = (id: string) => `${id} · v1 · 教师接受率 100.0%`;
const modelInput = () => screen.getByLabelText<HTMLInputElement>("评测模型");
const modelSelection = () => modelInput().closest(".ant-select")?.querySelector(".ant-select-selection-item");

async function selectOption(label: string, title: string) {
  const input = screen.getByLabelText<HTMLInputElement>(label);
  const selector = input.closest(".ant-select-selector");
  expect(selector).not.toBeNull();
  fireEvent.mouseDown(selector!);
  fireEvent.click(await screen.findByTitle(title));
  await waitFor(() => expect(input.getAttribute("aria-expanded")).toBe("false"));
}

async function openDraft() {
  const refresh = vi.fn().mockResolvedValue(undefined);
  render(<App><ModelApprovalWorkspace tenantID="tenant-a" approvals={[]} evaluationRuns={[run]} canManage onRefresh={refresh} /></App>);
  fireEvent.click(screen.getByRole("button", { name: "新建批准" }));
  expect(modelInput().disabled).toBe(true);
  await selectOption("授权冻结集评测", "合成冻结评测 · 数学/高一/short_answer");
  expect(modelInput().disabled).toBe(false);
  await selectOption("评测模型", modelLabel("model-a"));
  expect(modelSelection()?.getAttribute("title")).toBe(modelLabel("model-a"));
  await selectOption("评测模型", modelLabel("model-b"));
  expect(modelSelection()?.getAttribute("title")).toBe(modelLabel("model-b"));
  fireEvent.change(screen.getByLabelText("决策引用"), { target: { value: "synthetic-review" } });
  fireEvent.change(screen.getByLabelText("批准原因"), { target: { value: "合成批准原因" } });
  return {
    refresh,
    payload: {
      tenant_id: "tenant-a", evaluation_run_id: "run-a", model_config_id: "model-b",
      manual_review_rate: 1, decision_reference: "synthetic-review", reason: "合成批准原因",
      expires_at: new Date(screen.getByLabelText<HTMLInputElement>("批准到期时间").value).toISOString()
    }
  };
}

function expectFrozenModel() {
  const input = modelInput();
  expect(input.disabled).toBe(true);
  expect(screen.getByLabelText<HTMLTextAreaElement>("批准原因").disabled).toBe(true);
  // 请求未回执时尝试下拉和键盘改选，草稿仍须与已提交载荷保持一致。
  fireEvent.mouseDown(input.closest(".ant-select-selector")!);
  fireEvent.keyDown(input, { key: "ArrowDown", code: "ArrowDown" });
  fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(modelSelection()?.getAttribute("title")).toBe(modelLabel("model-b"));
}

beforeEach(() => {
  vi.clearAllMocks();
  window.matchMedia = vi.fn().mockImplementation(() => ({
    matches: false, addListener() {}, removeListener() {}, addEventListener() {},
    removeEventListener() {}, dispatchEvent() { return false; }
  }));
});
afterEach(cleanup);

it("真实 AntD 模型下拉保存前可改选，保存期间冻结当前选择，成功回执关闭草稿", async () => {
  const request = deferred();
  mocks.create.mockReturnValue(request.promise);
  const { refresh, payload } = await openDraft();
  fireEvent.click(screen.getByRole("button", { name: "确认批准" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
  expectFrozenModel();
  expect(mocks.create).toHaveBeenCalledWith(payload);
  await act(async () => { request.resolve(); await request.promise; });
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "新建题型级模型批准" })).toBeNull());
  expect(refresh).toHaveBeenCalledOnce();
  expect(mocks.create).toHaveBeenCalledOnce();
}, 20_000);

it("真实 AntD 模型下拉保存失败后保留当前选择并解冻，重新改选后按新值重试", async () => {
  const request = deferred();
  const retry = deferred();
  mocks.create.mockReturnValueOnce(request.promise).mockReturnValueOnce(retry.promise);
  const { refresh, payload } = await openDraft();
  fireEvent.click(screen.getByRole("button", { name: "确认批准" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
  expectFrozenModel();
  expect(mocks.create).toHaveBeenNthCalledWith(1, payload);
  await act(async () => {
    request.reject(new Error("合成保存失败"));
    await request.promise.catch(() => undefined);
  });
  await waitFor(() => expect(modelInput().disabled).toBe(false));
  expect(screen.getByRole("dialog", { name: "新建题型级模型批准" })).toBeTruthy();
  expect(modelSelection()?.getAttribute("title")).toBe(modelLabel("model-b"));
  expect(screen.getByLabelText<HTMLTextAreaElement>("批准原因").value).toBe("合成批准原因");
  expect(refresh).not.toHaveBeenCalled();
  await selectOption("评测模型", modelLabel("model-a"));
  expect(modelSelection()?.getAttribute("title")).toBe(modelLabel("model-a"));
  fireEvent.click(screen.getByRole("button", { name: "确认批准" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(2));
  expect(modelInput().disabled).toBe(true);
  expect(mocks.create).toHaveBeenNthCalledWith(2, { ...payload, model_config_id: "model-a" });
  await act(async () => { retry.resolve(); await retry.promise; });
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "新建题型级模型批准" })).toBeNull());
  expect(refresh).toHaveBeenCalledOnce();
}, 20_000);
