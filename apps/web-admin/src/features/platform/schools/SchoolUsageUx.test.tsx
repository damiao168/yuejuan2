// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SchoolUsageResponse } from "../../../api/platformSchools";
import { SchoolUsageTab } from "./SchoolUsageTab";

const api = vi.hoisted(() => ({ usage: vi.fn() }));
vi.mock("../../../api/platformSchools", () => ({ getPlatformSchoolUsage: api.usage }));
vi.mock("../../../components/ResponsiveTable", () => ({ ResponsiveTable: () => null }));
vi.mock("recharts", () => ({ CartesianGrid: () => null, Line: () => null, LineChart: () => null, ResponsiveContainer: () => null, Tooltip: () => null, XAxis: () => null, YAxis: () => null }));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <p>{message}</p>,
  Spin: () => <p>加载中</p>,
  Segmented: ({ value, onChange, options }: { value: string; onChange: (value: string) => void; options: { label: string; value: string }[] }) => <select aria-label="统计期间" value={value} onChange={(event) => onChange(event.target.value)}>{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>,
  DatePicker: { RangePicker: () => <p>日期选择</p> },
}));

function usage(cost: number): SchoolUsageResponse {
  return { summary: { total_tokens: 120, input_tokens: 80, output_tokens: 40, cached_input_tokens: 0, reasoning_tokens: 0, requests: 3, arbitration_requests: 0, window_days: 30, estimated_cost_microusd: cost }, trend: [], by_feature: [], by_model: [], start_date: "2026-10-01", end_date: "2026-10-08" };
}
beforeEach(() => { vi.clearAllMocks(); api.usage.mockResolvedValue(usage(1234567)); });
afterEach(cleanup);

describe("平台学校期间成本", () => {
  it("美元换算使用账本微美元，日期不齐时不把上一期间成本当作自定义期间", async () => {
    render(<SchoolUsageTab tenantId="school-a" />);
    await screen.findByText("US$1.234567");
    expect(screen.getByText("2026-10-01 — 2026-10-08")).toBeTruthy();
    expect(screen.getByText(/单题与整场考试上限不能作为本期用量比例的分母/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("统计期间"), { target: { value: "custom" } });
    expect(screen.queryByText("US$1.234567")).toBeNull();
    expect(screen.getByText(/请选择完整的开始与结束日期/)).toBeTruthy();
    expect(api.usage).toHaveBeenCalledTimes(1);
  });
  it("切校后前一学校的迟到成本不能覆盖当前学校，失败也不展示上一学校成本", async () => {
    let finishOld!: (result: SchoolUsageResponse) => void;
    api.usage.mockReturnValueOnce(new Promise<SchoolUsageResponse>((resolve) => { finishOld = resolve; }));
    const { rerender } = render(<SchoolUsageTab tenantId="school-a" />);
    rerender(<SchoolUsageTab tenantId="school-b" />);
    await screen.findByText("US$1.234567");
    await act(async () => finishOld(usage(9000000)));
    expect(screen.queryByText(/9\.00/)).toBeNull();
    api.usage.mockRejectedValueOnce(new Error("unavailable"));
    fireEvent.change(screen.getByLabelText("统计期间"), { target: { value: "7d" } });
    await screen.findByText("AI 用量加载失败");
    expect(screen.queryByText("US$1.234567")).toBeNull();
    expect(api.usage).toHaveBeenLastCalledWith("school-b", { window: "7d" });
  });
});
