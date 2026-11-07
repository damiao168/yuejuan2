// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { modelOperationKey } from "../lib/modelOperationQueue";
import { modelConfigColumns } from "./modelConfigColumns";
import { SchoolModelTable } from "./SchoolModelTable";

const tenantID = "synthetic-model-school";

function config(id = "first"): ManagedModelAPIConfig {
  return {
    id, tenant_id: tenantID, provider_key: "custom", display_name: id === "first" ? "其他兼容接口" : "学校备用模型", adapter_type: "openai_compatible",
    base_url: "https://synthetic.example/v1", model_name: id === "first" ? "DeepSeek-V4-Flash" : "Qwen3", model_version: "v1", region: "global",
    credential_configured: true, status: "active", is_default: id === "first", last_test_status: "success",
    last_text_status: "success", last_capability_status: "success", last_capability_probe_version: "structured-json-v4",
    config_source: "auto", created_at: "2026-10-31T00:00:00Z", updated_at: "2026-10-31T00:00:00Z",
  };
}

function options(): Parameters<typeof modelConfigColumns>[0] {
  return {
    tenantID, operations: {}, credentialHintIDs: new Set(), applicationsReady: true,
    chatModelIDs: ["first"], chatAccessBusy: false, toggleChatAccess: vi.fn().mockResolvedValue(undefined),
    gradingModelIDs: new Set(), documentFollowsChat: true, answerSheetReady: true,
    openApplication: vi.fn(), openEdit: vi.fn(), probe: vi.fn().mockResolvedValue(undefined), removeConfig: vi.fn(),
    showConnectionDetails: vi.fn(), showTextDetails: vi.fn(), showCapabilityDetails: vi.fn(), updateUsage: vi.fn().mockResolvedValue(undefined),
  };
}

function viewport(width: number) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: (!query.match(/min-width:\s*(\d+)px/) || width >= Number(query.match(/min-width:\s*(\d+)px/)?.[1]))
      && (!query.match(/max-width:\s*(\d+)px/) || width <= Number(query.match(/max-width:\s*(\d+)px/)?.[1])),
    media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => true,
  }));
}

beforeEach(() => {
  viewport(1366);
  // jsdom 没有伪元素布局，忽略 AntD 滚动条测量的伪元素参数；仍使用真实表格和响应式布局。
  const computedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation(element => computedStyle(element));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("school model table direct settings", () => {
  it("shows diagnostics and actions in independent columns without expanding the row", async () => {
    const actions = options();
    const model = config();
    const view = render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[model]} />);
    await screen.findByRole("columnheader", { name: "连接结果" });
    for (const name of ["普通回复", "结构化输出", "应用用途", "操作"]) {
      expect(screen.getByRole("columnheader", { name })).toBeTruthy();
    }
    expect(screen.queryByRole("columnheader", { name: "可用性" })).toBeNull();
    expect(screen.queryByRole("columnheader", { name: "已配置用途" })).toBeNull();
    expect(view.container.querySelector("details")).toBeNull();
    expect(screen.queryByText("设置与诊断")).toBeNull();
    for (const [label, mode] of [["检测连接", "quick"], ["检测普通回复", "text"], ["检测结构化输出", "structured"]] as const) {
      fireEvent.click(screen.getByRole("button", { name: `${model.model_name}：${label}` }));
      expect(actions.probe).toHaveBeenLastCalledWith(model, mode);
    }
    fireEvent.click(screen.getByRole("button", { name: `${model.model_name}：查看普通回复详情` }));
    expect(actions.showTextDetails).toHaveBeenCalledWith(model);
    expect(actions.showConnectionDetails).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: `${model.model_name}：阅卷配置` }));
    expect(actions.openApplication).toHaveBeenCalledWith(model, "grading");
  });

  it("preserves per-model queue exclusion while keeping other models available", async () => {
    const actions = options();
    const first = config(), second = config("second");
    const busy = { ...actions, operations: { [modelOperationKey(tenantID, first.id)]: { tenantID, modelID: first.id, mode: "text" as const, phase: "queued" as const } } };
    render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(busy)} configs={[first, second]} />);
    await screen.findByRole("columnheader", { name: "连接结果" });
    const firstRow = screen.getByRole("button", { name: `编辑${first.display_name}` }).closest("tr");
    expect(firstRow).toBeTruthy();
    for (const label of ["检测连接", "检测普通回复", "检测结构化输出"]) {
      const button = screen.getByRole("button", { name: `${first.model_name}：${label}` }) as HTMLButtonElement;
      expect(button.disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(actions.probe).not.toHaveBeenCalled();
    expect((within(firstRow!).getByRole("button", { name: "删除" }) as HTMLButtonElement).disabled).toBe(true);
    const edit = within(firstRow!).getByRole("button", { name: `编辑${first.display_name}` }) as HTMLButtonElement;
    expect(edit.disabled).toBe(false);
    fireEvent.click(edit);
    expect(actions.openEdit).toHaveBeenCalledWith(first);
    fireEvent.click(screen.getByRole("button", { name: `${second.model_name}：检测连接` }));
    expect(actions.probe).toHaveBeenCalledWith(second, "quick");
  });

  it("preserves default-model deletion and unconfigured application guards after splitting columns", async () => {
    const actions = options();
    const first = config(), second = { ...config("second"), credential_configured: false };
    render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[first, second]} />);
    await screen.findByRole("columnheader", { name: "操作" });
    const defaultRow = screen.getByRole("button", { name: `编辑${first.display_name}` }).closest("tr")!;
    const otherRow = screen.getByRole("button", { name: `编辑${second.display_name}` }).closest("tr")!;
    expect((within(defaultRow).getByRole("button", { name: "删除" }) as HTMLButtonElement).disabled).toBe(true);
    const remove = within(otherRow).getByRole("button", { name: "删除" }) as HTMLButtonElement;
    expect(remove.disabled).toBe(false);
    fireEvent.click(remove);
    expect(actions.removeConfig).toHaveBeenCalledWith(second);
    for (const label of ["阅卷配置", "答题卡识别", "试卷解析"]) {
      const button = screen.getByRole("button", { name: `${second.model_name}：${label}` }) as HTMLButtonElement;
      expect(button.disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(actions.openApplication).not.toHaveBeenCalled();
  });

  it("keeps every setting directly reachable on narrow screens instead of adding another disclosure", async () => {
    viewport(390);
    const model = config();
    const actions = options();
    const view = render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[model]} />);
    await waitFor(() => expect(view.container.querySelector(".responsive-record-list")).toBeTruthy());
    expect(view.container.querySelector("details")).toBeNull();
    expect(screen.queryByText("可用性")).toBeNull();
    expect(screen.queryByText("已配置用途")).toBeNull();
    for (const label of ["检测连接", "检测普通回复", "检测结构化输出"]) {
      expect(screen.getByRole("button", { name: `${model.model_name}：${label}` })).toBeTruthy();
    }
    fireEvent.click(screen.getByRole("checkbox", { name: `${model.model_name}：允许学校对话` }));
    expect(actions.toggleChatAccess).toHaveBeenCalledWith(model.id);
    expect(screen.getByRole("button", { name: `${model.model_name}：答题卡识别` })).toBeTruthy();
  });

  it("keeps an unavailable model's authorization cancellable and explains why it cannot chat", async () => {
    const actions = options();
    const model = config();
    const view = render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[model]} />);
    await screen.findByRole("columnheader", { name: "应用用途" });
    // 连接和结构化检测都通过，也不能掩盖普通回复失败；已开放配置仍可取消授权。
    const cases: [Partial<ManagedModelAPIConfig>, string][] = [
      [{ last_text_status: "failed" }, "普通回复未通过检测"],
      [{ last_text_status: "temporary_unavailable" }, "普通回复检测暂未完成"],
      [{ last_text_status: "untested" }, "普通回复尚未验证"],
      [{ credential_configured: false }, "尚未配置密钥"],
      [{ status: "disabled" }, "模型已停用"],
    ];
    for (const [patch, reason] of cases) {
      view.rerender(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[{ ...model, ...patch }]} />);
      const checkbox = screen.getByRole("checkbox", { name: `${model.model_name}：允许学校对话` }) as HTMLInputElement;
      expect(checkbox.checked).toBe(true);
      expect(checkbox.disabled).toBe(false);
      const label = checkbox.closest("label")!;
      expect(label.textContent).toBe("对话·不可用");
      expect(label.title).toContain(reason);
      expect(label.classList.contains("is-selected")).toBe(false);
      fireEvent.click(checkbox);
      expect(actions.toggleChatAccess).toHaveBeenLastCalledWith(model.id);
    }

    view.rerender(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[model]} />);
    const recovered = screen.getByRole("checkbox", { name: `${model.model_name}：允许学校对话` }) as HTMLInputElement;
    expect(recovered.checked).toBe(true);
    expect(recovered.closest("label")?.textContent).toBe("对话 ✓");
    expect(recovered.closest("label")?.title).toContain("已开放且可用于学校对话");
  });

  it("does not let connection or structured success authorize a model whose ordinary reply failed", async () => {
    const actions = { ...options(), chatModelIDs: [] };
    const model = { ...config(), last_text_status: "failed" as const };
    const view = render(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[model]} />);
    await screen.findByRole("columnheader", { name: "应用用途" });
    const blocked = screen.getByRole("checkbox", { name: `${model.model_name}：允许学校对话` }) as HTMLInputElement;
    expect(blocked.checked).toBe(false);
    expect(blocked.disabled).toBe(true);
    expect(blocked.closest("label")?.title).toBe("当前不能开放对话：普通回复未通过检测");

    view.rerender(<SchoolModelTable tenantID={tenantID} loading={false} columns={modelConfigColumns(actions)} configs={[{ ...model, last_text_status: "success" }]} />);
    const recovered = screen.getByRole("checkbox", { name: `${model.model_name}：允许学校对话` }) as HTMLInputElement;
    expect(recovered.disabled).toBe(false);
    fireEvent.click(recovered);
    expect(actions.toggleChatAccess).toHaveBeenCalledWith(model.id);
  });
});
