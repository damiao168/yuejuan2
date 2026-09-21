import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MathMarkdown, normalizeMathMarkdown, requiresDisplayMath } from "./MathMarkdown";

describe("math markdown normalization", () => {
  it("accepts both model-friendly dollar delimiters and legacy latex delimiters", () => {
    expect(normalizeMathMarkdown("$x^2$ 与 \\(y_1\\) 以及 \\[z=3\\]"))
      .toContain("$y_1$");
    expect(normalizeMathMarkdown("\\[z=3\\]")).toContain("$$\nz=3\n$$");
  });

  it("does not rewrite ordinary set separators or absolute-value bars", () => {
    expect(normalizeMathMarkdown("$A=\\{x|x>0\\}$，$|x|=2$"))
      .toBe("$A=\\{x|x>0\\}$，$|x|=2$");
  });

  it("promotes nested fractions to display math without expanding ordinary fractions", () => {
    const nested = String.raw`概率为：$P(A_3|D)=\frac{P(A_3)P(D|A_3)}{P(D)}=\frac{\frac{30}{2000}}{\frac{97}{2000}}=\frac{30}{97}$。`;
    const ordinary = String.raw`$P(A_1)=\frac{8}{20}=\frac{2}{5}$`;

    expect(normalizeMathMarkdown(nested)).toContain("$$\nP(A_3|D)=");
    expect(normalizeMathMarkdown(ordinary)).toBe(ordinary);
  });

  it("promotes long or structurally tall derivations while preserving short inline math", () => {
    const derivation = String.raw`求导得$h'(x)=f'(x)+f'(2x_{2n+1}-x)=\frac{\cos x}{e^x+e^{-x}}+\frac{\cos(2x_{2n+1}-x)}{e^{(2x_{2n+1}-x)}+e^{-(2x_{2n+1}-x)}}$，所以递增。`;
    const normalized = normalizeMathMarkdown(derivation);

    expect(requiresDisplayMath(String.raw`x^2+y^2=1`)).toBe(false);
    expect(requiresDisplayMath(String.raw`P(A_1)=\frac{8}{20}=\frac{2}{5}`)).toBe(false);
    expect(normalized).toContain("$$\nh'(x)=");
    expect(normalized).toContain(String.raw`\text{，}`);
    expect(normalized).not.toContain("$$\nx^2+y^2=1");

    const html = renderToStaticMarkup(createElement(MathMarkdown, { children: derivation }));
    expect(html).toContain("katex-display");
  });

  it("does not reinterpret escaped dollars or code examples as document math", () => {
    const source = "费用为 \\$20；示例：`$\\frac{\\frac{a}{b}}{c}$`。";
    expect(normalizeMathMarkdown(source)).toBe(source);
  });
});
