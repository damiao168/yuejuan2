// @vitest-environment jsdom
import type { ComponentProps, InputHTMLAttributes, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SubjectiveGradingBatch } from "../api/subjectiveGrading";
import { SubjectiveGradingBatchPage } from "./SubjectiveGradingBatchPage";

const transport = vi.hoisted(() => ({ create: vi.fn(), recover: vi.fn(), get: vi.fn() }));
vi.mock("../api/subjectiveGrading", () => ({
  createSubjectiveGradingBatch: transport.create,
  recoverSubjectiveBatchCommand: transport.recover,
  getSubjectiveGradingBatch: transport.get,
  enqueueSubjectiveGradingBatch: vi.fn(),
  recoverSubjectiveEnqueueCommand: vi.fn()
}));
vi.mock("antd", () => {
  const Input = Object.assign(({ prefix, ...props }: InputHTMLAttributes<HTMLInputElement> & { prefix?: ReactNode }) =>
    <input {...props} aria-label={typeof prefix === "string" ? prefix : undefined} />, {
    TextArea: ({ autoSize: _autoSize, ...props }: ComponentProps<"textarea"> & { autoSize?: object }) => <textarea {...props} />
  });
  const Wrapper = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Input,
    Button: ({ children, loading, disabled, onClick }: { children?: ReactNode; loading?: boolean; disabled?: boolean; onClick?: () => void }) =>
      <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
    Alert: ({ message }: { message?: ReactNode }) => <div role="alert">{message}</div>,
    Card: ({ title, extra, children }: { title?: ReactNode; extra?: ReactNode; children?: ReactNode }) => <section>{title}{extra}{children}</section>,
    Space: Wrapper, Tag: Wrapper, Progress: () => null
  };
});

const scopeKey = "school-a:teacher-a";
const commandKey = `subjective-batch-command:${scopeKey}`;
const segments = "segment-a, segment-b";
const segmentInput = () => screen.getByPlaceholderText("输入答题片段 ID，使用换行或逗号分隔（最多 1000 个）") as HTMLTextAreaElement;
const commandInput = () => screen.getByRole("textbox", { name: "幂等键" }) as HTMLInputElement;
const accepted: SubjectiveGradingBatch = {
  id: "accepted-batch", tenant_id: "school-a", idempotency_key: "accepted-command", status: "completed",
  created_by: "teacher-a", created_at: "2026-10-15T00:00:00Z", updated_at: "2026-10-15T00:00:00Z",
  segment_ids: ["segment-a", "segment-b"], total_count: 2, queued_count: 0, processing_count: 0, succeeded_count: 2, failed_count: 0
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  transport.create.mockResolvedValue({ batch: accepted });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});

// 页面和命令帮助函数均运行真实实现；存储拒绝发生在网络前，不能丢掉输入或改用无耐久身份的请求。
it.each(["getter", "read", "write"] as const)("存储%s被拒绝时可挂载，创建失败保留同一草稿与命令键，恢复后可提交", async failure => {
  const unavailable = new DOMException("Browser storage access rejected", "SecurityError");
  const fault = failure === "getter"
    ? vi.spyOn(window, "localStorage", "get").mockImplementation(() => { throw unavailable; })
    : failure === "read"
      ? vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw unavailable; })
      : vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw unavailable; });
  render(<SubjectiveGradingBatchPage scopeKey={scopeKey} />);
  expect(screen.getByRole("heading", { name: "主观题 AI 批次" })).toBeTruthy();
  expect(transport.get).not.toHaveBeenCalled();
  fireEvent.change(segmentInput(), { target: { value: segments } });
  const originalCommand = commandInput().value;
  fireEvent.click(screen.getByRole("button", { name: "创建批次" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toBe("主观题批次操作失败");
  expect(segmentInput().value).toBe(segments);
  expect(commandInput().value).toBe(originalCommand);
  expect(screen.getByRole<HTMLButtonElement>("button", { name: "创建批次" }).disabled).toBe(false);
  expect(transport.create).not.toHaveBeenCalled();
  expect(transport.recover).not.toHaveBeenCalled();

  fault.mockRestore();
  fireEvent.click(screen.getByRole("button", { name: "创建批次" }));
  await waitFor(() => expect(transport.create).toHaveBeenCalledExactlyOnceWith(originalCommand, ["segment-a", "segment-b"]));
  await waitFor(() => expect(localStorage.getItem(`${commandKey}:batch`)).toBe(accepted.id));
  expect(localStorage.getItem(commandKey)).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByText("已完成")).toBeTruthy();
  expect(segmentInput().value).toBe(segments);
});

it("正常存储下先写入当前账号命令再发送，成功后保存批次且移除原命令", async () => {
  transport.create.mockImplementation(async (id: string, segmentIds: string[]) => {
    expect(JSON.parse(localStorage.getItem(commandKey)!)).toEqual({ commandId: id, segmentIds });
    expect(localStorage.getItem("subjective-batch-command:school-b:teacher-b")).toBeNull();
    return { batch: accepted };
  });
  render(<SubjectiveGradingBatchPage scopeKey={scopeKey} />);
  fireEvent.change(segmentInput(), { target: { value: segments } });
  const originalCommand = commandInput().value;
  fireEvent.click(screen.getByRole("button", { name: "创建批次" }));
  await waitFor(() => expect(transport.create).toHaveBeenCalledExactlyOnceWith(originalCommand, ["segment-a", "segment-b"]));
  await screen.findByText("已完成");
  expect(localStorage.getItem(commandKey)).toBeNull();
  expect(localStorage.getItem(`${commandKey}:batch`)).toBe(accepted.id);
  expect(transport.recover).not.toHaveBeenCalled();
});

it("在原批次进度内展示失败原因并保留未知用量，不增加模型调用", async () => {
  localStorage.setItem(`${commandKey}:batch`, accepted.id);
  transport.get.mockResolvedValue({ batch: {
    ...accepted, status: "failed", succeeded_count: 1, failed_count: 1,
    failure_summary: [{ failure_kind: "output_truncated", count: 1, provider_request_count: 1, usage_status: "partial" }]
  } });
  render(<SubjectiveGradingBatchPage scopeKey={scopeKey} />);
  await screen.findByLabelText("AI 失败诊断");
  expect(screen.getByText("输出达到上限 · 1 项")).toBeTruthy();
  expect(screen.getByText("尝试次数未知 · 上游调用 1 次 · 用量：部分报告，仍有未知用量")).toBeTruthy();
  expect(transport.create).not.toHaveBeenCalled();
});
