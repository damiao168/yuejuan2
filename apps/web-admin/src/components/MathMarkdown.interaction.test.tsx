// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MathMarkdown } from "./MathMarkdown";

const first = "第一条说明[^note]。\n\n[^note]: 第一条注释。";
const second = "第二条说明[^note]。\n\n[^note]: 第二条注释。";
const table = "| 学科 | 人数 |\n| --- | --- |\n| 数学 | 30 |";

describe("markdown renderer identity and footnote scope", () => {
  let root: Root;
  let container: HTMLDivElement;
  let oldHash: string;
  let scrollDescriptor: PropertyDescriptor | undefined;
  let scrollByDescriptor: PropertyDescriptor | undefined;
  let scrollBy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    oldHash = window.location.hash;
    window.location.hash = "#/admin/dashboard";
    vi.stubGlobal("CSS", { escape: (value: string) => value });
    scrollDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
    scrollByDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollBy");
    // jsdom 不执行布局与原生滚动；记录浏览器接口调用并更新位置，不能据此声称已验证真实视口。
    scrollBy = vi.fn(function (this: HTMLElement, options: ScrollToOptions) {
      this.scrollLeft += options.left ?? 0;
    });
    Object.defineProperty(HTMLElement.prototype, "scrollBy", { configurable: true, value: scrollBy });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
    window.location.hash = oldHash;
    if (scrollDescriptor) Object.defineProperty(HTMLElement.prototype, "scrollIntoView", scrollDescriptor);
    else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
    if (scrollByDescriptor) Object.defineProperty(HTMLElement.prototype, "scrollBy", scrollByDescriptor);
    else Reflect.deleteProperty(HTMLElement.prototype, "scrollBy");
    vi.unstubAllGlobals();
  });

  it("resolves equal footnote names within each reply without changing the application route", async () => {
    await act(async () => { root.render(<><MathMarkdown>{first}</MathMarkdown><MathMarkdown>{second}</MathMarkdown></>); });
    const replies = container.querySelectorAll<HTMLElement>(".math-markdown");
    const references = container.querySelectorAll<HTMLAnchorElement>("a[data-footnote-ref]");
    expect(references).toHaveLength(2);
    expect(references[0].getAttribute("href")).not.toBe(references[1].getAttribute("href"));
    for (let index = 0; index < references.length; index += 1) {
      const id = references[index].getAttribute("href")!.slice(1);
      const target = document.getElementById(id)!;
      expect(replies[index].contains(target)).toBe(true);
      references[index].click();
      expect(document.activeElement).toBe(target);
      expect(window.location.hash).toBe("#/admin/dashboard");
    }
  });

  it("keeps renderer DOM identity and footnote ids when streamed prose is appended", async () => {
    await act(async () => { root.render(<MathMarkdown>{first}</MathMarkdown>); });
    const reference = container.querySelector("a[data-footnote-ref]");
    const heading = container.querySelector(".footnotes h2");
    const href = reference?.getAttribute("href");
    await act(async () => { root.render(<MathMarkdown>{`${first}\n\n后续内容。`}</MathMarkdown>); });
    expect(container.querySelector("a[data-footnote-ref]")).toBe(reference);
    expect(container.querySelector(".footnotes h2")).toBe(heading);
    expect(reference?.getAttribute("href")).toBe(href);
  });

  it("keeps table semantics in a named native section with real scroll buttons", async () => {
    await act(async () => { root.render(<MathMarkdown>{table}</MathMarkdown>); });
    const section = container.querySelector<HTMLElement>("section.math-markdown-table-region");
    expect(section?.getAttribute("aria-label")).toBe("表格，可横向滚动");
    expect(section?.hasAttribute("tabindex")).toBe(false);
    expect(section?.hasAttribute("role")).toBe(false);
    const buttons = section!.querySelectorAll<HTMLButtonElement>("button");
    expect(buttons).toHaveLength(2);
    const viewport = section!.querySelector<HTMLElement>(".math-markdown-table-scroll")!;
    for (const button of buttons) {
      expect(button.type).toBe("button");
      expect(button.getAttribute("title")).toBe(button.getAttribute("aria-label"));
      expect(document.getElementById(button.getAttribute("aria-controls")!)).toBe(viewport);
      button.focus();
      expect(document.activeElement).toBe(button);
      expect(button.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
    }
    expect(section?.querySelector("table")).not.toBeNull();
    expect(section?.querySelector("thead th")?.textContent).toBe("学科");
    expect(section?.querySelector("tbody td")?.textContent).toBe("数学");
  });

  it("scrolls only its own viewport in both directions without submitting an enclosing form", async () => {
    const submit = vi.fn();
    await act(async () => { root.render(<form onSubmit={submit}><MathMarkdown>{table}</MathMarkdown><MathMarkdown>{table}</MathMarkdown></form>); });
    const viewports = container.querySelectorAll<HTMLElement>(".math-markdown-table-scroll");
    Object.defineProperty(viewports[0], "clientWidth", { configurable: true, value: 400 });
    const left = container.querySelector<HTMLButtonElement>('[aria-label="向左滚动表格"]')!;
    const right = container.querySelector<HTMLButtonElement>('[aria-label="向右滚动表格"]')!;
    right.focus();
    right.click();
    expect(scrollBy).toHaveBeenLastCalledWith({ left: 300, behavior: "auto" });
    expect(viewports[0].scrollLeft).toBe(300);
    expect(viewports[1].scrollLeft).toBe(0);
    expect(document.activeElement).toBe(right);
    left.click();
    expect(scrollBy).toHaveBeenLastCalledWith({ left: -300, behavior: "auto" });
    expect(viewports[0].scrollLeft).toBe(0);
    expect(submit).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("#/admin/dashboard");
  });

  it("scrolls with either horizontal arrow on the focused button and leaves other keys alone", async () => {
    await act(async () => { root.render(<MathMarkdown>{table}</MathMarkdown>); });
    const viewport = container.querySelector<HTMLElement>(".math-markdown-table-scroll")!;
    Object.defineProperty(viewport, "clientWidth", { configurable: true, value: 40 });
    const left = container.querySelector<HTMLButtonElement>('[aria-label="向左滚动表格"]')!;
    left.focus();
    for (const [key, position] of [["ArrowRight", 80], ["ArrowLeft", 0]] as const) {
      const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
      left.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(true);
      expect(viewport.scrollLeft).toBe(position);
      expect(document.activeElement).toBe(left);
    }
    for (const options of [{ key: "Tab" }, { key: "ArrowUp" }, { key: "ArrowLeft", altKey: true }, { key: "ArrowRight", ctrlKey: true }, { key: "ArrowRight", metaKey: true }]) {
      const event = new KeyboardEvent("keydown", { ...options, bubbles: true, cancelable: true });
      left.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(scrollBy).toHaveBeenCalledTimes(2);
  });

  it.each(["Enter", " "])("does not override native %j activation or duplicate its click", async (key) => {
    await act(async () => { root.render(<MathMarkdown>{table}</MathMarkdown>); });
    const right = container.querySelector<HTMLButtonElement>('[aria-label="向右滚动表格"]')!;
    right.focus();
    const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
    right.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(scrollBy).not.toHaveBeenCalled();
    // 原生 Enter/Space 生成 click 的步骤由浏览器执行；这里仅验证处理器不会再手工滚动一次。
    right.click();
    expect(scrollBy).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(right);
  });

  it("retains table viewport, buttons, focus and scroll position while rows stream in", async () => {
    await act(async () => { root.render(<MathMarkdown>{`${table}\n\n${first}`}</MathMarkdown>); });
    const viewport = container.querySelector<HTMLElement>(".math-markdown-table-scroll")!;
    const button = container.querySelector<HTMLButtonElement>('[aria-label="向右滚动表格"]')!;
    const reference = container.querySelector("a[data-footnote-ref]");
    const controls = button.getAttribute("aria-controls");
    viewport.scrollLeft = 120;
    button.focus();
    await act(async () => { root.render(<MathMarkdown>{`${table}\n| 语文 | 28 |\n\n${first}\n\n后续内容。`}</MathMarkdown>); });
    expect(container.querySelector(".math-markdown-table-scroll")).toBe(viewport);
    expect(container.querySelector('[aria-label="向右滚动表格"]')).toBe(button);
    expect(button.getAttribute("aria-controls")).toBe(controls);
    expect(document.activeElement).toBe(button);
    expect(viewport.scrollLeft).toBe(120);
    expect(viewport.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(container.querySelector("a[data-footnote-ref]")).toBe(reference);
  });

  it("keeps table footnotes reply-scoped and model links or images inert", async () => {
    const withReferences = "| 内容 |\n| --- |\n| 说明[^note] 和 [外链](https://example.test/) 与 ![图片](https://example.test/image.png) |\n\n[^note]: 表格注释。";
    await act(async () => { root.render(<><MathMarkdown>{withReferences}</MathMarkdown><MathMarkdown>{withReferences}</MathMarkdown></>); });
    const replies = container.querySelectorAll<HTMLElement>(".math-markdown");
    const references = container.querySelectorAll<HTMLAnchorElement>("a[data-footnote-ref]");
    expect(references).toHaveLength(2);
    for (let index = 0; index < references.length; index += 1) {
      references[index].click();
      const target = document.getElementById(references[index].getAttribute("href")!.slice(1))!;
      expect(replies[index].contains(target)).toBe(true);
      expect(document.activeElement).toBe(target);
    }
    expect(container.querySelector("a[href^='https:']")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelectorAll(".math-markdown-link")).toHaveLength(2);
    expect(container.querySelectorAll(".math-markdown-image-label")).toHaveLength(2);
    expect(window.location.hash).toBe("#/admin/dashboard");
  });

  it("copies the visible reference address with feedback and keeps unsafe schemes inert", async () => {
    const copy = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: copy } });
    await act(async () => root.render(<MathMarkdown>{"[参考资料](https://example.test/source) 与 [危险地址](javascript:alert(1))"}</MathMarkdown>));
    const buttons = [...container.querySelectorAll<HTMLButtonElement>("button")].filter(button => button.textContent === "复制参考地址");
    expect(buttons).toHaveLength(1);
    const status = within(buttons[0].closest<HTMLElement>(".math-markdown-link")!).getByRole("status");
    expect(status.tagName).toBe("OUTPUT");
    expect(status.getAttribute("aria-live")).toBe("polite");
    expect(status.getAttribute("aria-atomic")).toBe("true");
    await act(async () => buttons[0].click());
    expect(copy).toHaveBeenCalledWith("https://example.test/source"); expect(container.textContent).toContain("参考地址已复制"); expect(window.location.hash).toBe("#/admin/dashboard");
    expect(status.textContent).toBe("参考地址已复制");
    copy.mockRejectedValueOnce(new Error("denied"));
    await act(async () => buttons[0].click());
    expect(container.textContent).toContain("复制失败");
    expect(status.textContent).toBe("复制失败，请手动复制下方地址");
  });
});
