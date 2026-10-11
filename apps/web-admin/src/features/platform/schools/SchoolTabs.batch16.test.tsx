// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SchoolModelHealthResponse, SchoolUsageResponse } from "../../../api/platformSchools";
import { SchoolActivityTab } from "./SchoolActivityTab";
import { SchoolModelsTab } from "./SchoolModelsTab";
import { SchoolUsageTab } from "./SchoolUsageTab";

const api = vi.hoisted(() => ({ health: vi.fn(), activity: vi.fn(), usage: vi.fn() }));
vi.mock("../../../api/platformSchools", () => ({
  getPlatformSchoolModelHealth: api.health, getPlatformSchoolActivity: api.activity, getPlatformSchoolUsage: api.usage,
}));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>,
  Button: ({ children, href }: { children: ReactNode; href: string }) => <a href={href}>{children}</a>,
  Spin: () => <span data-testid="loading" />,
  Empty: Object.assign(({ description }: { description: string }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
  Timeline: ({ items }: { items: Array<{ color: string; children: ReactNode }> }) => <div>{items.map((item, index) => <div key={index} data-color={item.color}>{item.children}</div>)}</div>,
  DatePicker: { RangePicker: () => null }, Segmented: () => null,
}));
vi.mock("../../../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ dataSource, columns }: { dataSource: Array<Record<string, unknown>>; columns: Array<{ dataIndex?: string; render?: (value: unknown) => ReactNode }> }) => (
    <table><tbody>{dataSource.map((row, rowIndex) => <tr key={rowIndex}>{columns.map((column, columnIndex) => {
      const value = row[column.dataIndex ?? ""];
      return <td key={columnIndex}>{column.render ? column.render(value) : String(value ?? "")}</td>;
    })}</tr>)}</tbody></table>
  ),
}));
vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  LineChart: () => <div data-testid="trend" />, CartesianGrid: () => null, Line: () => null, Tooltip: () => null, XAxis: () => null, YAxis: () => null,
}));

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("学校详情局部展示", () => {
  it.each([
    { status: "success", connection: "正常", capability: "通过" },
    { status: "failed", connection: "异常", capability: "未通过" },
    { status: "unknown", connection: "未检测", capability: "未检测" },
  ])("模型检测 $status 保留连接、能力与独立角色文案", async ({ status, connection, capability }) => {
    const response: SchoolModelHealthResponse = {
      default_model: { status: "healthy", model_name: "model", connection_status: status, capability_status: status },
      roles: [
        { agent_role: "primary_a", model_name: "主评模型", provider_key: "local", status: "healthy" },
        { agent_role: "primary_b", model_name: "停用模型", provider_key: "local", status: "disabled" },
        { agent_role: "arbiter", model_name: "待测模型", provider_key: "local", status: "unknown" },
      ],
    };
    api.health.mockResolvedValue(response);
    render(<SchoolModelsTab tenantId="tenant-16" />);
    await screen.findByRole("heading", { name: "日常对话默认模型" });
    expect(screen.getByText("API 连接").nextElementSibling?.textContent).toBe(connection);
    expect(screen.getByText("能力检测").nextElementSibling?.textContent).toBe(capability);
    expect(screen.getByText("主评模型").parentElement?.textContent).toContain("正常");
    expect(screen.getByText("停用模型").parentElement?.textContent).toContain("已停用");
    expect(screen.getByText("待测模型").parentElement?.textContent).toContain("需检测");
    expect(screen.getByRole("link", { name: "配置评分模型" }).getAttribute("href")).toContain("tenant_id=tenant-16&tab=bindings");
  });

  it("活动颜色保留 warning、success、其他优先级", async () => {
    api.activity.mockResolvedValue({ security: { active_admins: 1, mfa_enabled: 1, active_sessions: 1 },
      activities: ["warning", "success", "unknown"].map((severity) => ({
        id: severity, event_type: "test", severity, title: severity, summary: "说明", happened_at: "2026-10-05T00:00:00Z",
      })) });
    render(<SchoolActivityTab tenantId="tenant-16" />);
    await screen.findByText("warning");
    for (const [severity, color] of [["warning", "orange"], ["success", "green"], ["unknown", "blue"]]) {
      expect(screen.getByText(severity).closest("[data-color]")?.getAttribute("data-color")).toBe(color);
    }
  });

  it.each([false, true])("用量请求失败标记 %s 保留加载和结果分支", async (failed) => {
    let resolve: (response: SchoolUsageResponse) => void;
    let reject: (error: Error) => void;
    api.usage.mockReturnValue(new Promise<SchoolUsageResponse>((accept, refuse) => { resolve = accept; reject = refuse; }));
    render(<SchoolUsageTab tenantId="tenant-16" />);
    expect(screen.getByTestId("loading")).toBeTruthy();
    if (failed) {
      reject!(new Error("测试请求失败"));
      expect(await screen.findByRole("alert")).toHaveProperty("textContent", "AI 用量加载失败");
      expect(screen.queryByTestId("trend")).toBeNull();
    } else {
      resolve!({ summary: { input_tokens: 1, output_tokens: 2, cached_input_tokens: 0, reasoning_tokens: 0,
        total_tokens: 3, requests: 1, arbitration_requests: 0, estimated_cost_microusd: 0, window_days: 30 },
        trend: [], by_feature: [], by_model: [], start_date: "2026-09-05", end_date: "2026-10-05" });
      await screen.findByTestId("trend");
      expect(screen.getByText(/总 Token：3/)).toBeTruthy();
      expect(screen.getByText("本期预估成本（美元）")).toBeTruthy();
      expect(screen.getByText(/暂无此统计期间的学校总预算/)).toBeTruthy();
    }
    expect(screen.queryByTestId("loading")).toBeNull();
  });
});
