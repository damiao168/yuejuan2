// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetModelSelection, ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { ImageGradingModelForm } from "./ImageGradingModelForm";
import { modelConfigColumns } from "./modelConfigColumns";

const mocks = vi.hoisted(() => ({ probe: vi.fn() }));
vi.mock("../../../api/modelApiConfig", () => ({ probeGradingModelSelection: mocks.probe }));
vi.mock("antd", () => ({
  Button: ({ children, loading, disabled, onClick }: { children: ReactNode; loading?: boolean; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Tooltip: ({ children }: { children: ReactNode }) => children,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
const config: ManagedModelAPIConfig = {
  id: "model-a", tenant_id: "school-a", provider_key: "custom", display_name: "供应商", adapter_type: "openai_compatible",
  base_url: "https://example.invalid/v1", model_name: "视觉模型", model_version: "v1", region: "global", credential_configured: true,
  status: "active", is_default: false, last_test_status: "untested", last_capability_status: "untested", config_source: "manual", created_at: "2026-10-21T00:00:00Z", updated_at: "2026-10-21T00:00:00Z",
};
const selected: AnswerSheetModelSelection = { revision: "r1", mode: "model", model_config_id: config.id };
const passed = { capability: { visual_supported: true, model_version: "v1" } };

describe("自动阅卷模型用途与图片检测", () => {
  let host: HTMLDivElement;
  let root: Root;
  const save = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.probe.mockResolvedValue(passed);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  const render = async (nextConfig = config, selection = selected, tenantID = "school-a") => {
    await act(async () => root.render(<ImageGradingModelForm tenantID={tenantID} config={nextConfig} selection={selection} busy={false} loading={false} onRefresh={vi.fn()} onSave={save} onDirtyChange={vi.fn()} />));
  };
  const button = (text: string) => Array.from(host.querySelectorAll("button")).find(item => item.textContent === text)!;

  it("未做文本结构化检测也可独立保存，取消用途不会改成其他模型", async () => {
    await render(config, { revision: "absent", mode: "unconfigured" });
    expect(button("使用此模型自动阅卷").disabled).toBe(false);
    await act(async () => button("使用此模型自动阅卷").click());
    expect(save).toHaveBeenLastCalledWith("model");
    expect(mocks.probe).not.toHaveBeenCalled();
    expect(host.textContent).toContain("一个 API 模型测试");
    await render();
    await act(async () => button("取消用途").click());
    expect(save).toHaveBeenLastCalledWith("unconfigured");
  });

  it("更换同一配置的连接后丢弃迟到检测，并允许新配置重新检测", async () => {
    const old = deferred<typeof passed>(); mocks.probe.mockReturnValueOnce(old.promise);
    await render();
    await act(async () => button("检测图片能力").click());
    expect(button("检测图片能力").disabled).toBe(true);
    await render({ ...config, updated_at: "2026-10-21T00:01:00Z", base_url: "https://new.example.invalid/v1" });
    expect(button("检测图片能力").disabled).toBe(false);
    await act(async () => old.resolve(passed));
    expect(host.querySelector("[role=alert]")).toBeNull();
    await act(async () => button("检测图片能力").click());
    expect(mocks.probe).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("图片读取检测通过");
  });

  it("学校切换后不显示旧学校检测结果，已有通过结果随用途版本变化清除", async () => {
    const old = deferred<typeof passed>(); mocks.probe.mockReturnValueOnce(old.promise);
    await render(); await act(async () => button("检测图片能力").click());
    await render(config, selected, "school-b");
    await act(async () => old.resolve(passed));
    expect(host.querySelector("[role=alert]")).toBeNull();
    await act(async () => button("检测图片能力").click());
    expect(mocks.probe).toHaveBeenLastCalledWith("school-b");
    expect(host.querySelector("[role=alert]")).not.toBeNull();
    await render(config, { ...selected, revision: "r2" }, "school-b");
    expect(host.querySelector("[role=alert]")).toBeNull();
  });

  it("列表允许未做结构化检测的可用模型打开自动阅卷设置", async () => {
    const open = vi.fn();
    const columns = modelConfigColumns({ tenantID: "school-a", operations: {}, credentialHintIDs: new Set(), applicationsReady: true, gradingModelIDs: new Set(), documentFollowsChat: false, answerSheetReady: true,
      openApplication: open, openEdit: vi.fn(), probe: vi.fn(), removeConfig: vi.fn(), showConnectionDetails: vi.fn(), showTextDetails: vi.fn(), showCapabilityDetails: vi.fn(), updateUsage: vi.fn(),
    });
    const column = columns.find(item => item.key === "assignment")!;
    const renderCell = "render" in column ? column.render! : undefined;
    await act(async () => root.render(renderCell!(undefined, config, 0) as ReactNode));
    const grading = host.querySelector<HTMLButtonElement>('button[aria-label="视觉模型：阅卷配置"]')!;
    expect(grading.disabled).toBe(false);
    await act(async () => grading.click());
    expect(open).toHaveBeenCalledWith(config, "grading");
  });
});
