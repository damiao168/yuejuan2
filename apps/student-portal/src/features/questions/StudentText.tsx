import katex from "katex";
import "katex/dist/katex.min.css";

interface TextPart { text: string; sourceStart: number; formula?: boolean; display?: boolean }

const formulaMarkers = [
  { opening: "$$", closing: "$$", display: true },
  { opening: String.raw`\[`, closing: String.raw`\]`, display: true },
  { opening: String.raw`\(`, closing: String.raw`\)`, display: false },
  { opening: "$", closing: "$", display: false },
];

function escaped(value: string, position: number) {
  let slashes = 0;
  for (let index = position - 1; index >= 0 && value[index] === "\\"; index -= 1) slashes += 1;
  return slashes % 2 === 1;
}

function formulaEnd(value: string, position: number, marker: typeof formulaMarkers[number]) {
  let end = value.indexOf(marker.closing, position + marker.opening.length);
  while (end >= 0 && escaped(value, end)) end = value.indexOf(marker.closing, end + marker.closing.length);
  if (end >= 0 && !marker.display && value.slice(position, end).includes("\n")) return -1;
  return end;
}

export function splitStudentText(value: string): TextPart[] {
  const parts: TextPart[] = [];
  let start = 0;
  let position = 0;
  while (position < value.length) {
    // 长分隔符先匹配；不完整的行内公式仍允许后续独立公式被识别。
    const marker = formulaMarkers.find((candidate) => value.startsWith(candidate.opening, position) && !escaped(value, position));
    const end = marker ? formulaEnd(value, position, marker) : -1;
    if (!marker || end < 0) {
      position += 1;
      continue;
    }
    if (position > start) parts.push({ text: value.slice(start, position), sourceStart: start });
    parts.push({ text: value.slice(position + marker.opening.length, end), sourceStart: position, formula: true, display: marker.display });
    position = end + marker.closing.length;
    start = position;
  }
  if (start < value.length) parts.push({ text: value.slice(start), sourceStart: start });
  return parts;
}

// 普通内容由 React 转义；仅 KaTeX 的生成结果进入 HTML，trust=false 不允许公式使用可信 HTML 扩展。
// 未闭合公式保留原文，解析失败也回退到可阅读的源文本，不让一道题阻塞整页。
export function StudentText({ children }: Readonly<{ children: string }>) {
  return <div className="student-public-text">{splitStudentText(children).map((part) => {
    if (!part.formula) return <span key={part.sourceStart}>{part.text}</span>;
    try {
      const html = katex.renderToString(part.text, { displayMode: part.display, trust: false, throwOnError: false, output: "htmlAndMathml", maxExpand: 1000, maxSize: 10, strict: "warn" });
      return <span key={part.sourceStart} className={part.display ? "student-formula display" : "student-formula"} dangerouslySetInnerHTML={{ __html: html }} />;
    } catch { return <code key={part.sourceStart}>{part.text}</code>; }
  })}</div>;
}
