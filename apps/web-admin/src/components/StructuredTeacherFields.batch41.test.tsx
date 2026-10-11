// @vitest-environment jsdom
import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StructuredAnswerEditor, StructuredListEditor } from "./StructuredTeacherFields";

describe("structured teacher fields batch41 controlled row identities", () => {
  let root: Root;
  let host: HTMLDivElement;
  let latest: string;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function Accepted({ initial, kind, type, newItem = "" }: Readonly<{
    initial: string; kind: string; type?: string; newItem?: unknown;
  }>) {
    const [value, setValue] = useState(initial);
    latest = value;
    if (kind === "list") return <StructuredListEditor value={value} itemLabel="项目" newItem={newItem} onChange={setValue} />;
    return <StructuredAnswerEditor value={value} questionType={type} options={["甲", "乙", "丙", "丁"]} onChange={setValue} />;
  }

  async function render(node: ReactNode) {
    await act(async () => root.render(node));
  }

  async function edit(element: HTMLTextAreaElement, text: string) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(element, text);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function click(text: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""));
    expect(button).toBeDefined();
    await act(async () => button!.click());
  }

  async function select(label: string, title: string) {
    const input = host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`);
    expect(input).not.toBeNull();
    await act(async () => input!.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    const option = [...document.querySelectorAll<HTMLElement>(".ant-select-item-option")].find(item => item.getAttribute("title") === title);
    expect(option).toBeDefined();
    await act(async () => option!.click());
  }

  it.each(["list", "answer"])("keeps duplicate %s row nodes after deleting the first and editing the survivor", async kind => {
    // 相同文本不能充当 identity；删首项后原第二、三项必须继续使用各自 DOM 节点。
    await render(<Accepted initial='["重复","重复","重复"]' kind={kind} type="fill_blank" />);
    const before = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    await click(kind === "list" ? "移除项目" : "移除第 1 项");
    const after = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    expect(after).toHaveLength(2);
    expect(after[0]).toBe(before[1]);
    expect(after[1]).toBe(before[2]);
    await edit(after[0], "改后");
    expect(host.querySelectorAll("textarea")[0]).toBe(before[1]);
    expect(JSON.parse(latest)).toEqual(["改后", "重复"]);
    expect(latest).not.toContain("teacher-row");
  });

  it.each(["list", "answer"])("does not display a rejected controlled %s deletion or edit", async kind => {
    const value = '["重复","重复","重复"]';
    const onChange = vi.fn();
    const node = kind === "list"
      ? <StructuredListEditor value={value} onChange={onChange} />
      : <StructuredAnswerEditor value={value} questionType="fill_blank" onChange={onChange} />;
    await render(node);
    const before = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    await click(kind === "list" ? "移除项目" : "移除第 1 项");
    expect(host.querySelectorAll("textarea")).toHaveLength(3);
    before.forEach((node, index) => expect(host.querySelectorAll("textarea")[index]).toBe(node));
    expect(JSON.parse(onChange.mock.calls[0][0])).toEqual(["重复", "重复"]);
    await edit(before[1], "不接纳");
    before.forEach((node, index) => expect(host.querySelectorAll("textarea")[index]).toBe(node));
    expect(before[1].value).toBe("重复");
    expect(JSON.parse(onChange.mock.calls[1][0])).toEqual(["重复", "不接纳", "重复"]);
  });

  it("keeps external controlled replacement keyed by unique hidden business identities", async () => {
    const original = [
      { id: "first", description: "第一项", extension: { keep: 1 } },
      { id: "second", description: "第二项", extension: { keep: 2 } },
    ];
    const onChange = vi.fn();
    await render(<StructuredListEditor value={JSON.stringify(original)} onChange={onChange} />);
    const before = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    const replacement = [{ ...original[1], description: "外部更新第二项" }, original[0]];
    await render(<StructuredListEditor value={JSON.stringify(replacement)} onChange={onChange} />);
    const after = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    expect(after[0]).toBe(before[1]);
    expect(after[1]).toBe(before[0]);
    expect(after[0].value).toBe("外部更新第二项");
    expect(host.textContent).not.toContain("second");
    await edit(after[0], "本地建议");
    expect(JSON.parse(onChange.mock.calls[0][0])).toEqual([{ ...replacement[0], description: "本地建议" }, original[0]]);
    expect(after[0].value).toBe("外部更新第二项");
  });

  it.each([
    { initial: '["A","C"]', type: "single_choice", label: "标准答案选项", title: "B．乙", expected: '["B"]', displayed: "A．甲" },
    { initial: "A， C", type: "multiple_choice", label: "标准答案选项", title: "B．乙", expected: '["A","C","B"]', displayed: "C．丙" },
    { initial: "Z", type: "single_choice", label: "标准答案选项", title: "B．乙", expected: "B", displayed: "Z" },
    { initial: "0", type: "single_choice", label: "标准答案选项", title: "B．乙", expected: "B", displayed: "0" },
    { initial: "false", type: "true_false", label: "判断题标准答案", title: "正确（√）", expected: "true", displayed: "错误（×）" },
    { initial: "正确", type: "true_false", label: "判断题标准答案", title: "错误（×）", expected: "false", displayed: "正确（√）" },
  ])("preserves $type serialization for $initial", async ({ initial, type, label, title, expected, displayed }) => {
    await render(<Accepted initial={initial} kind="answer" type={type} />);
    expect(host.textContent).toContain(displayed);
    await select(label, title);
    expect(latest).toBe(expected);
  });

  it("keeps the check-type selector ahead of the deep-history guard and respects disabled controls", async () => {
    let value: unknown = { type: "concept", content: "深层原文" };
    for (let depth = 0; depth < 8; depth++) value = { children: value };
    const onChange = vi.fn();
    await render(<StructuredAnswerEditor value={JSON.stringify(value)} disabled questionType="single_choice" onChange={onChange} />);
    // 对象优先于题型，且 depth=9 的核对方式仍显示选择器；其他深层值只保留原数据。
    expect(host.querySelector('input[aria-label="标准答案选项"]')).toBeNull();
    expect(host.querySelector<HTMLInputElement>('input[aria-label="核对方式"]')?.disabled).toBe(true);
    expect(host.textContent).toContain("复杂历史数据");
    expect(onChange).not.toHaveBeenCalled();
    await render(<StructuredListEditor value='["原值"]' disabled onChange={onChange} />);
    await click("移除项目");
    await click("添加项目");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("原值");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("adds independent structured items without persisting UI keys or changing the item template", async () => {
    const newItem = { description: "新项", extension: { retained: true } };
    const clone = vi.spyOn(globalThis, "structuredClone");
    await render(<Accepted initial="[]" kind="list" newItem={newItem} />);
    await click("添加项目");
    await click("添加项目");
    expect(clone).toHaveBeenCalledTimes(2);
    expect(clone.mock.results[0].value).not.toBe(clone.mock.results[1].value);
    const nodes = [...host.querySelectorAll<HTMLTextAreaElement>("textarea")];
    await edit(nodes[0], "只改首项");
    expect(JSON.parse(latest)).toEqual([{ ...newItem, description: "只改首项" }, newItem]);
    expect(newItem.description).toBe("新项");
    expect(host.querySelectorAll("textarea")[1]).toBe(nodes[1]);
    clone.mockRestore();
  });
});
