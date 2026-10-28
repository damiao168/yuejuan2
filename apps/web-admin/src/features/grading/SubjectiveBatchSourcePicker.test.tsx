// @vitest-environment jsdom
import { act } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SubjectiveBatchSourcePicker } from "./SubjectiveBatchSourcePicker";

const api = vi.hoisted(() => ({ exams: vi.fn(), tasks: vi.fn() }));
vi.mock("../../api/exams", () => ({ listAllExams: api.exams }));
vi.mock("../../api/subjectiveGrading", () => ({ listSubjectiveGradingSources: api.tasks }));
vi.mock("antd", () => ({ Alert: ({ message }: { message: string }) => <aside>{message}</aside>, Button: ({ children, onClick }: { children: string; onClick: () => void }) => <button onClick={onClick}>{children}</button> }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it("授权目录按考试读取、同一作答片段去重，选择提交真实片段而非显示名称", async () => {
  api.exams.mockResolvedValue({ exams: [{ id: "exam", name: "合成考试" }] });
  api.tasks.mockResolvedValue({ sources: [{ answer_segment_id: "segment", question_id: "question", question_no: "2", submission_label: "匿名A", ready: true }, { answer_segment_id: "segment", question_id: "question", question_no: "2", submission_label: "匿名A", ready: true }], has_more: false });
  const select = vi.fn(); render(<SubjectiveBatchSourcePicker disabled={false} onSelect={select} />);
  await screen.findByText("合成考试");
  await act(async () => fireEvent.change(screen.getByLabelText("影子评分考试"), { target: { value: "exam" } }));
  expect(api.tasks).toHaveBeenCalledWith("exam", undefined);
  expect(screen.getAllByRole("checkbox")).toHaveLength(1);
  fireEvent.click(screen.getByRole("checkbox")); expect(select).toHaveBeenLastCalledWith(["segment"]);
});

it("切考试后迟到的旧目录不能覆盖当前考试答卷", async () => {
  let finish!: (value: unknown) => void;
  api.exams.mockResolvedValue({ exams: [{ id: "A", name: "合成A" }, { id: "B", name: "合成B" }] });
  api.tasks.mockReturnValueOnce(new Promise(resolve => { finish = resolve; })).mockResolvedValue({ sources: [{ answer_segment_id: "B", question_id: "qB", question_no: "B", submission_label: "匿名B", ready: true }], has_more: false });
  render(<SubjectiveBatchSourcePicker disabled={false} onSelect={vi.fn()} />); await screen.findByText("合成A");
  await act(async () => fireEvent.change(screen.getByLabelText("影子评分考试"), { target: { value: "A" } }));
  await act(async () => fireEvent.change(screen.getByLabelText("影子评分考试"), { target: { value: "B" } }));
  await act(async () => finish({ sources: [{ answer_segment_id: "A", question_id: "qA", question_no: "A", submission_label: "匿名A", ready: true }], has_more: false }));
  expect(screen.getByText("第 B 题 · 匿名B")).toBeTruthy(); expect(screen.queryByText("第 A 题 · 匿名A")).toBeNull();
});
it("尚未生成阅卷任务的完整材料可选，缺作答或冻结标准的材料不允许加入", async () => {
  api.exams.mockResolvedValue({ exams: [{ id: "exam", name: "合成考试" }] });
  api.tasks.mockResolvedValue({ sources: [{ answer_segment_id: "ready", question_id: "q", question_no: "1", submission_label: "可选材料", ready: true }, { answer_segment_id: "missing", question_id: "q", question_no: "1", submission_label: "尚未齐全", ready: false, unavailable_reason: "尚无已登记作答" }], has_more: false });
  render(<SubjectiveBatchSourcePicker disabled={false} onSelect={vi.fn()} />); await screen.findByText("合成考试");
  await act(async () => fireEvent.change(screen.getByLabelText("影子评分考试"), { target: { value: "exam" } }));
  expect((screen.getAllByRole("checkbox")[0] as HTMLInputElement).disabled).toBe(false);
  expect((screen.getAllByRole("checkbox")[1] as HTMLInputElement).disabled).toBe(true);
});
