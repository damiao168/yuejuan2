// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ProcessingException } from "../api/processing";
import { ProcessingOperationsPanel } from "./ProcessingOperationsPanel";

const mocks = vi.hoisted(() => ({ summary: vi.fn(), list: vi.fn(), actors: vi.fn() }));
vi.mock("../api/processing", () => ({ getProcessingSummary: mocks.summary, listProcessingExceptions: mocks.list, assignProcessingException: vi.fn(), resolveProcessingException: vi.fn(), retryProcessingException: vi.fn() }));
vi.mock("../api/workflowActors", () => ({ listProcessingActors: mocks.actors }));
vi.mock("./ProcessingExceptionEvidence", () => ({ ProcessingExceptionEvidence: () => null }));
vi.mock("./PageState", () => ({ EmptyState: () => null }));
vi.mock("./StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("./ResponsiveTable", () => ({ ResponsiveTable: ({ columns, dataSource }: { columns: Array<{ key?: string; dataIndex?: string; render?: (value: unknown, row: ProcessingException) => ReactNode }>; dataSource: ProcessingException[] }) => <div>{dataSource.map(row => <article key={row.id}>{columns.map((column, index) => <div key={column.key ?? column.dataIndex ?? index}>{column.render?.(column.dataIndex ? row[column.dataIndex as keyof ProcessingException] : undefined, row)}</div>)}</article>)}</div> }));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return { App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }) },
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <div>{message}{description}</div>, Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
    Descriptions: Object.assign(passthrough, { Item: passthrough }), Input: { TextArea: () => null }, Modal: () => null, Select: () => null, Space: passthrough };
});
afterEach(cleanup);
beforeEach(() => { vi.resetAllMocks(); mocks.summary.mockResolvedValue({ summary: { total_pages: 1, ready_pages: 0, pending_pages: 0, blocked_pages: 1, by_stage: [] } }); });
const exception: ProcessingException = { id: "exception", exam_id: "exam", page_id: "raw-page-uuid", source_type: "quality", source_id: "quality", code: "BLOCKED_LOW_IMAGE_QUALITY", severity: "P1", blocking: true, status: "assigned", assigned_to: "raw-user-uuid", details: {}, created_at: "2026-10-09", updated_at: "2026-10-09" };

describe("异常列表使用受限业务摘要", () => {
  it("只读用户直接看到同校处理人、考号与页号，不请求人员目录", async () => {
    mocks.list.mockResolvedValue({ exceptions: [{ ...exception, assigned_to_name: "张老师", assigned_to_username: "zhang", candidate_no: "00123", page_no: 2 }] });
    render(<ProcessingOperationsPanel examId="exam" canManage={false} />);
    expect(await screen.findByText("张老师（zhang）")).toBeTruthy();
    expect(screen.getByText("考号 00123 · 第 2 页")).toBeTruthy();
    expect(document.body.textContent).not.toContain("raw-user-uuid"); expect(document.body.textContent).not.toContain("raw-page-uuid");
    expect(mocks.actors).not.toHaveBeenCalled();
  });
  it("历史缺资料仍区别已指派与未指派，不用UUID冒充姓名", async () => {
    mocks.list.mockResolvedValue({ exceptions: [exception, { ...exception, id: "unassigned", assigned_to: "" }] });
    render(<ProcessingOperationsPanel examId="exam" canManage={false} />);
    expect(await screen.findByText("已指派，人员资料暂不可用")).toBeTruthy();
    expect(screen.getByText("未指派")).toBeTruthy(); expect(screen.getAllByText(/页号待核对/)).toHaveLength(2);
    expect(mocks.actors).not.toHaveBeenCalled();
  });
  it("处理人摘要去除空白并按姓名、用户名、缺资料的顺序回退", async () => {
    mocks.list.mockResolvedValue({ exceptions: [
      { ...exception, id: "full", assigned_to_name: " 张老师 ", assigned_to_username: " zhang " },
      { ...exception, id: "name", assigned_to_name: " 李老师 ", assigned_to_username: " " },
      { ...exception, id: "username", assigned_to_name: " ", assigned_to_username: " wang " },
      { ...exception, id: "unassigned", assigned_to: "", assigned_to_name: "过期姓名" }
    ] });
    render(<ProcessingOperationsPanel examId="exam" canManage={false} />);
    expect(await screen.findByText("张老师（zhang）")).toBeTruthy();
    expect(screen.getByText("李老师")).toBeTruthy();
    expect(screen.getByText("wang")).toBeTruthy();
    expect(screen.getByText("未指派")).toBeTruthy();
    expect(document.body.textContent).not.toContain("过期姓名");
    expect(mocks.actors).not.toHaveBeenCalled();
  });
});
