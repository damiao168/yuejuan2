import { render } from "@testing-library/react";
import katex from "katex";
import { afterEach, describe, expect, it, vi } from "vitest";
import { splitStudentText, StudentText } from "./StudentText";

afterEach(() => vi.restoreAllMocks());

describe("学生公开公式文本的分隔与回退", () => {
  it("四种分隔符保留展示模式、原文位置和中间普通文本", () => {
    expect(splitStudentText(String.raw`a$x$b$$y$$c\[z\]d\(w\)e`)).toEqual([
      { text: "a", sourceStart: 0 },
      { text: "x", sourceStart: 1, formula: true, display: false },
      { text: "b", sourceStart: 4 },
      { text: "y", sourceStart: 5, formula: true, display: true },
      { text: "c", sourceStart: 10 },
      { text: "z", sourceStart: 11, formula: true, display: true },
      { text: "d", sourceStart: 16 },
      { text: "w", sourceStart: 17, formula: true, display: false },
      { text: "e", sourceStart: 22 },
    ]);
  });

  it.each(["$x", String.raw`\[x`, String.raw`\(x`, "$x\ny$", "\\(x\ny\\)"])("未闭合或跨行行内公式保留原文：%s", (text) => {
    expect(splitStudentText(text)).toEqual([{ text, sourceStart: 0 }]);
  });

  it("转义开头仍是文本，转义结尾不提前结束公式", () => {
    expect(splitStudentText(String.raw`\$plain`)).toEqual([{ text: String.raw`\$plain`, sourceStart: 0 }]);
    expect(splitStudentText(String.raw`$x\$y$`)).toEqual([
      { text: String.raw`x\$y`, sourceStart: 0, formula: true, display: false },
    ]);
  });

  it("偶数个反斜线不把分隔符当作转义", () => {
    expect(splitStudentText(String.raw`\\$x$`)).toEqual([
      { text: String.raw`\\`, sourceStart: 0 },
      { text: "x", sourceStart: 2, formula: true, display: false },
    ]);
  });

  it("块公式允许换行，不完整前缀不吞掉后面的独立公式", () => {
    expect(splitStudentText("$$x\ny$$")).toEqual([{ text: "x\ny", sourceStart: 0, formula: true, display: true }]);
    expect(splitStudentText("$bad\nplain $x$")).toEqual([
      { text: "$bad\nplain ", sourceStart: 0 },
      { text: "x", sourceStart: 11, formula: true, display: false },
    ]);
  });

  it("空输入、空块公式和重复公式保留各自位置", () => {
    expect(splitStudentText("")).toEqual([]);
    expect(splitStudentText("$$$$")).toEqual([{ text: "", sourceStart: 0, formula: true, display: true }]);
    expect(splitStudentText("$x$$x$").map((part) => part.sourceStart)).toEqual([0, 3]);
  });

  it("真实 KaTeX 渲染行内及块公式，普通 HTML 保持文本", () => {
    const renderFormula = vi.spyOn(katex, "renderToString");
    const { container } = render(<StudentText>{"求 $x^2$ 的值\n\\[\\frac{1}{2}\\]\n<img src=x onerror=alert(1)>"}</StudentText>);
    expect(container.querySelectorAll(".katex")).toHaveLength(2);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(renderFormula.mock.calls.map(([, options]) => options?.displayMode)).toEqual([false, true]);
    for (const [, options] of renderFormula.mock.calls) {
      expect(options).toMatchObject({ trust: false, throwOnError: false, maxExpand: 1000, maxSize: 10, output: "htmlAndMathml", strict: "warn" });
    }
  });

  it("渲染器意外抛错只让当前公式回退为源文本", () => {
    vi.spyOn(katex, "renderToString").mockImplementation(() => { throw new Error("测试渲染故障"); });
    const { container } = render(<StudentText>{"前$x$后"}</StudentText>);
    expect(container.querySelector("code")?.textContent).toBe("x");
    expect(container.textContent).toBe("前x后");
  });
});
