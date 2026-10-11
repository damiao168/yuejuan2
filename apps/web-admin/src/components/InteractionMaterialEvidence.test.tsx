// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RegradeCandidateEvidence } from "../features/score-management/components/RegradeCandidateEvidence";
import { AnswerGroupSampleEvidence } from "../features/answer-groups/AnswerGroupSampleEvidence";
import { ProcessingExceptionEvidence } from "./ProcessingExceptionEvidence";
import { TaskScoringReference } from "./TaskScoringReference";

const api = vi.hoisted(() => ({ regrade: vi.fn(), regradeImage: vi.fn(), member: vi.fn(), memberImage: vi.fn(), exception: vi.fn(), exceptionImage: vi.fn() }));
vi.mock("../api/regrade", () => ({ getRegradeReviewContext: api.regrade, downloadRegradeReviewImage: api.regradeImage }));
vi.mock("../api/answerGroups", () => ({ getAnswerGroupMemberContext: api.member, getAnswerGroupMemberImage: api.memberImage }));
vi.mock("../api/processing", () => ({ getProcessingExceptionContext: api.exception, getProcessingExceptionImage: api.exceptionImage }));
vi.mock("./MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <div>{children}</div> }));
vi.mock("antd", () => ({ Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <aside role="alert">{message}{action}</aside>, Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>, Spin: () => <p>读取中</p> }));
const question = { id: "q", question_no: "8", question_type: "short_answer", score: 6, stem: "解释此结论", options: ["A. 条件成立", "B. 条件不成立"], answer_key: { id: "key", question_id: "q", answer_version: "v1", standard_answer: { text: "参考答案" }, equivalent_answers: [{ text: "等价答案" }], tolerance: {} }, solution: { raw_text: "解析正文", steps: [{ text: "推导步骤" }] } };
const rubric = { version: "frozen-3", points: [{ id: "p", description: "完整评分点", score: 6, condition: "必要条件" }], deductions: [{ rule: "重复计算扣分" }], examples: [{ answer: "评分样例正文" }] };
function review(revision = 3) { return { regrade_review_context: { item: { id: "item", revision, submission_id: "sub", old_score: 2, candidate_score: 4, max_score: 6 }, context: { expected_revision: revision, question, frozen_rubric: rubric, answer: { raw_answer: "实际答题内容" } } } }; }
function member(hash = "hash", answer = "实际组样本") { return { member_context: { group_id: "group", member: { segment_id: "segment", representation_hash: hash }, expected_revision: 0, question: { ...question, rubric }, answer_text: answer } }; }
describe("管理材料与候选对象绑定", () => {
  beforeEach(() => { vi.resetAllMocks(); vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:evidence"), revokeObjectURL: vi.fn() })); api.regradeImage.mockRejectedValue(new Error("图片不可读")); api.memberImage.mockRejectedValue(new Error("图片不可读")); api.exceptionImage.mockResolvedValue({ blob: new Blob(["image"]) }); });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
  it("展示完整冻结题目、等价答案、步骤、条件和扣分样例", () => {
    render(<TaskScoringReference question={question} rubric={rubric} />);
    for (const text of ["满分 6", "解释此结论", "A. 条件成立", "参考答案", "等价答案", "推导步骤", "必要条件", "重复计算扣分", "评分样例正文", "frozen-3"]) expect(document.body.textContent).toContain(text);
  });
  it("候选修订号不匹配时拒绝解锁，重试同一候选后可核对实际材料", async () => {
    api.regrade.mockResolvedValueOnce(review(2)).mockResolvedValueOnce(review()); const ready = vi.fn();
    render(<RegradeCandidateEvidence id="item" revision={3} onReady={ready} />);
    fireEvent.click(screen.getByText("查看答卷与冻结标准"));
    await screen.findByText(/候选已更新/); expect(ready).not.toHaveBeenCalled(); expect(api.regradeImage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("重试材料")); await screen.findByText("实际答题内容"); expect(ready).toHaveBeenCalledWith(3); await screen.findByText("图片不可读");
  });
  it("候选材料未返回时用原生状态输出播报，仍保持段落布局且不提前解锁", () => {
    api.regrade.mockImplementation(() => new Promise(() => undefined));
    const ready = vi.fn();
    render(<RegradeCandidateEvidence id="item" revision={3} onReady={ready} />);
    fireEvent.click(screen.getByText("查看答卷与冻结标准"));
    const status = screen.getByRole("status");
    expect(status.tagName).toBe("OUTPUT");
    expect(status.parentElement?.tagName).toBe("P");
    expect(status.textContent).toBe("正在读取本条候选材料…");
    expect(status.getAttribute("aria-live")).toBe("polite");
    expect(status.getAttribute("aria-atomic")).toBe("true");
    expect(ready).not.toHaveBeenCalled();
    expect(api.regradeImage).not.toHaveBeenCalled();
  });
  it("只有无文字样本的图片成功显示后才登记已读取，hash变化重新核查", async () => {
    api.member.mockResolvedValue(member("hash", "")); api.memberImage.mockResolvedValue({ blob: new Blob(["image"]) }); const read = vi.fn();
    const view = render(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={read} />);
    fireEvent.click(screen.getByText("查看实际答题样本")); const image = await screen.findByAltText("分组样本答题图"); expect(read).not.toHaveBeenCalled();
    fireEvent.load(image); expect(read).toHaveBeenCalledOnce();
    view.rerender(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="new-hash" onRead={read} />); await screen.findByText(/样本已变化/); expect(read).toHaveBeenCalledOnce();
  });
  it("异常上下文必须匹配考试，正确材料能打开本批次本页处理入口", async () => {
    const value = { exception: { id: "ex", exam_id: "wrong", page_id: "page", code: "BLOCKED_LOW_IMAGE_QUALITY" }, submission_id: "sub", candidate_no: "1008", page_no: 2, capture_batch_id: "batch", capture_page_id: "capture-page" };
    api.exception.mockResolvedValueOnce({ exception_context: value }).mockResolvedValueOnce({ exception_context: { ...value, exception: { ...value.exception, exam_id: "exam" } } }); const navigate = vi.fn();
    render(<ProcessingExceptionEvidence id="ex" examId="exam" onNavigate={navigate} />); await screen.findByText(/异常页面已变化/); expect(api.exceptionImage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("重试页面材料")); await screen.findByText(/考号：1008/); fireEvent.click(screen.getByText("打开本页处理工作区")); expect(navigate).toHaveBeenCalledWith("/exams/exam/capture?batch=batch&submission=sub&page=capture-page&tab=image-quality");
  });
});
