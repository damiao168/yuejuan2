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

  await screen.findByText("方案尚未确认，请联系学校管理员。");
  expect(screen.getAllByText("单模型评分")).toHaveLength(2);
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
  expect(screen.queryByRole("spinbutton", { name: "三评后允许分差" })).toBeNull();
  const save = screen.getByRole("button", { name: "确认本场大题 AI 评分方案" }) as HTMLButtonElement;
  expect(primary.value).toBe("");
  expect(save.disabled).toBe(true);
  expect(screen.getByText("需要学校管理员先确认本场大题方案；只有一个模型也可确认单模型。")).toBeTruthy();
  fireEvent.change(primary, { target: { value: "2" } });
  expect(save.disabled).toBe(false);
  expect(screen.getByText("本场大题允许分差尚未填写，需要学校管理员先确认。")).toBeTruthy();
  fireEvent.change(primary, { target: { value: "0" } });
  expect(save.disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => expect(api.save).toHaveBeenCalledWith("exam", expect.objectContaining({
    decision_config: { primary_allowed_diff_points: 0, teacher_allowed_diff_points: null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" }
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

it("已确认的只读方案不催促重复确认，设置页只解释一次适用范围", async () => {
  const response = singleModelResponse();
  response.can_configure = false;
  response.plan.confirmed_at = "2026-10-22T00:00:00Z";
  api.get.mockResolvedValue(response);
  render(<App><GradingAIPlanSettings examId="exam" /></App>);
  await screen.findByText("方案已确认。如需修改，请联系学校管理员。");
  expect(screen.queryByText("方案尚未确认，请联系学校管理员。")).toBeNull();
  expect(screen.queryByText("仅用于解答、计算、证明等大题；选择题和填空题不使用本方案。")).toBeNull();
  expect(screen.getByText("仅用于解答、计算、证明题；选择题和填空题不使用。可确认单模型方案。")).toBeTruthy();
  expect(api.save).not.toHaveBeenCalled();
});

it.each([false, true])("编辑规则按独立第三模型显示，第三模型独立=%s", async (independent) => {
  const response = singleModelResponse();
  response.available_models.push({ ...response.available_models[0], model_config_id: "model-b", model_name: "model-b" });
  response.available_models.push({ ...response.available_models[0], model_config_id: "model-c", model_name: independent ? "model-c" : "model-b" });
  response.plan.roles = { primary_a: { model_config_id: "model-a", strength_rank: 1 }, primary_b: { model_config_id: "model-b", strength_rank: 1 }, arbiter: { model_config_id: "model-c", strength_rank: 2 } };
  api.get.mockResolvedValue(response);
  const view = render(<App><GradingAIPlanSettings examId="exam" /></App>);
  const section = await screen.findByRole("region", { name: "编辑中的分差设置" });
  expect(section.textContent).toContain(independent ? "两个分差均必填" : "填写 A、B 允许分差");
  expect(section.textContent?.includes("三评按 A 25%、B 25%、C 50% 汇总。")).toBe(independent);
  expect(section.querySelector("details")?.open).toBe(false);
  expect(view.getByRole("button", { name: "确认本场大题 AI 评分方案" }).hasAttribute("disabled")).toBe(true);
  expect(api.save).not.toHaveBeenCalled();
});

it("所选模型串行批量检测，失败重试复用原请求身份且不重复调用通过项", async () => {
  const response = singleModelResponse();
  response.available_models.push({ ...response.available_models[0], model_config_id: "model-b", model_name: "second", display_name: "第二模型" });
  response.plan.roles = { primary_a: { model_config_id: "model-a", strength_rank: 1 }, primary_b: { model_config_id: "model-b", strength_rank: 1 } };
  api.get.mockResolvedValue(response);
  api.probe.mockResolvedValueOnce({ vision_probe_status: "passed" }).mockRejectedValueOnce(new Error("synthetic network interruption")).mockResolvedValueOnce({ vision_probe_status: "passed" });
  render(<App><GradingAIPlanSettings examId="exam" /></App>);
  fireEvent.click(await screen.findByRole("button", { name: "检测所选模型图片能力（2 个）" }));
  const retry = await screen.findByRole("button", { name: "只重试未通过模型（1 个）" });
  await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(false));
  expect(api.probe.mock.calls.map(call => call[1])).toEqual(["model-a", "model-b"]);
  const originalID = api.probe.mock.calls[1][2].requestId;
  fireEvent.click(retry);
  await waitFor(() => expect(api.probe).toHaveBeenCalledTimes(3));
  expect(api.probe.mock.calls[2][1]).toBe("model-b"); expect(api.probe.mock.calls[2][2].requestId).toBe(originalID);
  expect(api.save).not.toHaveBeenCalled();
}, 30000);
