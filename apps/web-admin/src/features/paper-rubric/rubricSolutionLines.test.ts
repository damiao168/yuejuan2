import { describe, expect, it } from "vitest";
import type { Question } from "../../api/papers";
import { isSolutionSplitOffsetSafe, renderSolutionSpans, rubricSolutionLines, rubricSolutionSources, solutionSplitOffsets, type RubricSolutionLine, type RubricSolutionSource } from "./rubricSolutionLines";

function question(input: Partial<Question>): Question {
  return { id: "q-1", tenant_id: "t-1", exam_id: "e-1", question_no: "1", question_type: "calculation", score: 10,
    options: [], knowledge_points: [], sort_order: 1, status: "draft", ...input };
}

function solution(rawText: string, steps: { step_no: number; content: string }[] = []): NonNullable<Question["solution"]> {
  return { id: "s-1", question_id: "q-1", solution_version: "1", raw_text: rawText, steps, source_refs: [], verification_status: "machine" };
}

describe("rubricSolutionSources", () => {
  it("优先采用已识别步骤，跳过空内容但保留证据原数组位置", () => {
    expect(rubricSolutionSources(question({ solution: solution("原始解答", [
      { step_no: 9, content: " 设 $x=1$ " }, { step_no: 10, content: "  " }, { step_no: 20, content: "因此 $y=2$" }
    ]) }))).toEqual([
      { id: "step-1", position: 1, content: "设 $x=1$" }, { id: "step-3", position: 2, content: "因此 $y=2$" }
    ]);
  });

  it("原文按非空逻辑行划分，多行数学公式不能拆成独立步骤", () => {
    expect(rubricSolutionSources(question({ solution: solution("由题意\r\n\r\n$$\r\nx=1+2\r\n=3\r\n$$\r\n所以\r\n\\[\r\ny=x^2\r\n\\] 成立\r\n") }))).toEqual([
      { id: "step-1", position: 1, content: "由题意" },
      { id: "step-2", position: 2, content: "$$\nx=1+2\n=3\n$$" },
      { id: "step-3", position: 3, content: "所以" },
      { id: "step-4", position: 4, content: "\\[\ny=x^2\n\\] 成立" }
    ]);
  });

  it("行内完整公式无需合并下一行，未闭合公式整段保留", () => {
    expect(rubricSolutionSources(question({ solution: solution("$$x=1$$，所以成立\n下一步\n$$\ny=2\n\n结论") })).map((line) => line.content))
      .toEqual(["$$x=1$$，所以成立", "下一步", "$$\ny=2\n\n结论"]);
  });

  it("无可用解答时只接受文字标准答案，不将结构化对象伪装成步骤", () => {
    const answerKey = { id: "a-1", question_id: "q-1", answer_version: "1", standard_answer: "先代入\n\n得到 $x=2$", equivalent_answers: [], tolerance: {} };
    expect(rubricSolutionSources(question({ solution: solution(" ", [{ step_no: 1, content: "" }]), answer_key: answerKey }))).toEqual([
      { id: "step-1", position: 1, content: "先代入" }, { id: "step-2", position: 2, content: "得到 $x=2$" }
    ]);
    expect(rubricSolutionSources(question({ answer_key: { ...answerKey, standard_answer: { result: "x=2" } } }))).toEqual([]);
    expect(rubricSolutionSources(undefined)).toEqual([]);
  });
});

function fromSingleStep(content: string) {
  const input = question({ solution: solution(content, [{ step_no: 1, content }]) });
  return { sources: rubricSolutionSources(input), lines: rubricSolutionLines(input) };
}

function assertOriginalCoverage(lines: RubricSolutionLine[], sources: RubricSolutionSource[]) {
  for (const source of sources) {
    const spans = lines.flatMap((line) => line.spans ?? []).filter((span) => span.source_id === source.id);
    expect(spans[0]?.start).toBe(0);
    expect(spans[spans.length - 1]?.end).toBe(source.content.length);
    expect(spans.map((span, index) => index ? span.start === spans[index - 1].end : true).every(Boolean)).toBe(true);
    expect(spans.map((span) => source.content.slice(span.start, span.end)).join("")).toBe(source.content);
  }
}

describe("rubricSolutionLines", () => {
  it("已识别步骤内部继续按小问、句号、分号、自然行与独立公式细分", () => {
    const { lines, sources } = fromSingleStep("（1）由题意得。设 $x=1$；\n因此\n$$y=x^2$$\n（2）检验成立。");
    expect(lines.map((line) => line.content)).toEqual(["（1）由题意得。", "设 $x=1$；", "因此", "$$y=x^2$$", "（2）检验成立。"]);
    assertOriginalCoverage(lines, sources);
  });

  it("空步骤保留证据原数组编号，细分 ID 仅由原文范围决定", () => {
    const input = question({ solution: solution("", [
      { step_no: 4, content: "甲\n乙" }, { step_no: 6, content: " " }, { step_no: 9, content: "丙" }
    ]) });
    const lines = rubricSolutionLines(input);
    expect(lines.map((line) => line.id)).toEqual(["step-1:0-2", "step-1:2-3", "step-3:0-1"]);
    expect(lines.map((line) => line.position)).toEqual([1, 2, 3]);
    assertOriginalCoverage(lines, rubricSolutionSources(input));
  });

  it("aligned 顶层推导行独立显示，矩阵、分式和嵌套环境内部保持完整", () => {
    const matrix = String.raw`\begin{pmatrix}1 & 2 \\ 3 & 4\end{pmatrix}`;
    const cases = String.raw`\begin{cases}x & x>0 \\ -x & x\leq0\end{cases}`;
    const fraction = String.raw`\frac{1+\begin{aligned}a\\b\end{aligned}}{2}`;
    const text = `$$\n\\begin{aligned}\nA&=${matrix} \\\\\nB&=${cases} \\\\\nC&=${fraction}\n\\end{aligned}\n$$`;
    const { lines, sources } = fromSingleStep(text);
    expect(lines).toHaveLength(3);
    expect(lines[0].content).toContain(matrix);
    expect(lines[1].content).toContain(cases);
    expect(lines[2].content).toContain(fraction);
    for (const line of lines) {
      expect(line.content).toContain("\\begin{aligned}");
      expect(line.content).toContain("\\end{aligned}");
    }
    assertOriginalCoverage(lines, sources);
  });

  it("裸推导按顶层换行拆开，分式参数内的换行不拆", () => {
    const { lines, sources } = fromSingleStep("$$\nx=\\frac{1+\n2}{3}\n=1\n$$");
    expect(lines.map((line) => line.content)).toEqual(["$$\nx=\\frac{1+\n2}{3}\n$$", "$$\n=1\n$$"]);
    assertOriginalCoverage(lines, sources);
  });

  it("独立矩阵、cases 及没有数学定界符的矩阵都不拆内部行", () => {
    for (const environment of ["pmatrix", "cases"]) {
      const body = `\\begin{${environment}}\na&b\\\\\nc&d\n\\end{${environment}}`;
      for (const text of [body, `$$\n${body}\n$$`, `\\[${body}\\]`]) {
        const { lines, sources } = fromSingleStep(text);
        expect(lines).toHaveLength(1);
        expect(lines[0].content).toBe(text);
        assertOriginalCoverage(lines, sources);
      }
    }
  });

  it("成对伸缩括号跨行时保留完整，避免独立公式缺失 left 或 right", () => {
    const { lines, sources } = fromSingleStep("$$\nx=\\left(\na+\nb\n\\right)\n=3\n$$");
    expect(lines).toHaveLength(2);
    expect(lines[0].content).toContain("\\left(\na+\nb\n\\right)");
    expect(lines[1].content).toBe("$$\n=3\n$$");
    assertOriginalCoverage(lines, sources);
  });

  it("未闭合数学公式与不完整环境保留整段，不猜测分界", () => {
    for (const text of ["$$\nx=1\ny=2", "$$\\begin{aligned}x&=1\\\\y&=2$$", "$$x=\\frac{1\\\\2}{3$$"]) {
      const { lines, sources } = fromSingleStep(text);
      expect(lines).toHaveLength(1);
      assertOriginalCoverage(lines, sources);
    }
  });

  it("分段后合并连续范围显示为完整原公式，保留 alignedat 列数参数", () => {
    const { lines, sources } = fromSingleStep(String.raw`$$\begin{alignedat}{2}x&=1&y&=2\\[4pt]x&=2&y&=3\end{alignedat}$$`);
    expect(lines).toHaveLength(2);
    expect(lines[1].content).toContain("\\begin{alignedat}{2}");
    expect(renderSolutionSpans(lines.flatMap((line) => line.spans ?? []), sources)).toBe(sources[0].content);
  });

  it("无解答时细分文字标准答案，但对象标准答案保持无单元", () => {
    const answer_key = { id: "a-1", question_id: "q-1", answer_version: "1", standard_answer: "设x。得到结果", equivalent_answers: [], tolerance: {} };
    expect(rubricSolutionLines(question({ answer_key })).map((line) => line.content)).toEqual(["设x。", "得到结果"]);
    expect(rubricSolutionLines(question({ answer_key: { ...answer_key, standard_answer: { x: 1 } } }))).toEqual([]);
  });
});

describe("手动拆分安全边界", () => {
  it("中文长段可任意码点分界，emoji 代理对及行内公式中间不允许分界", () => {
    const text = "长段😀中文$x+1$接着\\(y=2\\)结论";
    const { lines, sources } = fromSingleStep(text);
    const line = lines[0];
    expect(isSolutionSplitOffsetSafe(line, sources, 1)).toBe(true);
    expect(isSolutionSplitOffsetSafe(line, sources, 3)).toBe(false);
    expect(isSolutionSplitOffsetSafe(line, sources, 4)).toBe(true);
    expect(isSolutionSplitOffsetSafe(line, sources, text.indexOf("x") + 1)).toBe(false);
    expect(isSolutionSplitOffsetSafe(line, sources, text.indexOf("y") + 1)).toBe(false);
    expect(isSolutionSplitOffsetSafe(line, sources, text.indexOf("$"))).toBe(true);
    expect(isSolutionSplitOffsetSafe(line, sources, 0)).toBe(false);
    expect(isSolutionSplitOffsetSafe(line, sources, text.length)).toBe(false);
  });

  it("手动候选包括句号分号与展示公式边界，矩阵内部始终不可拆", () => {
    const { sources } = fromSingleStep("前文。中间；$$\\begin{matrix}1&2\\\\3&4\\end{matrix}$$后文");
    const line: RubricSolutionLine = { id: "whole", position: 1, content: sources[0].content, spans: [{ source_id: "step-1", start: 0, end: sources[0].content.length }] };
    expect(solutionSplitOffsets(line, sources).map((item) => item.offset)).toEqual([3, 6, sources[0].content.indexOf("后文")]);
    expect(isSolutionSplitOffsetSafe(line, sources, sources[0].content.indexOf("3&4"))).toBe(false);
    expect(solutionSplitOffsets(line, sources).every((item) => isSolutionSplitOffsetSafe(line, sources, item.offset))).toBe(true);
  });

  it("合并跨 source 单元的手动偏移不插入虚构换行，原 source 分界可再拆", () => {
    const sources = [{ id: "step-1", position: 1, content: "第一段" }, { id: "step-3", position: 2, content: "第二段" }];
    const line: RubricSolutionLine = { id: "merged", position: 1, content: "第一段\n第二段", spans: sources.map((source) => ({ source_id: source.id, start: 0, end: source.content.length })) };
    expect(solutionSplitOffsets(line, sources)).toEqual([{ offset: 3, label: "原文第 3 个字符后" }]);
    expect(isSolutionSplitOffsetSafe(line, sources, 3)).toBe(true);
    expect(renderSolutionSpans(line.spans ?? [], sources)).toBe("第一段\n第二段");
  });
});
