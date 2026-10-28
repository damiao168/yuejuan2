// @vitest-environment jsdom
import type { ReactNode } from "react";
import type { AIHumanDisagreement } from "@edugrade/sdk";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DisagreementTaskSelector } from "./DisagreementTaskSelector";

const api = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock("../../api/scoringAssurance", () => ({ listDisagreementReviewTasks: api.list }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Alert: ({ message, action }: { message: string; action: ReactNode }) => <div role="alert">{message}{action}</div>,
  Select: ({ options, value, disabled, onChange, "aria-label": label }: { options: { value: string; label: string }[]; value: string; disabled: boolean; onChange: (id: string) => void; "aria-label": string }) => <select aria-label={label} value={value ?? ""} disabled={disabled} onChange={event => onChange(event.target.value)}><option value="" />{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));
const source = { id: "difference-a", exam_id: "exam-a", question_id: "question-a", submission_id: "submission-a", answer_segment_id: "segment-a" } as AIHumanDisagreement;
const task = { ...source, id: "task-a", tenant_id: "school-a", question_no: "3", anonymous_code: "anonymous-a", exam_name: "数学期中" };
afterEach(() => { cleanup(); vi.resetAllMocks(); });

describe("人机分歧的人工任务选择", () => {
  it("选择前给同题匿名号摘要，读取失败重试后必须重新核对选择", async () => {
    api.list.mockResolvedValueOnce({ tasks: [task], has_more: true, next_cursor: "page-2" }).mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({ tasks: [task], has_more: false });
    const selected = vi.fn();
    render(<DisagreementTaskSelector tenantID="school-a" disagreement={source} disabled={false} onSelect={selected} />);
    const input = screen.getByRole<HTMLSelectElement>("combobox", { name: "关联人工阅卷任务" });
    await waitFor(() => expect(input.disabled).toBe(false));
    expect(screen.getByRole("option", { name: /第3题.*anonymous-a/ })).toBeTruthy();
    fireEvent.change(input, { target: { value: "task-a" } });
    expect(selected).toHaveBeenLastCalledWith(task);
    expect(screen.getByRole("status").textContent).toContain("匿名号 anonymous-a");
    fireEvent.click(screen.getByRole("button", { name: "继续查找本题任务" }));
    await screen.findByRole("alert");
    expect(selected).toHaveBeenLastCalledWith(undefined);
    expect(input.disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "重试任务目录" }));
    await waitFor(() => expect(input.disabled).toBe(false));
    expect(input.value).toBe("");
  });
  it("切换学校和分歧后迟到任务不能进入新选择器", async () => {
    let resolve!: (value: unknown) => void;
    api.list.mockImplementationOnce(() => new Promise(done => { resolve = done; })).mockResolvedValueOnce({ tasks: [], has_more: false });
    const selected = vi.fn();
    const view = render(<DisagreementTaskSelector tenantID="school-a" disagreement={source} disabled={false} onSelect={selected} />);
    view.rerender(<DisagreementTaskSelector tenantID="school-b" disagreement={{ ...source, id: "difference-b" }} disabled={false} onSelect={selected} />);
    await act(async () => resolve({ tasks: [task], has_more: false }));
    expect(screen.queryByRole("option", { name: /anonymous-a/ })).toBeNull();
    expect(api.list).toHaveBeenCalledTimes(2);
    expect(selected.mock.calls.some(call => call[0]?.id === "task-a")).toBe(false);
  });
});
