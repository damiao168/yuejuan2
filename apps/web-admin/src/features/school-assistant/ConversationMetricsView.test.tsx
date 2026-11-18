// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { SchoolChatModel } from "../../api/schoolAiChat";
import { ConversationMetricsView } from "./ConversationMetricsView";
import type { ConversationTurnMetrics } from "./conversationMetrics";
import { formatConversationTokens } from "./tokenFormat";

afterEach(cleanup);

const model: SchoolChatModel = {
  id: "model-a", available: true, model_name: "test-model", message: "", context_window_tokens: 128_000,
};

function measured(): ConversationTurnMetrics {
  return {
    modelConfigID: "model-a", modelName: "test-model",
    contextBudget: {
      phase: "answer", estimated_input_tokens: 32_768, input_budget_tokens: 60_000,
      max_output_tokens: 4_096, context_window_tokens: 131_072, dropped_messages: 0,
      estimation_method: "utf8_bytes", memory_used: false, memory_omitted: false, accepted: true,
    },
    generation: { model_config_id: "model-a", output_tokens: 241, duration_ms: 2_000, tokens_per_second: 120.5 },
  };
}

describe("对话下方的上下文容量与平均速率", () => {
  it("首次只展示应用配置容量，未知用量和速率保留为空", () => {
    const { rerender } = render(<ConversationMetricsView model={model} />);
    const metrics = screen.getByLabelText("对话用量");
    expect(metrics.textContent).toContain("本次输入 — / 应用上限 128K Token");
    expect(metrics.textContent).toContain("平均速率（含等待） —");
    expect(metrics.querySelector("[aria-live]")).toBeNull();
    rerender(<ConversationMetricsView model={null} />);
    expect(metrics.textContent).toBe("本次输入 —平均速率（含等待） —");
  });

  it("使用最近接受的输入估算和请求窗口，保留准确数字与计量口径", () => {
    render(<ConversationMetricsView model={model} metrics={measured()} />);
    const metrics = screen.getByLabelText("对话用量");
    expect(metrics.textContent).toContain("本次输入 约 32.8K / 应用上限 131K Token（25.0% · 估算）");
    expect(metrics.textContent).toContain("平均速率（含等待） 120.5 Token/s");
    expect(metrics.children[0].getAttribute("title")).toContain("估算输入 32,768 Token");
    expect(metrics.children[0].getAttribute("title")).toContain("输入预算 60,000 Token，预留输出 4,096 Token");
    expect(metrics.children[1].getAttribute("title")).toContain("包含请求准备、首 Token 等待、网络及 SSE 传输");
    expect(metrics.children[1].getAttribute("title")).toContain("不是纯模型解码速率");
    expect(metrics.children[1].getAttribute("title")).toContain("排除此前的搜索和工具耗时");
  });

  it("流式回复没有供应商计量时显示统计中，结束后也不把未知速率写成零", () => {
    const current = { ...measured(), generation: undefined };
    const { rerender } = render(<ConversationMetricsView model={model} metrics={current} pending />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("平均速率（含等待） 统计中");
    rerender(<ConversationMetricsView model={model} metrics={current} />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("平均速率（含等待） —");
  });

  it("切换到同名但不同配置的模型后，旧容量和旧速率不能沿用", () => {
    const { rerender } = render(<ConversationMetricsView model={model} metrics={measured()} />);
    rerender(<ConversationMetricsView model={{ ...model, id: "model-b", context_window_tokens: 256_000 }} metrics={measured()} />);
    expect(screen.getByLabelText("对话用量").textContent).toBe("本次输入 — / 应用上限 256K Token平均速率（含等待） —");
  });

  it("旧模型接口没有配置ID时允许按模型名匹配，异名指标仍不展示", () => {
    const current = { ...measured(), modelConfigID: undefined, generation: undefined };
    const { rerender } = render(<ConversationMetricsView model={{ ...model, id: undefined }} metrics={current} />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("约 32.8K");
    rerender(<ConversationMetricsView model={{ ...model, id: undefined, model_name: "other-model" }} metrics={current} />);
    expect(screen.getByLabelText("对话用量").textContent).not.toContain("32.8K");
  });

  it("拒绝的预算不能显示为已用上下文，来源不同的生成指标不能显示为当前模型速率", () => {
    const current = measured();
    current.contextBudget!.accepted = false;
    current.generation!.model_config_id = "other-model";
    render(<ConversationMetricsView model={model} metrics={current} />);
    expect(screen.getByLabelText("对话用量").textContent).toBe("本次输入 — / 应用上限 128K Token平均速率（含等待） —");
  });

  it("极低但有效的速率不会四舍五入成零", () => {
    const generation = { output_tokens: 1, duration_ms: 20_000, tokens_per_second: 0.05 };
    render(<ConversationMetricsView model={model} metrics={{ ...measured(), generation }} />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("平均速率（含等待） <0.1 Token/s");
  });

  it.each([
    { output_tokens: 0, duration_ms: 2_000, tokens_per_second: 120.5 },
    { output_tokens: 241, duration_ms: 0, tokens_per_second: 120.5 },
    { output_tokens: 241, duration_ms: Number.NaN, tokens_per_second: 120.5 },
    { output_tokens: 241, duration_ms: 2_000, tokens_per_second: Number.POSITIVE_INFINITY },
  ])("不显示缺少有效计量的速率：%j", generation => {
    render(<ConversationMetricsView model={model} metrics={{ ...measured(), generation }} />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("平均速率（含等待） —");
  });

  it("收到单次调用实报后以 989 代替 4750 的调用前估算", () => {
    const current = measured();
    current.contextBudget = {
      ...current.contextBudget!, estimated_input_tokens: 4_750, actual_input_tokens: 989,
      context_window_tokens: 32_768, input_budget_tokens: 28_672,
    };
    render(<ConversationMetricsView model={model} metrics={current} />);
    const metrics = screen.getByLabelText("对话用量");
    expect(metrics.textContent).toContain("本次输入 989 / 应用上限 32.8K Token（3.0% · 实报）");
    expect(metrics.textContent).not.toContain("约 4.8K");
    expect(metrics.children[0].getAttribute("title")).toContain("上游接口实报输入 989 Token（含缓存输入）");
    expect(metrics.children[0].getAttribute("title")).toContain("调用前估算输入 4,750 Token");
    expect(metrics.children[0].getAttribute("title")).toContain("应用上限 32,768 Token");
    expect(metrics.children[0].getAttribute("title")).toContain("不是整段对话的累计用量或下一轮预计占用，不含草稿及随后生成的输出");
  });

  it.each([undefined, 0, -1, Number.NaN, Number.POSITIVE_INFINITY, 1.5])("缺少或非法实报 %s 时降级为调用前估算", actual_input_tokens => {
    const current = measured();
    current.contextBudget = {
      ...current.contextBudget!, estimated_input_tokens: 4_750, actual_input_tokens,
      context_window_tokens: 32_768, input_budget_tokens: 28_672,
    };
    render(<ConversationMetricsView model={model} metrics={current} />);
    const metrics = screen.getByLabelText("对话用量");
    expect(metrics.textContent).toContain("本次输入 约 4.8K / 应用上限 32.8K Token（14.5% · 估算）");
    expect(metrics.textContent).not.toContain("实报");
  });

  it("实报有效而调用前估算非法时，仍显示实报输入", () => {
    const current = measured();
    current.contextBudget = {
      ...current.contextBudget!, estimated_input_tokens: Number.NaN, actual_input_tokens: 989,
      context_window_tokens: 32_768,
    };
    render(<ConversationMetricsView model={model} metrics={current} />);
    expect(screen.getByLabelText("对话用量").textContent).toContain("989 / 应用上限 32.8K Token（3.0% · 实报）");
  });

  it("实报超过应用上限时保留真实比例，并提示核对配置", () => {
    const current = measured();
    current.contextBudget = { ...current.contextBudget!, actual_input_tokens: 65_536, context_window_tokens: 32_768 };
    render(<ConversationMetricsView model={model} metrics={current} />);
    const metrics = screen.getByLabelText("对话用量");
    expect(metrics.textContent).toContain("本次输入 65.5K / 应用上限 32.8K Token（200.0% · 实报）");
    expect(metrics.children[0].getAttribute("title")).toContain("容量配置待校验");
  });

  it.each([
    [517, "517"], [12_200, "12.2K"], [517_000, "517K"], [1_200_000, "1.2M"],
  ])("沿用 Harness 的紧凑 Token 单位：%d → %s", (value, expected) => {
    expect(formatConversationTokens(value)).toBe(expected);
  });
});
