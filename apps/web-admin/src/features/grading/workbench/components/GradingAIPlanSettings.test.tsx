// @vitest-environment jsdom
import { App } from "antd";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { GradingAIPlanResponse } from "../../../../api/gradingAIPlan";
import { GradingAIPlanSettings, GradingAIPlanSummary } from "./GradingAIPlanSettings";

const api = vi.hoisted(() => ({ get: vi.fn(), probe: vi.fn(), save: vi.fn() }));
const session = vi.hoisted(() => ({ scope: "school:test", generation: 1 }));
vi.mock("../../../../api/gradingAIPlan", () => ({
  getGradingAIPlan: api.get, probeGradingAIModel: api.probe, saveGradingAIPlan: api.save
}));
vi.mock("../../../../api/businessCommand", () => ({ getBusinessCommandSession: () => session }));
vi.mock("../gradingWorkbench.model", () => ({ formatError: (error: unknown) => String(error) }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

function singleModelResponse(): GradingAIPlanResponse {
  const model = { model_config_id: "model-a", display_name: "已选模型", provider_key: "provider", model_name: "vision-model", model_version: "v1", config_revision: "1", binding_revision: "1" };
  return {
    can_configure: true, available_models: [model],
    plan: { version: 1, revision: "1", fallback_model_config_id: model.model_config_id, roles: {},
      decision_config: { primary_allowed_diff_points: null, teacher_allowed_diff_points: null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" },
      effective_mode: "single", reason_codes: [], resolved_roles: {}, single_model: model, independent_model_count: 1 }
  };
}

it("服务端只读时可查看单模型方案，但不提供保存或主动探测入口", async () => {
  const response = { ...singleModelResponse(), can_configure: false };
  api.get.mockResolvedValue(response);
  const onConfigure = vi.fn();
  render(<App><GradingAIPlanSettings examId="exam" /><GradingAIPlanSummary examId="exam" onConfigure={onConfigure} /></App>);

  await screen.findByText("请学校管理员确认本场大题 AI 评分方案。");
  expect(screen.getAllByText("单模型评分 · 非三智能体")).toHaveLength(2);
  expect(screen.queryByLabelText("评分模型 A")).toBeNull();
  expect(screen.queryByRole("button", { name: "确认本场大题 AI 评分方案" })).toBeNull();
  expect(screen.queryByRole("button", { name: "检测图片能力" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "查看本场 AI 评分方案" }));
  expect(onConfigure).toHaveBeenCalledOnce();
  expect(api.save).not.toHaveBeenCalled();
  expect(api.probe).not.toHaveBeenCalled();
});

it("两个独立模型要求手填绝对分差，允许零分且不会把表单值冒充已保存摘要", async () => {
  const response = singleModelResponse();
  const second = { ...response.available_models[0], model_config_id: "model-b", model_name: "second-model", display_name: "第二模型" };
  response.available_models.push(second);
  response.plan.roles = { primary_a: { model_config_id: "model-a", strength_rank: 1 }, primary_b: { model_config_id: "model-b", strength_rank: 1 } };
  response.plan.effective_mode = "dual";
  response.plan.independent_model_count = 2;
  api.get.mockResolvedValue(response);
  api.save.mockResolvedValue({ ...response, plan: { ...response.plan, confirmed_at: "2026-10-22T00:00:00Z", decision_config: { ...response.plan.decision_config, primary_allowed_diff_points: 0, teacher_allowed_diff_points: 0 } } });
  render(<App><GradingAIPlanSettings examId="exam" /></App>);

  const primary = await screen.findByRole("spinbutton", { name: "前两模型允许分差" }) as HTMLInputElement;
  const teacher = screen.getByRole("spinbutton", { name: "三评后允许分差" }) as HTMLInputElement;
  const save = screen.getByRole("button", { name: "确认本场大题 AI 评分方案" }) as HTMLButtonElement;
  expect(primary.value).toBe("");
  expect(teacher.value).toBe("");
  expect(save.disabled).toBe(true);
  expect(screen.getByText("需要学校管理员先确认本场大题方案；只有一个模型也可确认单模型。")).toBeTruthy();
  fireEvent.change(primary, { target: { value: "2" } });
  fireEvent.change(teacher, { target: { value: "1" } });
  expect(save.disabled).toBe(true);
  fireEvent.change(teacher, { target: { value: "2" } });
  expect(save.disabled).toBe(false);
  expect(screen.getByText("本场大题允许分差尚未填写，需要学校管理员先确认。")).toBeTruthy();
  fireEvent.change(primary, { target: { value: "0" } });
  fireEvent.change(teacher, { target: { value: "0" } });
  expect(save.disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => expect(api.save).toHaveBeenCalledWith("exam", expect.objectContaining({
    decision_config: { primary_allowed_diff_points: 0, teacher_allowed_diff_points: 0, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" }
  })));
  expect(api.probe).not.toHaveBeenCalled();
});

it("跨供应商的同名称同版本模型不算独立，单模型可不填分差直接确认", async () => {
  const response = singleModelResponse();
  const duplicate = { ...response.available_models[0], model_config_id: "duplicate", provider_key: "other-provider", model_name: " VISION-MODEL ", model_version: " V1 " };
  response.available_models.push(duplicate);
  response.plan.roles = { primary_a: { model_config_id: "model-a", strength_rank: 1 }, primary_b: { model_config_id: "duplicate", strength_rank: 1 } };
  api.get.mockResolvedValue(response);
  api.save.mockResolvedValue(response);
  render(<App><GradingAIPlanSettings examId="exam" /></App>);

  const primary = await screen.findByRole("spinbutton", { name: "前两模型允许分差" }) as HTMLInputElement;
  expect(primary.value).toBe("");
  expect(primary.disabled).toBe(true);
  const save = screen.getByRole("button", { name: "确认本场大题 AI 评分方案" }) as HTMLButtonElement;
  expect(save.disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => expect(api.save).toHaveBeenCalledWith("exam", expect.objectContaining({
    decision_config: { primary_allowed_diff_points: null, teacher_allowed_diff_points: null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" }
  })));
  expect(api.probe).not.toHaveBeenCalled();
});
