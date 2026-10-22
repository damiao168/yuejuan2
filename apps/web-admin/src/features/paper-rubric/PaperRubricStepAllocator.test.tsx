// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PaperRubricStepAllocator } from "./PaperRubricStepAllocator";

const lines = ["设 $x=1$", "代入得 $y=x+2$", "因此 $y=3$", "核对定义域成立"].map((content, index) => ({ id: `step-${index + 1}`, position: index + 1, content }));

beforeEach(() => {
  Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })) });
});
afterEach(cleanup);

function selectLine(position: number) {
  fireEvent.click(screen.getByRole("checkbox", { name: `选择第 ${position} 行` }));
}

function score(value: string, label = "本段分值") {
  fireEvent.change(screen.getByRole("spinbutton", { name: label }), { target: { value } });
}

function addGroup(start: number, end: number, value: string) {
  selectLine(start);
  if (start !== end) selectLine(end);
  score(value);
  fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
}

describe("PaperRubricStepAllocator", () => {
  it("点击结束采分段从首个未分配步骤连续选择，聚焦分值并在段尾展示已分配分值", () => {
    render(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "在第 2 行结束采分段" }));
    expect(screen.getByText("已选第 1—2 行")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("spinbutton", { name: "本段分值" }));
    score("4");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect(screen.getByText("采分点 1 · 4 分")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "在第 4 行结束采分段" }));
    expect(screen.getByText("已选第 3—4 行")).toBeTruthy();
    score("6");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect(screen.getByText("采分点 2 · 6 分")).toBeTruthy();
    expect((screen.getByRole("button", { name: "采用分段结果" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("结束采分段不能跨过已有分段，教师仍可使用首末勾选单独配后段", () => {
    render(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={vi.fn()} />);
    addGroup(2, 2, "3");
    fireEvent.click(screen.getByRole("button", { name: "在第 4 行结束采分段" }));
    expect(screen.getByText("请先完成前面的未分配区间，或使用首末勾选单独选择后面的连续步骤。")).toBeTruthy();
    expect(screen.getByText("请选择解答步骤")).toBeTruthy();
    addGroup(3, 4, "4");
    expect(screen.getByText("已分配 7 / 10 分")).toBeTruthy();
  });

  it("勾选首末行自动扩展连续区间，不允许取消中间行并留下不连续采分点", () => {
    const onApply = vi.fn();
    render(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={onApply} />);
    selectLine(1);
    selectLine(3);
    expect((screen.getByRole("checkbox", { name: "选择第 2 行" }) as HTMLInputElement).checked).toBe(true);
    selectLine(2);
    expect(screen.getByText("一个采分区间必须连续，请从首行或末行调整选择。")).toBeTruthy();
    expect((screen.getByRole("checkbox", { name: "选择第 2 行" }) as HTMLInputElement).checked).toBe(true);
    score("3");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect((screen.getByRole("checkbox", { name: "选择第 1 行" }) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "采用分段结果" }) as HTMLButtonElement).disabled).toBe(true);
    addGroup(4, 4, "7");
    fireEvent.click(screen.getByRole("button", { name: "采用分段结果" }));
    expect(onApply).toHaveBeenCalledTimes(1);
    const points = onApply.mock.calls[0][0];
    expect(points).toHaveLength(2);
    expect(points[0]).toEqual(expect.objectContaining({ description: `完成以下解答步骤：\n${lines.slice(0, 3).map((line) => line.content).join("\n")}`, score: 3, required: false }));
    expect(points[1]).toEqual(expect.objectContaining({ description: `完成以下解答步骤：\n${lines[3].content}`, score: 7, required: false }));
    expect(points[0].id).not.toBe(points[1].id);
  });

  it("首末行未被使用但中间已分配时拒绝跨越，删除后可以重新分段", () => {
    render(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={vi.fn()} />);
    addGroup(2, 2, "3");
    selectLine(1);
    selectLine(4);
    expect(screen.getByText("所选范围包含已分配的步骤，请选择不重叠的连续区间。")).toBeTruthy();
    expect((screen.getByRole("checkbox", { name: "选择第 4 行" }) as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "删除分段采分点 1" }));
    selectLine(4);
    expect((screen.getByRole("checkbox", { name: "选择第 2 行" }) as HTMLInputElement).checked).toBe(true);
    score("10");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect((screen.getByRole("button", { name: "采用分段结果" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("拒绝超出满分的新增区间，调分后重新核对合计并报告未应用分组", () => {
    const onApply = vi.fn();
    const onDirtyChange = vi.fn();
    render(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={onApply} onDirtyChange={onDirtyChange} />);
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    addGroup(1, 2, "6");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    addGroup(3, 4, "5");
    expect(screen.getByText("采分点合计不能超过本题满分，请调整分值。")).toBeTruthy();
    score("4");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect(screen.getByText("已分配 10 / 10 分")).toBeTruthy();
    score("3", "采分点 2 分值");
    expect(screen.getByText("剩余 1 分")).toBeTruthy();
    expect((screen.getByRole("button", { name: "采用分段结果" }) as HTMLButtonElement).disabled).toBe(true);
    score("4", "采分点 2 分值");
    fireEvent.click(screen.getByRole("button", { name: "采用分段结果" }));
    // 父页面可能仍在等待替换确认，点击采用不能先清空教师尚未接纳的分组。
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(screen.getByText("已分配 10 / 10 分")).toBeTruthy();
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "删除分段采分点 1" }));
    fireEvent.click(screen.getByRole("button", { name: "删除分段采分点 1" }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("解答版本变化使整段失效时清空旧分值并提示重新配分", () => {
    const onDirtyChange = vi.fn();
    const props = { maxScore: 10, onApply: vi.fn(), onDirtyChange };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    addGroup(1, 4, "10");
    view.rerender(<PaperRubricStepAllocator {...props} lines={[{ id: "step-1", position: 1, content: "新版解答" }]} />);
    expect(screen.getByText("已分配 0 / 10 分")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "删除分段采分点 1" })).toBeNull();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(screen.getByText("解答内容或分段边界已改变，已移除 1 个受影响采分段，请重新配分。")).toBeTruthy();
  });

  it("拆分只清除受影响采分段，保留后续连续原文对应的分值和ID", () => {
    const onApply = vi.fn();
    const props = { maxScore: 10, onApply };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    addGroup(1, 2, "4");
    addGroup(3, 4, "6");
    fireEvent.click(screen.getByRole("button", { name: "采用分段结果" }));
    const preservedID = onApply.mock.calls[0][0][1].id;
    const changed = [
      { id: "split-a", position: 1, content: "设" },
      { id: "split-b", position: 2, content: "$x=1$" },
      ...lines.slice(1).map((line, index) => ({ ...line, position: index + 3 }))
    ];
    view.rerender(<PaperRubricStepAllocator {...props} lines={changed} />);
    expect(screen.getByText("已分配 6 / 10 分")).toBeTruthy();
    expect(screen.getByText("解答内容或分段边界已改变，已移除 1 个受影响采分段，请重新配分。其他采分段已保留。")).toBeTruthy();
    expect(screen.getByText("采分点 1 · 6 分")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "在第 3 行结束采分段" }));
    score("4");
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    fireEvent.click(screen.getByRole("button", { name: "采用分段结果" }));
    expect(onApply.mock.calls[1][0][1]).toEqual(expect.objectContaining({ id: preservedID, score: 6 }));
  });

  it("相同原文行之间插入内容会使跨插入位置的区间失效，撤销插入也不会恢复旧配分", () => {
    const props = { maxScore: 10, onApply: vi.fn() };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    addGroup(1, 4, "10");
    const changed = [lines[0], { id: "inserted", position: 2, content: "新增说明" }, ...lines.slice(1).map((line) => ({ ...line, position: line.position + 1 }))];
    view.rerender(<PaperRubricStepAllocator {...props} lines={changed} />);
    expect(screen.getByText("已分配 0 / 10 分")).toBeTruthy();
    view.rerender(<PaperRubricStepAllocator {...props} lines={lines} />);
    expect(screen.getByText("已分配 0 / 10 分")).toBeTruthy();
    expect((screen.getByRole("button", { name: "采用分段结果" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("显示行号变化保留原文连续区间，满分改变则提示重新配分", () => {
    const props = { onApply: vi.fn() };
    const view = render(<PaperRubricStepAllocator {...props} maxScore={10} lines={lines} />);
    addGroup(1, 4, "10");
    view.rerender(<PaperRubricStepAllocator {...props} maxScore={10} lines={lines.map((line) => ({ ...line, position: line.position + 3 }))} />);
    expect(screen.getByText("已分配 10 / 10 分")).toBeTruthy();
    expect(screen.queryByText(/解答内容或分段边界已改变/)).toBeNull();
    view.rerender(<PaperRubricStepAllocator {...props} maxScore={12} lines={lines} />);
    expect(screen.getByText("已分配 0 / 12 分")).toBeTruthy();
    expect(screen.getByText("本题满分已改变，请重新核对并分配各段分值。")).toBeTruthy();
  });

  it("未添加的行选择与临时分值也报告未保存，清除选择同时清除临时分值", () => {
    const onDirtyChange = vi.fn();
    const draftStore = { current: null as unknown };
    const props = { maxScore: 10, onApply: vi.fn(), onDirtyChange, draftStore };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    selectLine(1);
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    score("2");
    view.rerender(<PaperRubricStepAllocator key="refresh-selection" {...props} lines={lines} />);
    expect(screen.getByText("已选第 1 行")).toBeTruthy();
    expect(Number((screen.getByRole("spinbutton", { name: "本段分值" }) as HTMLInputElement).value)).toBe(2);
    fireEvent.click(screen.getByRole("button", { name: "清除选择" }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    score("3");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "清除选择" }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("解答全被清除也显示原采分段失效的提示，并支持父级行编辑入口", () => {
    const props = { maxScore: 10, onApply: vi.fn(), renderLineActions: (line: typeof lines[number]) => <button type="button">拆分行 {line.position}</button> };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    expect(screen.getByRole("button", { name: "拆分行 1" })).toBeTruthy();
    addGroup(1, 4, "10");
    view.rerender(<PaperRubricStepAllocator {...props} lines={[]} />);
    expect(screen.getByText("解答内容或分段边界已改变，已移除 1 个受影响采分段，请重新配分。")).toBeTruthy();
    expect(screen.getByText("请先补充参考解答")).toBeTruthy();
  });

  it("同题刷新重挂后恢复尚未应用的分组，已改变的解答不能恢复旧划分", () => {
    const draftStore = { current: null as unknown };
    const onDirtyChange = vi.fn();
    const props = { maxScore: 10, onApply: vi.fn(), onDirtyChange, draftStore };
    const view = render(<PaperRubricStepAllocator {...props} lines={lines} />);
    addGroup(1, 4, "10");
    view.rerender(<PaperRubricStepAllocator key="refresh" {...props} lines={lines} />);
    expect(screen.getByText("已分配 10 / 10 分")).toBeTruthy();
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    view.rerender(<PaperRubricStepAllocator key="changed" {...props} lines={[{ id: "step-1", position: 1, content: "新版解答" }]} />);
    expect(screen.getByText("已分配 0 / 10 分")).toBeTruthy();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("缺少解答不编造内容，只读时所有编辑入口禁用", () => {
    const view = render(<PaperRubricStepAllocator lines={[]} maxScore={10} onApply={vi.fn()} />);
    expect(screen.getByText("请先补充参考解答")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "采用分段结果" })).toBeNull();
    view.rerender(<PaperRubricStepAllocator lines={lines} maxScore={10} onApply={vi.fn()} disabled />);
    expect(screen.getAllByRole("checkbox").every((item) => (item as HTMLInputElement).disabled)).toBe(true);
    expect((screen.getByRole("spinbutton", { name: "本段分值" }) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getAllByRole("button").every((item) => (item as HTMLButtonElement).disabled)).toBe(true);
  });
});
