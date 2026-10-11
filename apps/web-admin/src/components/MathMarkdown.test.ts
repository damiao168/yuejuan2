import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MathMarkdown, normalizeMathMarkdown, requiresDisplayMath } from "./MathMarkdown";

function renderMarkdown(source: string, promoteInlineMath = false) {
  return renderToStaticMarkup(createElement(MathMarkdown, { children: source, promoteInlineMath }));
}

function elements(html: string, tag: string) {
  return [...html.matchAll(new RegExp(`<${tag}(?:\\s[^>]*)?>[\\s\\S]*?<\\/${tag}>`, "g"))].map((match) => match[0]);
}

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

  it("keeps multi-backtick inline examples and closed or streaming fences intact", () => {
    const examples = [
      "代码：``\\(x\\) 和 $\\frac{\\frac{a}{b}}{c}$ 与 \\[z\\]``。",
      "代码：```\\(x\\) 和 ``\\[z\\]`` ```。",
      ["````text", String.raw`\(x\) 和 \[z\]`, "```", String.raw`$\frac{\frac{a}{b}}{c}$`, "````"].join("\n"),
      ["~~~text", String.raw`\(x\) 和 \[z\]`, String.raw`$\frac{\frac{a}{b}}{c}$`].join("\n"),
      ["> ````text", String.raw`> \(x\) 和 \[z\]`, "> ```"].join("\n")
    ];

    for (const source of examples) {
      expect(normalizeMathMarkdown(source)).toBe(source);
      expect(renderMarkdown(source)).not.toContain('class="katex"');
    }
  });

  it("preserves indented code and link destinations while normalizing visible document math", () => {
    const source = [
      String.raw`正文 \(x+1\)。`,
      "",
      String.raw`    \(code\) 和 \[code\] 与 $\frac{\frac{a}{b}}{c}$`,
      "",
      String.raw`[示例](https://example.test/\(path\)?formula=$\frac{\frac{a}{b}}{c}$)`
    ].join("\n");
    const normalized = normalizeMathMarkdown(source);

    expect(normalized).toContain("正文 $x+1$。");
    expect(normalized).toContain(String.raw`    \(code\) 和 \[code\] 与 $\frac{\frac{a}{b}}{c}$`);
    expect(normalized).toContain(String.raw`https://example.test/\(path\)?formula=$\frac{\frac{a}{b}}{c}$`);
    expect(elements(renderMarkdown(source), "pre")).toHaveLength(1);
  });

  it("keeps escaped TeX delimiters as literal examples", () => {
    const source = String.raw`写法：\\(x\\)、\\[y\\] 和 \$20。`;
    expect(normalizeMathMarkdown(source)).toBe(source);
    expect(renderMarkdown(source)).not.toContain('class="katex"');
  });

  it("keeps an unfinished inline code example literal before and after its closing backtick arrives", () => {
    const unfinished = "示例：`" + String.raw`\(x\)`;

    for (const source of [unfinished, unfinished + "`"]) {
      expect(normalizeMathMarkdown(source)).toBe(source);
      expect(renderMarkdown(source)).not.toContain('class="katex"');
    }
  });

  it("does not split inline formulas out of list items, quotations or table cells", () => {
    const formula = String.raw`$P(A_3|D)=\frac{\frac{30}{2000}}{\frac{97}{2000}}=\frac{30}{97}$`;
    const source = [
      `- 结果为 ${formula}，请复核。`,
      "",
      `> 结果为 ${formula}，请复核。`,
      "",
      "| 项目 | 结果 |",
      "| --- | --- |",
      `| 后验概率 | ${formula} |`
    ].join("\n");
    const html = renderMarkdown(source, true);

    expect(elements(html, "li")).toHaveLength(1);
    expect(elements(html, "blockquote")).toHaveLength(1);
    expect(elements(html, "td")).toHaveLength(2);
    expect(html).not.toContain("katex-display");
    expect(html).not.toContain("katex-error");
  });
});

describe("math markdown rendering", () => {
  it("renders GFM table structure, alignment and a keyboard reachable horizontal scroll region", () => {
    const html = renderMarkdown("| 学科 | 人数 | 完成率 |\n| :--- | ---: | :---: |\n| 数学 | 30 | 90% |");

    expect(html).toContain('class="math-markdown-table-scroll"');
    expect(html).toMatch(/<button(?=[^>]*type="button")(?=[^>]*aria-label="向左滚动表格")[^>]*>/);
    expect(html).toMatch(/<button(?=[^>]*type="button")(?=[^>]*aria-label="向右滚动表格")[^>]*>/);
    expect(html).not.toMatch(/<section[^>]*tabindex=/);
    expect(html).toContain('aria-label="表格，可横向滚动"');
    expect(elements(html, "table")).toHaveLength(1);
    expect(elements(html, "thead")).toHaveLength(1);
    expect(elements(html, "tbody")).toHaveLength(1);
    expect(elements(html, "tr")).toHaveLength(2);
    expect(html).toMatch(/<th[^>]*(?:align="left"|text-align:left)[^>]*>学科<\/th>/);
    expect(html).toMatch(/<td[^>]*(?:align="right"|text-align:right)[^>]*>30<\/td>/);
    expect(html).toMatch(/<td[^>]*(?:align="center"|text-align:center)[^>]*>90%<\/td>/);
  });

  it("renders long inline formulas, absolute values and probability pipes inside intact table cells", () => {
    const source = String.raw`| 类型 | 公式 |
| --- | --- |
| 绝对值 | \(|x|+|y|\) |
| 条件概率 | $P(A|B)=\frac{\frac{30}{2000}}{\frac{97}{2000}}=\frac{30}{97}$ |
| 数组分栏 | $\begin{array}{c|c}a & b \\ c & d\end{array}$ |`;
    const html = renderMarkdown(source);
    const cells = elements(html, "td");

    expect(elements(html, "tr")).toHaveLength(4);
    expect(cells).toHaveLength(6);
    expect(cells.filter((cell) => cell.includes('class="katex"'))).toHaveLength(3);
    expect(html).not.toContain("katex-display");
    expect(html).not.toContain("katex-error");
    expect(html).toContain("P(A|B)");
    expect(html).toContain(String.raw`\begin{array}{c|c}`);
  });

  it("renders dollar and legacy TeX math delimiters as inline or block formulas", () => {
    const source = String.raw`行内 $x^2$ 和 \(y_1\)。

$$z=3$$

\[\frac{a}{b}=c\]`;
    const html = renderMarkdown(source);

    expect((html.match(/class="katex"/g) ?? [])).toHaveLength(4);
    expect((html.match(/class="katex-display"/g) ?? [])).toHaveLength(2);
    expect(html).not.toContain("katex-error");
    expect(html).toContain('<annotation encoding="application/x-tex">x^2</annotation>');
    expect(html).toContain('<annotation encoding="application/x-tex">y_1</annotation>');
  });

  it("keeps explicit display formulas inside their quotation and list containers", () => {
    const source = String.raw`- 第一步：\[x^2+y^2=1\]完成复核。

> 计算结果：\[\frac{a}{b}=c\]请保留结论。`;
    const html = renderMarkdown(source);
    const listItem = elements(html, "li")[0];
    const quotation = elements(html, "blockquote")[0];

    expect(elements(html, "li")).toHaveLength(1);
    expect(listItem).toContain("katex-display");
    expect(listItem).toContain("完成复核。");
    expect(quotation).toContain("katex-display");
    expect(quotation).toContain("请保留结论。");
    expect(html).not.toContain("katex-error");
  });

  it("ends display math before trailing prose at the start of a root, quotation or list line", () => {
    const sources = [
      String.raw`\[x\]正文`,
      String.raw`> \[x\]正文`,
      String.raw`- \[x\]正文`,
      String.raw`> \[x\] 尾注`,
      String.raw`- \[x\] 尾注`
    ];

    for (const source of sources) {
      const html = renderMarkdown(source);
      const annotation = elements(html, "annotation");
      expect(annotation).toEqual(['<annotation encoding="application/x-tex">x</annotation>']);
      expect(html).toContain("katex-display");
      expect(html).toContain(source.includes("尾注") ? "尾注" : "正文");
      expect(html).not.toContain("katex-error");
      if (source.startsWith(">")) expect(elements(html, "blockquote")).toHaveLength(1);
      if (source.startsWith("-")) expect(elements(html, "li")).toHaveLength(1);
    }
  });

  it("keeps display math and trailing prose inside mixed and nested container prefixes", () => {
    const sources = [
      { source: String.raw`- > \[x\]正文`, tag: "blockquote", opening: /<ul>\s*<li>\s*<blockquote>/ },
      { source: String.raw`- - \[x\]正文`, tag: "li", opening: /<ul>\s*<li>\s*<ul>\s*<li>/ },
      { source: String.raw`> - > \[x\]正文`, tag: "blockquote", opening: /<blockquote>\s*<ul>\s*<li>\s*<blockquote>/ },
      { source: "-\t> \\[x\\]正文", tag: "blockquote", opening: /<ul>\s*<li>\s*<blockquote>/ },
      { source: "-\t-\t\\[x\\]正文", tag: "li", opening: /<ul>\s*<li>\s*<ul>\s*<li>/ }
    ];

    for (const { source, tag, opening } of sources) {
      const html = renderMarkdown(source);
      // 只取最内层容器，避免公式移到外层后仍被整条 li 的宽泛匹配误判为通过。
      const innermost = html.match(new RegExp(`<${tag}>(?:(?!<${tag}(?:\\s|>))[\\s\\S])*?<\\/${tag}>`))?.[0];

      expect(html).toMatch(opening);
      expect(innermost).toContain("katex-display");
      expect(innermost).toContain("正文");
      expect(elements(html, "annotation")).toEqual(['<annotation encoding="application/x-tex">x</annotation>']);
      expect(html).not.toContain("katex-error");
    }
  });

  it("preserves a mathematical greater than sign at the first line of a quoted display formula", () => {
    const html = renderMarkdown(String.raw`> \[>0\]正文`);
    const quotation = elements(html, "blockquote")[0];

    expect(quotation).toContain("katex-display");
    expect(quotation).toContain("正文");
    expect(elements(html, "annotation")).toEqual(['<annotation encoding="application/x-tex">&gt;0</annotation>']);
    expect(html).not.toContain("katex-error");
  });

  it("normalizes math in footnote definitions without moving it out of the footnote", () => {
    const html = renderMarkdown("说明[^formula]。\n\n[^formula]: \\[x>0\\]正文");
    const footnotes = elements(html, "section")[0];

    expect(footnotes).toContain("katex-display");
    expect(footnotes).toContain("正文");
    expect(elements(html, "annotation")).toEqual(['<annotation encoding="application/x-tex">x&gt;0</annotation>']);
    expect(html).not.toContain("katex-error");
  });

  it("removes only container prefixes from multiline quoted and listed TeX blocks", () => {
    const sources = [
      { source: [String.raw`> \[`, "> >0", String.raw`> \]`, "> 正文"].join("\n"), tag: "blockquote", formula: "&gt;0" },
      { source: [String.raw`- \[`, "  x>0", String.raw`  \]`, "  正文"].join("\n"), tag: "li", formula: "x&gt;0" },
      { source: [String.raw`- > \[`, "  > x>0", String.raw`  > \]`, "  > 正文"].join("\n"), tag: "blockquote", formula: "x&gt;0" },
      { source: [String.raw`> - > \[`, ">   > x>0", String.raw`>   > \]`, ">   > 正文"].join("\n"), tag: "blockquote", formula: "x&gt;0" }
    ];

    for (const { source, tag, formula } of sources) {
      const html = renderMarkdown(source);
      const innermost = html.match(new RegExp(`<${tag}>(?:(?!<${tag}(?:\\s|>))[\\s\\S])*?<\\/${tag}>`))?.[0];

      expect(innermost).toContain("katex-display");
      expect(innermost).toContain("正文");
      expect(elements(html, "annotation")).toEqual([`<annotation encoding="application/x-tex">${formula}</annotation>`]);
      expect(html).not.toContain("katex-error");
    }
  });

  it("keeps headings and emphasis intact when they contain legacy display delimiters", () => {
    const html = renderMarkdown(String.raw`## \[x\] 标题

**\[y\]**`);
    const heading = elements(html, "h2")[0];
    const emphasis = elements(html, "strong")[0];

    expect(heading).toContain('class="katex"');
    expect(heading).toContain("标题");
    expect(emphasis).toContain('class="katex"');
    expect(elements(html, "annotation")).toEqual([
      '<annotation encoding="application/x-tex">x</annotation>',
      '<annotation encoding="application/x-tex">y</annotation>'
    ]);
    expect(html).not.toContain("katex-display");
    expect(html).not.toContain("katex-error");
  });

  it("recognizes tables whose header formulas contain unescaped math pipes", () => {
    const html = renderMarkdown(String.raw`| $P(A|B)$ | \(|x|\) |
| --- | --- |
| 条件概率 | 绝对值 |`);
    const headers = elements(html, "th");

    expect(headers).toHaveLength(2);
    expect(headers.every((header) => header.includes('class="katex"'))).toBe(true);
    expect(elements(html, "td")).toHaveLength(2);
    expect(elements(html, "annotation")).toEqual([
      '<annotation encoding="application/x-tex">P(A|B)</annotation>',
      '<annotation encoding="application/x-tex">|x|</annotation>'
    ]);
    expect(html).not.toContain("katex-error");
  });

  it("leaves math looking delimiters inside GFM automatic link URLs unchanged", () => {
    const source = String.raw`地址：https://example.test/\(x\)/$z$`;
    const html = renderMarkdown(source);

    expect(normalizeMathMarkdown(source)).toBe(source);
    expect(html).toContain('class="math-markdown-link"');
    expect(html).not.toContain('class="katex"');
    expect(html).not.toMatch(/<a(?:\s|>)/);
  });

  it("keeps complex math inline inside a reference style link when default promotion is enabled", () => {
    const source = String.raw`[结果 $\frac{\frac{a}{b}}{c}$][ref]

[ref]: https://example.test/`;
    const html = renderMarkdown(source, true);
    const links = elements(html, "span").filter((span) => span.includes('class="math-markdown-link"'));

    expect(links).toHaveLength(1);
    expect(links[0]).toContain('class="katex"');
    expect(links[0]).toContain("结果");
    expect(html).not.toContain("katex-display");
    expect(html).not.toContain("katex-error");
    expect(html).not.toMatch(/<a(?:\s|>)/);
  });

  it("renders math fences as formulas and preserves unknown code languages without throwing", () => {
    const math = renderMarkdown(["```math", String.raw`\frac{a}{b}`, "```"].join("\n"));
    const code = renderMarkdown(["```unsupported-school-format", String.raw`\(example\) $value$ <script>`, "```"].join("\n"));

    expect(math).toContain("katex-display");
    expect(math).not.toContain("katex-error");
    expect(code).toContain('class="math-markdown-code-block"');
    expect(code).toContain("复制代码");
    expect(code).toContain("unsupported-school-format");
    expect(code).toContain(String.raw`\(example\) $value$ &lt;script&gt;`);
    expect(code).not.toContain('class="katex"');
  });

  it("highlights declared programming languages and leaves inline code inline", () => {
    const source = ["`let x = 1`", "", "```javascript", "const total = 30;", "```"].join("\n");
    const html = renderMarkdown(source);

    expect(html).toContain("<code>let x = 1</code>");
    expect(html).toContain("language-javascript");
    expect(html).toContain("hljs-keyword");
    expect(elements(html, "pre")).toHaveLength(1);
  });

  it("renders headings, nested lists, quotations, task lists, strikethrough and footnotes", () => {
    const source = [
      "## 阅卷检查",
      "",
      "> 先核查 **成绩**，再发布。",
      "",
      "1. 检查阅卷",
      "   - 检查缺考",
      "   - 检查异常",
      "2. 核查发布",
      "",
      "- [x] 已复核",
      "- [ ] 未发布",
      "",
      "~~旧结论~~ 与 ~普通波浪号~。",
      "",
      "*提示* 需要复核。  ",
      "按当前状态处理。",
      "",
      "---",
      "",
      "结果说明[^review]。",
      "",
      "[^review]: 复核后按最新状态判断。"
    ].join("\n");
    const html = renderMarkdown(source);

    expect(html).toContain("<h2>阅卷检查</h2>");
    expect(html).toContain("<blockquote>");
    expect(html).toContain("<strong>成绩</strong>");
    expect(elements(html, "ol")).toHaveLength(2);
    expect(html).toContain('class="contains-task-list"');
    expect(html).toMatch(/<input(?=[^>]*type="checkbox")(?=[^>]*checked="")(?=[^>]*disabled="")[^>]*\/>/);
    expect(html).toContain("<del>旧结论</del>");
    expect(html).toContain("~普通波浪号~");
    expect(html).toContain("<em>提示</em>");
    expect(html).toContain("<br/>");
    expect(html).toContain("<hr/>");
    expect(html).toContain("复核后按最新状态判断。");
    expect(html).toContain('aria-label="查看注释"');
    expect(html).toContain('aria-label="返回引用"');
    expect(html).toMatch(/href="#markdown-[^\"]+-fn-review"/);
  });

  it("does not activate model supplied HTML, image requests or external and javascript links", () => {
    const source = [
      '<script>alert("x")</script>',
      "",
      '<img src="https://example.test/pixel" onerror="alert(1)">',
      "",
      "![示意图](https://example.test/pixel)",
      "",
      "[外链](https://example.test/path) 和 [脚本](javascript:alert%281%29)"
    ].join("\n");
    const html = renderMarkdown(source);

    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<a");
    expect(html).not.toContain("onerror=");
    expect(html).toContain('class="math-markdown-image-label">示意图</span>');
    expect(html).toContain('class="math-markdown-link"');
    expect(html).toContain("外链");
    expect(html).toContain("脚本");
  });

  it("accepts every streamed prefix and converges to a complete table and formulas", () => {
    // 实际流式回复会停在围栏、分隔符或表格中途；残缺帧不能抛错，最终帧仍应得到完整结构。
    const source = String.raw`| 类型 | 值 |
| --- | --- |
| 概率 | \(P(A|B)=\frac{1}{2}\) |

\[x^2+y^2=1\]

` + ["````text", String.raw`\(code\) $not-math$`, "```", "````"].join("\n");

    for (let length = 0; length <= source.length; length += 1) {
      expect(() => renderMarkdown(source.slice(0, length))).not.toThrow();
    }
    const final = renderMarkdown(source);
    expect(elements(final, "td")).toHaveLength(2);
    expect((final.match(/class="katex"/g) ?? [])).toHaveLength(2);
    expect(final).not.toContain("katex-error");
    expect(elements(final, "pre")).toHaveLength(1);
  });
});
