// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { formatCriterionValue } from "../../calibration/calibrationPresentation";
import { formatAnswer } from "./gradingWorkbench.model";
import { DraftConflictModal } from "./components/DraftConflictModal";
import type { DraftFallbackSnapshot } from "./gradingWorkbench.types";

vi.mock("antd", () => {
  const Wrapper = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  return {
    Modal: ({ open, children, onOk }: { open: boolean; children: ReactNode; onOk: () => void }) => open ? <div>{children}<button onClick={onOk}>应用</button></div> : null,
    Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
    Space: Wrapper,
    Radio: Object.assign(Wrapper, { Group: Wrapper }),
  };
});

afterEach(cleanup);

describe("平台值格式化保留既有含义", () => {
  it.each([
    [0, "0"], [false, "false"], [true, "true"], ["文本", "文本"],
    [null, "未选择"], [undefined, "未选择"], ["", "未选择"],
    [{ selected: false, score: 0 }, '{"selected":false,"score":0}'],
    [[0, false], "[0,false]"],
  ])("采分点值 %j", (value, expected) => {
    expect(formatCriterionValue(value)).toBe(expected);
  });

  it.each([
    [0, "0"], [false, "错误"], [true, "正确"], ["答案", "答案"],
    [null, "-"], [undefined, "-"], ["", "-"],
    [{ answer: 0 }, "0"], [{ answers: [false, null, "乙"] }, "错误、乙"],
    [{ first: "甲", second: [0, true] }, "甲、0、正确"], [[], "-"],
  ])("答案值 %j", (value, expected) => {
    expect(formatAnswer(value)).toBe(expected);
  });

  it("保留既有非 JSON 原始值转换，不把大整数交给 JSON 编码", () => {
    const symbol = Symbol("标识");
    const callback = () => "value";
    for (const value of [123n, symbol, callback]) {
      expect(formatCriterionValue(value)).toBe(String(value));
      expect(formatAnswer(value)).toBe(String(value));
    }
  });
});

function snapshot(score: number | null, comments: string, fit: boolean): DraftFallbackSnapshot {
  return {
    draft: { score, comments, studentFeedback: "", privateNote: "", reason: "", disputeReason: "", answerText: "", rubricSelections: { criterion: score ?? 0 } },
    viewer: { mode: "segment", scale: 1, rotation: 0, offset: { x: 0, y: 0 }, fit },
  };
}

describe("冲突字段展示不改默认合并事实", () => {
  it("明确展示零分、空字段与结构化查看状态，默认仍保留本机", () => {
    const local = snapshot(0, "", false);
    const server = snapshot(2, "教师评语", true);
    const onApply = vi.fn();
    render(<DraftConflictModal conflict={{ taskId: "task", serverRevision: 2, local, server }} onCancel={vi.fn()} onApply={onApply} />);

    const score = within(screen.getByLabelText("最终得分冲突"));
    expect(score.getByText("0", { selector: "pre" }).textContent).toBe("0");
    const comments = within(screen.getByLabelText("教师评语冲突"));
    expect(comments.getByText("（空）", { selector: "pre" }).textContent).toBe("（空）");
    const viewer = within(screen.getByLabelText("图像查看位置冲突"));
    const values = viewer.getAllByText(/"fit":/, { selector: "pre" });
    expect(values.map((node) => node.textContent)).toEqual([JSON.stringify(local.viewer, null, 2), JSON.stringify(server.viewer, null, 2)]);
    fireEvent.click(screen.getByRole("button", { name: "应用" }));
    expect(onApply).toHaveBeenCalledWith(local);
    expect(local.viewer.fit).toBe(false);
    expect(server.viewer.fit).toBe(true);
  });

  it("相同持久化字段不生成虚假冲突，也不改原草稿", () => {
    const local = snapshot(0, "", false);
    const onApply = vi.fn();
    render(<DraftConflictModal conflict={{ taskId: "task", serverRevision: 2, local, server: structuredClone(local) }} onCancel={vi.fn()} onApply={onApply} />);
    expect(screen.getByText("本机与服务端可保存字段已经一致，可以直接应用并恢复自动保存。")).toBeTruthy();
    expect(screen.queryByLabelText("最终得分冲突")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "应用" }));
    expect(onApply).toHaveBeenCalledWith(local);
  });
});
