// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { StudentResult } from "../../api";
import { StudentSessionContext } from "../../studentSession";
import { makeQuestion, makeResult } from "../../test/studentAppBatch8Fixtures";
import { PaperViewer } from "./PaperViewer";

function viewer(result: StudentResult, onViewTextFeedback: (() => void) | undefined, submitting = false, isSubmitting?: () => boolean) {
  return <StudentSessionContext.Provider value={{ draftOwner: "student-1", submitting, isSubmitting, setSubmitting: vi.fn(), reauthenticate: vi.fn() }}>
    <PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} onViewTextFeedback={onViewTextFeedback} />
  </StudentSessionContext.Provider>;
}

describe("empty paper viewer navigation", () => {
  it.each([
    [true, true], [true, false], [false, true], [false, false]
  ])("offers text feedback only with questions=%s and navigation=%s", (hasQuestions, hasNavigation) => {
    const navigate = vi.fn();
    render(viewer(makeResult(hasQuestions ? [makeQuestion()] : []), hasNavigation ? navigate : undefined));
    const feedback = screen.queryByRole("button", { name: "查看题目文字反馈" });
    expect(Boolean(feedback)).toBe(hasQuestions && hasNavigation);
    expect(screen.getByText("暂无可查看的答卷图像。可先查看题目文字反馈；需要原卷时请联系学校考试负责人。")).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByRole("button", { name: "放大" })).toBeNull();
    if (feedback) {
      fireEvent.click(feedback);
      expect(navigate).toHaveBeenCalledOnce();
    }
  });

  it("checks the synchronous submit flag before navigation even before the disabled state renders", () => {
    const navigate = vi.fn();
    const isSubmitting = vi.fn().mockReturnValue(false);
    const result = makeResult();
    const view = render(viewer(result, navigate, false, isSubmitting));
    const feedback = () => screen.getByRole<HTMLButtonElement>("button", { name: "查看题目文字反馈" });
    fireEvent.click(feedback());
    expect(navigate).toHaveBeenCalledOnce();

    // 同步提交标记先变、React 禁用状态尚未更新，点击仍不能定位到另一个输入区。
    isSubmitting.mockReturnValue(true);
    expect(feedback().disabled).toBe(false);
    fireEvent.click(feedback());
    expect(navigate).toHaveBeenCalledOnce();
    expect(isSubmitting).toHaveBeenCalledTimes(2);

    isSubmitting.mockReturnValue(false);
    view.rerender(viewer(result, navigate, true, isSubmitting));
    expect(feedback().disabled).toBe(true);
    fireEvent.click(feedback());
    expect(navigate).toHaveBeenCalledOnce();
    expect(isSubmitting).toHaveBeenCalledTimes(2);

    view.rerender(viewer(result, navigate));
    fireEvent.click(feedback());
    expect(navigate).toHaveBeenCalledTimes(2);
  });
});
