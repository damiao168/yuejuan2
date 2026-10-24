import { useMemo } from "react";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { normalizeGradingMathText } from "./gradingMathText";

export function GradingMathMarkdown({ children, className = "" }: Readonly<{ children: string; className?: string }>) {
  const content = useMemo(() => normalizeGradingMathText(children), [children]);
  if (children.trim() === "-") return <div className={`math-markdown grading-math-markdown ${className}`.trim()}>{children}</div>;
  return <MathMarkdown className={`grading-math-markdown ${className}`.trim()}>{content}</MathMarkdown>;
}
