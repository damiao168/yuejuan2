// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExamReadiness, ReadinessCheck } from "../../api/configuration";
import { PaperQuestionConfirmation } from "./PaperQuestionConfirmation";

const api = vi.hoisted(() => ({ getExamReadiness: vi.fn() }));
vi.mock("../../api/configuration", () => api);
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));
vi.mock("antd", () => ({
  Alert: ({ type, message, description, action }: { type: string; message: ReactNode; description: ReactNode; action: ReactNode }) => <aside data-tone={type}><h3>{message}</h3><div>{description}</div>{action}</aside>,
  Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
}));

const questionCodes = ["questions", "total_score", "question_scores", "answer_keys", "rubrics"];
function readiness(checks?: ReadinessCheck[]): ExamReadiness {
  return { ready: false, confirmed: false, configuration_hash: "hash-current", import_snapshot_available: false,
    checks: checks ?? questionCodes.map((code) => ({ code, label: `${code} 检查`, message: `${code} 已通过`, passed: true, section: "questions", severity: "blocker" })) };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("question preparation confirmation", () => {
  const onConfirmed = vi.fn();
  const onChecked = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    api.getExamReadiness.mockResolvedValue({ readiness: readiness() });
  });
  afterEach(cleanup);
  function mount(overrides: Partial<Parameters<typeof PaperQuestionConfirmation>[0]> = {}) {
    return render(<PaperQuestionConfirmation examId="exam-1" configurationKey="version-1" canManage disabled={false} onChecked={onChecked} onConfirmed={onConfirmed} {...overrides} />);
  }
  function click() { fireEvent.click(screen.getByRole("button", { name: "检查并确认小题与分值" })); }

  it("reads only on an explicit click and confirms this step independently of overall readiness", async () => {
    mount();
    expect(api.getExamReadiness).not.toHaveBeenCalled();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值检查已通过")).toBeDefined());
    expect(api.getExamReadiness).toHaveBeenCalledExactlyOnceWith("exam-1");
    expect(onConfirmed).toHaveBeenCalledOnce();
    expect(onChecked).toHaveBeenCalledOnce();
    expect(screen.getByRole("region", { name: "小题与分值检查" }).getAttribute("data-tone")).toBe("success");
  });

  it("lists specific persisted configuration problems without confirming the step", async () => {
    const result = readiness();
    result.checks[1] = { ...result.checks[1], passed: false, label: "题目总分", message: "当前 90 分，考试满分 150 分" };
    result.checks[4] = { ...result.checks[4], passed: false, label: "主观题评分细则", message: "第 16 题评分细则尚未锁定" };
    api.getExamReadiness.mockResolvedValue({ readiness: result });
    mount();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值尚未通过检查")).toBeDefined());
    expect(screen.getByRole("region", { name: "小题与分值检查" }).textContent).toContain("题目总分：当前 90 分，考试满分 150 分");
    expect(screen.getByRole("region", { name: "小题与分值检查" }).textContent).toContain("主观题评分细则：第 16 题评分细则尚未锁定");
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(onChecked).toHaveBeenCalledOnce();
  });

  it("refreshes navigation for an incomplete check response without creating completion", async () => {
    api.getExamReadiness.mockResolvedValue({ readiness: readiness([]) });
    mount({ onConfirmed: undefined });
    click();
    await waitFor(() => expect(onChecked).toHaveBeenCalledOnce());
    expect(screen.getByText("检查结果不完整，请重新检查；当前结果不能确认本步完成。")).toBeDefined();
    expect(onConfirmed).not.toHaveBeenCalled();
  });

  it.each(questionCodes)("does not confirm when required check %s is missing", async (missing) => {
    const result = readiness();
    result.checks = result.checks.filter((item) => item.code !== missing);
    api.getExamReadiness.mockResolvedValue({ readiness: result });
    mount();
    click();
    await waitFor(() => expect(screen.getByText("检查结果不完整，请重新检查；当前结果不能确认本步完成。")).toBeDefined());
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(screen.queryByText("小题与分值检查已通过")).toBeNull();
  });

  it("includes material structure failures in the question step checks", async () => {
    const result = readiness();
    result.checks.push({ code: "material_structure", section: "questions", passed: false, severity: "blocker", label: "试卷资料与题目结构", message: "请采用完整资料的题目结构" });
    api.getExamReadiness.mockResolvedValue({ readiness: result });
    mount();
    click();
    await waitFor(() => expect(screen.getByText("试卷资料与题目结构")).toBeDefined());
    expect(onConfirmed).not.toHaveBeenCalled();
  });

  it("allows a failed read to be retried without writing configuration", async () => {
    api.getExamReadiness.mockRejectedValueOnce(new Error("read failed"));
    mount();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值检查失败")).toBeDefined());
    expect(screen.getByText("read failed")).toBeDefined();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(onChecked).not.toHaveBeenCalled();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值检查已通过")).toBeDefined());
    expect(api.getExamReadiness).toHaveBeenCalledTimes(2);
    expect(onConfirmed).toHaveBeenCalledOnce();
    expect(onChecked).toHaveBeenCalledOnce();
  });

  it("uses a synchronous request guard for repeated clicks", async () => {
    const request = deferred<{ readiness: ExamReadiness }>();
    api.getExamReadiness.mockReturnValue(request.promise);
    mount();
    click();
    click();
    expect(api.getExamReadiness).toHaveBeenCalledOnce();
    await act(async () => request.resolve({ readiness: readiness() }));
    expect(onConfirmed).toHaveBeenCalledOnce();
  });

  it.each([{ canManage: false }, { disabled: true }, { examId: "" }])("prevents confirmation when permissions or saved configuration are not ready", (props) => {
    mount(props);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "检查并确认小题与分值" }).disabled).toBe(true);
    click();
    expect(api.getExamReadiness).not.toHaveBeenCalled();
    expect(onConfirmed).not.toHaveBeenCalled();
  });

  it("shows a supplied blocking reason and invalidates a passed check as soon as unsaved work appears", async () => {
    const view = mount();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值检查已通过")).toBeDefined());
    view.rerender(<PaperQuestionConfirmation examId="exam-1" configurationKey="version-1" canManage disabled disabledReason="请先重新读取保存结果，再检查确认。" onConfirmed={onConfirmed} />);
    expect(screen.queryByText("小题与分值检查已通过")).toBeNull();
    expect(screen.getByText("请先重新读取保存结果，再检查确认。")).toBeDefined();
    view.rerender(<PaperQuestionConfirmation examId="exam-1" configurationKey="version-1" canManage disabled={false} onConfirmed={onConfirmed} />);
    expect(screen.queryByText("小题与分值检查已通过")).toBeNull();
  });

  it("drops a saved-configuration result after the configuration key changes", async () => {
    const view = mount();
    click();
    await waitFor(() => expect(screen.getByText("小题与分值检查已通过")).toBeDefined());
    view.rerender(<PaperQuestionConfirmation examId="exam-1" configurationKey="version-2" canManage disabled={false} onConfirmed={onConfirmed} />);
    expect(screen.queryByText("小题与分值检查已通过")).toBeNull();
  });

  it.each(["configuration", "disabled", "permission", "exam_round_trip"])("isolates late responses after %s changes", async (change) => {
    const request = deferred<{ readiness: ExamReadiness }>();
    api.getExamReadiness.mockReturnValue(request.promise);
    const view = mount();
    click();
    const props = { examId: "exam-1", configurationKey: "version-1", canManage: true, disabled: false, onChecked, onConfirmed };
    if (change === "configuration") view.rerender(<PaperQuestionConfirmation {...props} configurationKey="version-2" />);
    if (change === "disabled") view.rerender(<PaperQuestionConfirmation {...props} disabled />);
    if (change === "permission") view.rerender(<PaperQuestionConfirmation {...props} canManage={false} />);
    if (change === "exam_round_trip") {
      view.rerender(<PaperQuestionConfirmation {...props} examId="exam-2" />);
      view.rerender(<PaperQuestionConfirmation {...props} />);
    }
    await act(async () => request.resolve({ readiness: readiness() }));
    expect(screen.queryByText("小题与分值检查已通过")).toBeNull();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(onChecked).not.toHaveBeenCalled();
  });

  it("does not refresh the parent or set a result after unmount", async () => {
    const request = deferred<{ readiness: ExamReadiness }>();
    api.getExamReadiness.mockReturnValue(request.promise);
    const view = mount();
    click();
    view.unmount();
    await act(async () => request.resolve({ readiness: readiness() }));
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(onChecked).not.toHaveBeenCalled();
  });
});
