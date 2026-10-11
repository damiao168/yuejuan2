// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StructuredAnswerEditor, StructuredListEditor } from "./StructuredTeacherFields";

describe("teacher structured fields preserve existing answer facts", () => {
  let root: Root; let host: HTMLDivElement; let latest: string;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  function Answer({ initial, type }: { initial: string; type?: string }) { const [value, setValue] = useState(initial); latest = value; return <StructuredAnswerEditor value={value} questionType={type} options={["甲", "乙", "丙", "丁"]} onChange={setValue} />; }
  function List({ initial }: { initial: string }) { const [value, setValue] = useState(initial); latest = value; return <StructuredListEditor value={value} itemLabel="扣分点" onChange={setValue} newItem={{ description: "", deduction: 0 }} />; }
  async function edit(label: string, text: string) {
    const element = host.querySelector<HTMLTextAreaElement | HTMLInputElement>(`[aria-label="${label}"]`)!;
    expect(element).not.toBeNull();
    await act(async () => { Object.getOwnPropertyDescriptor(element.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(element, text); element.dispatchEvent(new Event("input", { bubbles: true })); });
  }
  async function click(text: string) { const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""))!; expect(button).toBeDefined(); await act(async () => button.click()); }

  it("updates a known answer field without dropping identifiers or unknown nested structure", async () => {
    const original = { answer: "旧答案", id: "answer-v1", source_refs: [{ page: 2 }], legacy_extension: { nested: [1, { mode: "historical" }] } };
    await act(async () => root.render(<Answer initial={JSON.stringify(original)} />));
    await edit("答案", "新答案");
    expect(JSON.parse(latest)).toEqual({ ...original, answer: "新答案" });
    expect(host.textContent).toContain("历史补充信息"); expect(host.textContent).not.toContain("legacy_extension");
  });

  it("keeps non-JSON written answers as ordinary text", async () => {
    await act(async () => root.render(<Answer initial="x∈R；{这只是答案文字}" type="short_answer" />));
    await edit("标准答案", "{无需填写JSON}：因为 x＞0");
    expect(latest).toBe("{无需填写JSON}：因为 x＞0");
  });

  it("edits one blank while preserving all other blanks and supports adding a blank", async () => {
    await act(async () => root.render(<Answer initial='["第一空","第二空"]' type="fill_blank" />));
    await edit("标准答案 2", "第二空改后"); expect(JSON.parse(latest)).toEqual(["第一空", "第二空改后"]);
    await click("添加一项"); expect(JSON.parse(latest)).toEqual(["第一空", "第二空改后", ""]);
  });

  it("uses real multiple-choice selection and serializes every selected answer", async () => {
    await act(async () => root.render(<Answer initial='["A","C"]' type="multiple_choice" />));
    const input = host.querySelector<HTMLInputElement>('input[aria-label="标准答案选项"]')!;
    await act(async () => input.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    const option = [...document.querySelectorAll<HTMLElement>(".ant-select-item-option")].find((item) => item.getAttribute("title")?.startsWith("B"))!;
    expect(option).toBeDefined(); await act(async () => option.click());
    expect(JSON.parse(latest)).toEqual(["A", "C", "B"]);
  });

  it("edits known scoring fields while retaining unknown historic rules", async () => {
    const original = [{ id: "deduction-1", description: "旧扣分条件", deduction: 2, custom_constraint: { limit: 7 } }];
    await act(async () => root.render(<List initial={JSON.stringify(original)} />));
    await edit("说明", "漏写单位"); expect(JSON.parse(latest)).toEqual([{ ...original[0], description: "漏写单位" }]);
  });

  it("does not replace malformed historical lists with empty data", async () => {
    await act(async () => root.render(<List initial="历史非数组内容" />));
    expect(host.textContent).toContain("原数据已保留"); expect(latest).toBe("历史非数组内容");
    expect([...host.querySelectorAll("button")].some((button) => button.textContent?.includes("添加扣分点"))).toBe(false);
  });
});
