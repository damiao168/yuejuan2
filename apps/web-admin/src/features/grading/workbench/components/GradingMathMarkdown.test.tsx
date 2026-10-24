// @vitest-environment jsdom
import { render, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { GradingMathMarkdown } from "./GradingMathMarkdown";
import { normalizeGradingMathText } from "./gradingMathText";

afterEach(cleanup);

describe("阅卷识别与分析中的数学公式", () => {
  it("渲染截图中的裸下标及无花括号分数，同时保留中文说明与两行作答", () => {
    const source = String.raw`P=p_1p_2(1-p_3)+p_1p_3(1-p_2)+p_2p_3(1-p_1)+p_1p_2p_3=分数形式写作看似为1/6（字迹较潦草）
P=p_1p_2+p_1(1-p_2)+p_2(1-p_1)=\frac13\times\frac14+\frac13\times\frac34+\frac14\times\frac25=\frac12`;
    const { container } = render(<GradingMathMarkdown>{source}</GradingMathMarkdown>);
    expect(container.querySelectorAll(".katex").length).toBeGreaterThanOrEqual(3);
    expect(container.querySelectorAll(".mfrac").length).toBeGreaterThanOrEqual(7);
    expect(container.querySelectorAll(".msupsub").length).toBeGreaterThan(3);
    expect(container.querySelector(".katex-error")).toBeNull();
    expect(container.textContent).toContain("分数形式写作看似为");
    expect(container.textContent).toContain("（字迹较潦草）");
    expect(source).toContain(String.raw`\frac13`);
  });

  it("混排现有公式、裸公式和行内中文命令，不对已标记的公式重复包裹", () => {
    const source = String.raw`已知 $p_1=\frac{1}{3}$，计算 P=p_1p_2=\frac13\times\frac14；结论 \text{闭区间}[2,+\infty)。`;
    const { container } = render(<GradingMathMarkdown>{source}</GradingMathMarkdown>);
    expect(container.querySelectorAll(".katex")).toHaveLength(3);
    expect(container.querySelector(".katex-error")).toBeNull();
    expect(normalizeGradingMathText(source)).toContain(String.raw`$p_1=\frac{1}{3}$`);
    expect(container.textContent).toContain("闭区间");
  });

  it("代码、链接、普通说明和无法解析的原文保持可读，不修正识别答案", () => {
    const code = "`" + String.raw`p_1=\frac13` + "`";
    const source = ["使用模型：deepseek-v4.1-flash，得分 5 / 17。", `代码：${code}，链接：[文件](https://example.test/p_1?q=x_2)。`,
      String.raw`未知命令：\doesnotexist{1}，未闭合：$p_1=\frac13`].join("\n");
    const normalized = normalizeGradingMathText(source);
    expect(normalized).toContain("deepseek-v4.1-flash");
    expect(normalized).toContain(code);
    expect(normalized).toContain("[文件](https://example.test/p_1?q=x_2)");
    expect(normalized).toContain(String.raw`\doesnotexist{1}`);
    expect(normalized).toContain(String.raw`未闭合：$p_1=\frac13`);
    const { container } = render(<GradingMathMarkdown>{source}</GradingMathMarkdown>);
    expect(container.querySelector(".katex-error")).toBeNull();
  });

  it("评分依据的加粗表达式可渲染，阶乘保留，空值占位不变成Markdown列表", () => {
    const { container } = render(<GradingMathMarkdown>{String.raw`**依据：P=1/n!**，未提供标准答案：-`}</GradingMathMarkdown>);
    expect(container.querySelector("strong .katex")).not.toBeNull();
    expect(container.querySelector(".katex-error")).toBeNull();
    expect(container.querySelector("annotation")?.textContent).toContain("n!");
    cleanup();
    const placeholder = render(<GradingMathMarkdown>-</GradingMathMarkdown>);
    expect(placeholder.container.textContent).toBe("-");
    expect(placeholder.container.querySelector("ul")).toBeNull();
  });
});
