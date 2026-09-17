import { describe, expect, it } from "vitest";
import { normalizeMathMarkdown } from "./MathMarkdown";

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
});
