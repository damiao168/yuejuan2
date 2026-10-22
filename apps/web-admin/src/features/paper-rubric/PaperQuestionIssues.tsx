import { useState } from "react";
import { Button, Drawer } from "antd";
import { isUnmatchedPlaceholder, questionNeedsAnswer, questionNeedsRubric, type QuestionIssueTarget } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

export function PaperQuestionIssues({ context }: Readonly<{ context: PaperRubricViewContext }>) {
  const [open, setOpen] = useState(false);
  const questions = context.config.questions.filter(question => !isUnmatchedPlaceholder(question, context.latestPaperImport, context.structureConfirmed));
  const issues = questions.flatMap(question => {
    const targets: { target: QuestionIssueTarget; label: string }[] = [];
    if (question.score <= 0) targets.push({ target: "score", label: "填写分值" });
    if (questionNeedsAnswer(question)) targets.push({ target: "answer", label: "填写答案" });
    if (questionNeedsRubric(question)) targets.push({ target: "rubric", label: question.rubric?.points.length ? "核对评分细则" : "设置评分细则" });
    return targets.length ? [{ question, targets }] : [];
  });
  if (!issues.length) return null;
  return <>
    <Button type="text" onClick={() => setOpen(true)}>待处理题目</Button>
    <Drawer title="待处理题目" width="min(440px, 100vw)" open={open} onClose={() => setOpen(false)}>
      <ul className="paper-question-issue-list">{issues.map(({ question, targets }) => <li key={question.id}>
        <strong>第 {question.question_no} 题</strong><div>{targets.map(({ target, label }) => <Button key={target} type="link"
          aria-label={`第 ${question.question_no} 题：${label}`} disabled={context.state.saveBusy} onClick={() => {
            context.actions.openQuestionIssue(question, target); setOpen(false);
          }}>{label}</Button>)}</div>
      </li>)}</ul>
    </Drawer>
  </>;
}
