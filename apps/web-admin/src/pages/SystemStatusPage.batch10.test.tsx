// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SystemStatus, WorkerServiceStatus } from "../api/system";
import { SystemStatusPage } from "./SystemStatusPage";

const mocks = vi.hoisted(() => ({ get: vi.fn(), table: null as Record<string, unknown> | null }));
vi.mock("../api/system", () => ({ getSystemStatus: mocks.get }));
vi.mock("../components/ResponsiveTable", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return { ResponsiveTable: (props: Record<string, unknown>) => { mocks.table = props; return React.createElement("div", { "data-table": "dependencies" }); } };
});
vi.mock("../components/PageState", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", { "data-state": "loading" }, label),
    ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => React.createElement("div", { "data-state": "error" }, message, React.createElement("button", { onClick: onRetry }, "重试"))
  };
});
vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const wrapper = ({ children }: { children: ReactNode }) => React.createElement("div", null, children);
  return {
    Alert: ({ type, message, description }: { type: string; message: ReactNode; description: ReactNode }) => React.createElement("div", { "data-alert": type }, message, description),
    Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => React.createElement("button", { onClick }, children),
    Space: wrapper,
    Tooltip: wrapper,
    Tag: ({ color, children }: { color: string; children: ReactNode }) => React.createElement("span", { "data-tag": color }, children),
    Descriptions: Object.assign(wrapper, { Item: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("div", null, label, children) })
  };
});

function worker(overrides: Partial<WorkerServiceStatus> = {}): WorkerServiceStatus {
  return {
    name: "ocr_worker", worker_service: "ocr", queue_name: "queue", status: "ok", availability: "online", automation_available: true,
    fresh_instances: 1, stale_instances: 2, queued_tasks: 3, in_flight_tasks: 4, dead_letter_tasks: 5, failed_last_hour: 6,
    stale_after_sec: 60, impact_code: "available", impact: "识别影响说明", action: "处理动作", ...overrides
  };
}

function status(overrides: Partial<SystemStatus> = {}): SystemStatus {
  return {
    status: "healthy", service: "gateway", environment: "development", version: "v1", git_sha: "sha", build_time: "2026-10-04",
    image_digest: "digest", release_id: "local", schema_version: "183", started_at: "2026-10-04", generated_at: "2026-10-04T08:00:00Z",
    uptime_sec: 90060, dependencies: [], worker_services: [worker()],
    observability: { log_format: "json", system_log_stream: "system", audit_log_stream: "audit", request_id_header: "request-id", trace_id_header: "trace-id", slow_request_threshold_ms: 500, slow_query_log: "enabled", sensitive_log_policy: "不记录答卷正文" },
    ...overrides
  };
}

describe("系统状态页告警重构的共同回归", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks(); vi.stubEnv("VITE_RELEASE_ID", "local"); mocks.table = null;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.get.mockResolvedValue(status());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllEnvs(); });
  async function render() { await act(async () => root.render(<SystemStatusPage />)); }
  async function click(label: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
    expect(button).toBeDefined(); await act(async () => button!.click());
  }

  it.each([
    [true, "online", "在线", "success", "success"], [true, "not_configured", "在线", "success", "success"],
    [true, "stale", "在线", "success", "success"], [true, "unavailable", "在线", "success", "success"],
    [false, "online", "不可用", "error", "error"], [false, "not_configured", "未启用", "default", "info"],
    [false, "stale", "心跳失联", "warning", "warning"], [false, "unavailable", "不可用", "error", "error"]
  ] as const)("保持自动能力与可用性组合 %s/%s 的展示优先级", async (available, availability, text, tone, alertType) => {
    mocks.get.mockResolvedValue(status({ worker_services: [worker({ automation_available: available, availability })] }));
    await render(); const panel = host.querySelector(".system-worker-status");
    expect(panel?.classList.contains(availability)).toBe(true);
    expect(panel?.querySelector(`[data-tag="${tone}"]`)?.textContent).toBe(text);
    expect(panel?.querySelector(`[data-alert="${alertType}"]`)?.textContent).toBe("识别影响说明处理建议：处理动作");
    expect([...panel!.querySelectorAll(".system-worker-metrics strong")].map((node) => node.textContent)).toEqual(["1", "2", "3", "4", "6", "5"]);
  });

  it.each([undefined, [], [worker({ name: "other" })]])("OCR 项不存在时保留警告，不使用其他 worker", async (services) => {
    mocks.get.mockResolvedValue(status({ worker_services: services })); await render();
    expect(host.textContent).toContain("暂未获取到文字识别服务状态"); expect(host.querySelector(".system-worker-status")).toBeNull();
  });

  it("只使用第一个 OCR 项，并保留无效心跳原值", async () => {
    mocks.get.mockResolvedValue(status({ worker_services: [worker({ last_seen_at: "invalid-time" }), worker({ automation_available: false })] }));
    await render(); expect(host.textContent).toContain("处理建议：处理动作 最近心跳：invalid-time");
    expect(host.querySelectorAll(".system-worker-status")).toHaveLength(1);
  });

  it.each([["local", "remote", false], ["release-A", "local", false], ["release-A", "release-A", false], ["release-A", "release-B", true]] as const)("保留发布版本 %s/%s 的告警边界", async (front, back, warning) => {
    vi.stubEnv("VITE_RELEASE_ID", front); mocks.get.mockResolvedValue(status({ release_id: back })); await render();
    expect(host.textContent?.includes("前后端发布版本暂不一致")).toBe(warning);
  });

  it("刷新失败仍保留之前状态并允许重试替换", async () => {
    await render(); mocks.get.mockRejectedValueOnce(new Error("连接断开")); await click("刷新");
    expect(host.textContent).toContain("gateway"); expect(host.querySelector('[data-state="error"]')).not.toBeNull();
    mocks.get.mockResolvedValueOnce(status({ service: "gateway-new", status: "degraded", uptime_sec: -1 })); await click("重试");
    expect(host.textContent).toContain("gateway-new"); expect(host.textContent).toContain("降级"); expect(host.querySelector('[data-state="error"]')).toBeNull();
  });

  it("首个请求未返回前显示加载且不伪造状态", async () => {
    let resolve!: (value: SystemStatus) => void;
    mocks.get.mockReturnValue(new Promise<SystemStatus>((done) => { resolve = done; })); await render();
    expect(host.querySelector('[data-state="loading"]')?.textContent).toBe("读取系统状态"); expect(host.querySelector(".system-status-summary")).toBeNull();
    await act(async () => resolve(status())); expect(host.querySelector('[data-state="loading"]')).toBeNull();
    expect(host.textContent).toContain("1天 1小时 1分");
  });

  it("依赖统计和表格契约保持一致", async () => {
    const dependencies = ["ok", "not_configured", "error", "disabled", "mock"].map((state, index) => ({ name: `service-${index}`, status: state, required: index === 0, duration_ms: 5, checked_at: "invalid", detail: "说明" })) as SystemStatus["dependencies"];
    mocks.get.mockResolvedValue(status({ dependencies })); await render();
    expect(host.textContent).toContain("共 5 项，正常 1，未配置 1，异常 1");
    expect(mocks.table).toMatchObject({ dataSource: dependencies, pagination: false, rowKey: "name", scroll: { x: 900 }, mobilePrimaryKeys: ["name", "status", "detail"] });
    const columns = mocks.table!.columns as Array<{ title: string; render?: (value: unknown, record: Record<string, unknown>) => unknown }>;
    const explanation = columns.find((column) => column.title === "说明")!.render!;
    expect(explanation(undefined, { error: "", detail: "详情" })).toBe("");
    expect(explanation(undefined, { detail: "详情" })).toBe("详情");
    expect(explanation(undefined, {})).toBe("-");
  });
});
