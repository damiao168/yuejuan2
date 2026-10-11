// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerGroupMemberContext } from "../../api/answerGroups";
import { AnswerGroupSampleEvidence } from "./AnswerGroupSampleEvidence";

const mocks = vi.hoisted(() => ({ context: vi.fn(), image: vi.fn(), createUrl: vi.fn(), revokeUrl: vi.fn() }));
vi.mock("../../api/answerGroups", () => ({ getAnswerGroupMemberContext: mocks.context, getAnswerGroupMemberImage: mocks.image }));
vi.mock("../../components/TaskScoringReference", () => ({ TaskScoringReference: () => <span>冻结题目材料</span> }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <p>{children}</p> }));
vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
  Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <div>{message}{action}</div>
}));

beforeEach(() => {
  mocks.createUrl.mockReturnValue("blob:sample-image");
  vi.stubGlobal("URL", { createObjectURL: mocks.createUrl, revokeObjectURL: mocks.revokeUrl });
});
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.unstubAllGlobals(); });

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((nextResolve) => { resolve = nextResolve; });
  return { promise, resolve };
}

function material(answerText = "实际答案"): AnswerGroupMemberContext {
  return { group_id: "group", expected_revision: 1, answer_text: answerText, segment_image_url: "",
    member: { submission_id: "submission", segment_id: "segment", similarity: 0.9, outlier_score: 0.1,
      representative: true, boundary: false, outlier: false, representation_hash: "hash" },
    question: { id: "question", exam_id: "exam", question_no: "1", question_type: "short_answer", score: 2 } };
}

describe("真实样本材料的异步登记", () => {
  it("退出样本后迟到的文字材料不登记已读，也不继续请求图片", async () => {
    const slowContext = deferred<{ member_context: AnswerGroupMemberContext }>();
    let signal: AbortSignal | undefined;
    mocks.context.mockImplementation((_groupId: string, _segmentId: string, nextSignal: AbortSignal) => {
      signal = nextSignal;
      return slowContext.promise;
    });
    const onRead = vi.fn();
    const view = render(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={onRead} />);
    fireEvent.click(screen.getByRole("button", { name: "查看实际答题样本" }));
    expect(mocks.context).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => { slowContext.resolve({ member_context: material() }); await slowContext.promise; });
    expect(onRead).not.toHaveBeenCalled();
    expect(mocks.image).not.toHaveBeenCalled();
  });

  it("退出样本后迟到的图片不创建对象URL", async () => {
    const slowImage = deferred<{ blob: Blob }>();
    mocks.context.mockResolvedValue({ member_context: material("") });
    mocks.image.mockReturnValue(slowImage.promise);
    const onRead = vi.fn();
    const view = render(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={onRead} />);
    fireEvent.click(screen.getByRole("button", { name: "查看实际答题样本" }));
    await waitFor(() => expect(mocks.image).toHaveBeenCalledTimes(1));
    view.unmount();
    await act(async () => { slowImage.resolve({ blob: new Blob(["image"]) }); await slowImage.promise; });
    expect(onRead).not.toHaveBeenCalled();
    expect(mocks.createUrl).not.toHaveBeenCalled();
  });

  it("无文字样本必须等图片成功显示后才能登记已读", async () => {
    const sample = material("");
    mocks.context.mockResolvedValue({ member_context: sample });
    mocks.image.mockResolvedValue({ blob: new Blob(["image"]) });
    const onRead = vi.fn();
    const view = render(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={onRead} />);
    fireEvent.click(screen.getByRole("button", { name: "查看实际答题样本" }));
    const picture = await screen.findByRole("img", { name: "分组样本答题图" });
    expect(onRead).not.toHaveBeenCalled();
    fireEvent.load(picture);
    expect(onRead).toHaveBeenCalledWith(sample);
    view.unmount();
    expect(mocks.revokeUrl).toHaveBeenCalledWith("blob:sample-image");
  });

  it("父组件更换登记回调时沿用请求，并只调用最新回调", async () => {
    const slowContext = deferred<{ member_context: AnswerGroupMemberContext }>();
    mocks.context.mockReturnValue(slowContext.promise);
    mocks.image.mockResolvedValue({ blob: new Blob(["image"]) });
    const oldRead = vi.fn();
    const currentRead = vi.fn();
    const view = render(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={oldRead} />);
    fireEvent.click(screen.getByRole("button", { name: "查看实际答题样本" }));
    view.rerender(<AnswerGroupSampleEvidence groupId="group" segmentId="segment" hash="hash" onRead={currentRead} />);
    await act(async () => { slowContext.resolve({ member_context: material() }); await slowContext.promise; });
    expect(mocks.context).toHaveBeenCalledTimes(1);
    expect(oldRead).not.toHaveBeenCalled();
    expect(currentRead).toHaveBeenCalledWith(material());
  });
});
