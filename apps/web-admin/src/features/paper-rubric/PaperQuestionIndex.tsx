import { Button, Input, Select } from "antd";
import { Plus } from "lucide-react";
import { isUnmatchedPlaceholder, questionAttention, questionIndexLabel, questionScoreLabel, questionSummary, type QuestionFilter } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;

export function PaperQuestionIndex({ context }: ViewProps) {
  const { config, options, state, actions } = context;
  const summary = questionSummary(config.questions, context.latestPaperImport, context.structureConfirmed);
  return <aside className="workspace-section question-list-panel" aria-label="题号总览">
    <div className="section-head question-index-head">
      <div><h2>题号总览</h2><p>按题序定位，选中后在右侧编辑</p></div>
      <Button size="small" icon={<Plus size={14} />} disabled={!options.canManage} onClick={actions.createQuestion}>新建</Button>
    </div>
    <button type="button" className="question-index-toggle" aria-expanded={state.indexOpen}
      onClick={() => actions.setIndexOpen((current) => !current)}>{state.indexOpen ? "收起题号" : "展开题号与筛选"}</button>
    <div className={`question-index-body${state.indexOpen ? " is-open" : ""}`}>
      <Input aria-label="搜索题号或题干" placeholder="搜索题号或题干" value={state.questionSearch}
        onChange={(event) => actions.setQuestionSearch(event.target.value)} allowClear className="question-index-search" />
      <Select<QuestionFilter> aria-label="筛选题目状态" value={state.questionFilter} onChange={actions.setQuestionFilter}
        className="question-index-filter" options={[
          { label: `全部题目 · ${config.questions.length}`, value: "all" },
          { label: `待补答案 · ${summary.needsAnswerCount}`, value: "answer" },
          { label: `待锁定评分细则 · ${summary.needsRubricCount}`, value: "rubric" }
        ]} />
      <PaperCandidateIndex context={context} />
      <div className="question-index-section-head"><strong>正式题目</strong><span>{state.visibleQuestions.length} / {config.questions.length}</span></div>
      <PaperFormalQuestionIndex context={context} />
      <div className="question-index-legend"><span><i className="is-ready" />已填答案或锁定细则</span>
        <span><i className="is-answer" />待补答案</span><span><i className="is-rubric" />细则待锁定</span></div>
    </div>
  </aside>;
}

function PaperCandidateIndex({ context }: ViewProps) {
  if (!context.pendingDrafts.length) return null;
  return <div className="paper-pending-questions"><strong>待核对的识别候选</strong>
    <div className="paper-pending-question-grid">{context.pendingDrafts.map((draft, index) => {
      const candidateLabel = `识别候选 · 第 ${draft.question_no || index + 1} 题 · ${questionScoreLabel(draft.score)}`;
      const selected = context.state.activeCandidateIndex === index;
      return <button key={`${draft.candidate_id ?? draft.question_no}-${index}`} type="button"
        className={`paper-pending-question${selected ? " is-active" : ""}`} aria-current={selected ? "true" : undefined}
        aria-label={candidateLabel} title={candidateLabel} onClick={() => context.actions.openCandidate(index)}>{draft.question_no || index + 1}</button>;
    })}</div>
  </div>;
}

function PaperFormalQuestionIndex({ context }: ViewProps) {
  const { config, state, latestPaperImport, structureConfirmed, actions } = context;
  if (!state.visibleQuestions.length) return <div className="question-index-empty">{config.questions.length ? "没有符合条件的题目" : "暂无题目，请新建题目"}</div>;
  return <div className="question-index-grid">{state.visibleQuestions.map((question) => {
    const placeholder = isUnmatchedPlaceholder(question, latestPaperImport, structureConfirmed);
    const attention = placeholder ? null : questionAttention(question);
    const selected = state.activeCandidateIndex === null && config.selectedQuestionId === question.id;
    const questionLabel = questionIndexLabel(question, placeholder);
    const attentionClass = attention ? ` is-${attention}` : " is-ready";
    return <button key={question.id} type="button" className={`question-index-cell${attentionClass}${selected ? " is-active" : ""}`}
      aria-current={selected ? "true" : undefined} aria-label={questionLabel} title={questionLabel}
      onClick={() => actions.openQuestion(question)}><span>{question.question_no}</span><i aria-hidden="true" /></button>;
  })}</div>;
}
