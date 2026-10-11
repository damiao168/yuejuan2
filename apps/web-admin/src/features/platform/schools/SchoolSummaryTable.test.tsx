// @vitest-environment jsdom
import { act, createElement, type MouseEventHandler } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlatformSchoolSummary } from "../../../api/platformSchools";
import { SchoolSummaryTable } from "./SchoolSummaryTable";

vi.mock("antd", () => ({ App: { useApp: () => ({ modal: { confirm: vi.fn() } }) } }));
vi.mock("../../../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ dataSource, onRow }: {
    dataSource: PlatformSchoolSummary[];
    onRow: (record: PlatformSchoolSummary) => { onClick: MouseEventHandler<HTMLElement> };
  }) => {
    const rowProps = onRow(dataSource[0]);
    return createElement("div", null,
      createElement("button", { ...rowProps, type: "button", "data-action": "mobile" }, createElement("svg", null)),
      createElement("table", null, createElement("tbody", null, createElement("tr", rowProps,
        createElement("td", null, "School"),
        createElement("td", null, createElement("button", { type: "button", "data-action": "nested" }, "Nested")),
      ))),
    );
  },
}));

const school: PlatformSchoolSummary = {
  tenant_id: "tenant-test", school_id: "school-test", name: "School", code: "TEST", status: "active", created_at: "2026-10-01T00:00:00Z",
  administrator: { admin_count: 1 },
  members: { accounts: 1, active_accounts: 1, administrators: 1, teachers: 0, graders: 0, students: 0, classes: 0 },
  usage: { input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, reasoning_tokens: 0, total_tokens: 0, requests: 0, arbitration_requests: 0, estimated_cost_microusd: 0, window_days: 1 },
  model_health: { status: "unconfigured" }, exam_count: 0, attention_reasons: [],
};

describe("school summary row-action boundary", () => {
  let host: HTMLDivElement;
  let root: Root;
  const onOpen = vi.fn();

  beforeEach(async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    onOpen.mockClear();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    await act(async () => root.render(<SchoolSummaryTable schools={[school]} loading={false} usageWindow="today" onOpen={onOpen} onChangeStatus={vi.fn()} />));
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  it.each(["button[data-action=mobile]", "button[data-action=mobile] svg", "tr > td:first-child"])("opens overview through %s", async (selector) => {
    await act(async () => host.querySelector(selector)!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(school, "overview");
  });

  it("does not open the row when an independent table control is clicked", async () => {
    // 只测试调用方过滤边界；此表格替身不代表真实 AntD 布局或读屏验证。
    await act(async () => host.querySelector("button[data-action=nested]")!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(onOpen).not.toHaveBeenCalled();
  });
});
