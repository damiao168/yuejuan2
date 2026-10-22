// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { useObjectiveGrid } from "../editor/useObjectiveGrid";
import { ObjectiveGridDrawer } from "./ObjectiveGridDrawer";

vi.mock("./ObjectiveGridCanvas", () => ({ ObjectiveGridCanvas: () => <div /> }));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <div role="alert">{message}{description}</div>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>,
  Checkbox: ({ children, disabled, checked }: { children: ReactNode; disabled?: boolean; checked?: boolean }) => <label><input type="checkbox" disabled={disabled} checked={checked} readOnly />{children}</label>,
  Drawer: ({ open, title, extra, children }: { open: boolean; title: ReactNode; extra: ReactNode; children: ReactNode }) => open ? <div>{title}{extra}{children}</div> : null,
  InputNumber: () => <input type="number" />,
  Select: ({ "aria-label": label, disabled, options = [] }: { "aria-label"?: string; disabled?: boolean; options?: Array<{ value: string; label: string }> }) => <select aria-label={label} disabled={disabled}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>
}));

describe("objective grid teacher guidance", () => {
  let root: Root; let host: HTMLDivElement;
  const analyze = vi.fn(); const recognizeNumberHints = vi.fn();
  function draft() {
    const questionIds = Array.from({ length: 8 }, (_, index) => `q${index + 1}`);
    return { open: true, busy: false, ocrBusy: false, applying: false, reviewed: true, partialReviewed: false,
      partialDetection: true, validationError: undefined, block: { x: .1, y: .1, width: .8, height: .8 },
      optionCount: 4, orientation: "horizontal", reverse: false, readOrder: "column_first", questionIds,
      groups: Array.from({ length: 6 }, (_, index) => ({ group_id: `g${index}`, options: Array.from({ length: 4 }, () => ({})) })),
      assignments: Object.fromEntries([1, 3, 4, 6, 7, 8].map((number, index) => [`g${index}`, `q${number}`])),
      choiceQuestions: questionIds.map((id, index) => ({ id, question_no: `${index + 1}` })),
      analysis: { analysis: { request_id: "request" } }, analyze, recognizeNumberHints,
      closeGrid: vi.fn(), apply: vi.fn()
    } as unknown as ReturnType<typeof useObjectiveGrid>;
  }
  async function mount(grid = draft()) {
    await act(async () => root.render(<ObjectiveGridDrawer grid={grid} imageURL="blob:page" pageNo={1} readonly={false} />));
  }
  const button = (label: string) => [...host.querySelectorAll("button")].find(element => element.textContent === label)!;
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("explains the separate location and number actions without suggesting OCR repairs missed options", async () => {
    await mount();
    expect(host.textContent).toContain("不会补格或自动关联");
    await act(async () => button("定位填涂格").click());
    expect(analyze).toHaveBeenCalledOnce(); expect(recognizeNumberHints).not.toHaveBeenCalled();
    await act(async () => button("识别题号（参考）").click());
    expect(recognizeNumberHints).toHaveBeenCalledOnce();
  });

  it("shows the eight versus six mismatch and the two unassigned questions before allowing partial apply", async () => {
    const grid = draft(); await mount(grid);
    expect(host.textContent).toContain("本区已选 8 题，定位到 6 组");
    expect(host.textContent).toContain("少定位到 2 题，不能按顺序关联题号");
    expect(host.textContent).toContain("尚未关联：第 2 题、第 5 题");
    expect(button("按所选题号与阅读顺序关联").disabled).toBe(true);
    expect(button("应用到草稿").disabled).toBe(true);
    // 教师可以明确只保存已核对的 6 题，但不能由“已核对”一次勾选掩盖未定位的第 2、5 题。
    await mount({ ...grid, partialReviewed: true });
    expect(button("应用到草稿").disabled).toBe(false);
    expect(button("按所选题号与阅读顺序关联").disabled).toBe(true);
  });
});
