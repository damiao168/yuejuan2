import { Alert, Button, ConfigProvider, Space } from "antd";
import { ChevronLeft, ChevronRight, LockKeyhole, Save, Trash2 } from "lucide-react";
import { StatusTag } from "../../components/StatusTag";
import { PaperImportReviewPanel } from "../paper-import/PaperImportReviewPanel";
import { PaperQuestionForm } from "./PaperQuestionForm";
import { PaperScoringConfiguration } from "./PaperScoringConfiguration";
import { hasStandardAnswer, questionEditorDescription, rubricSaveLabel } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;

function savedResultAction(context: PaperRubricViewContext) {
  const { state } = context;
  // 保存后的回读未完成时先恢复读取，不能提前跳到下一题。
  if (state.saveRefreshPending) return <Button loading={state.saveBusy} onClick={context.actions.rereadSavedResult}>重新读取保存结果</Button>;
  if (state.allSaveCompleted && state.selectedQuestionIndex >= 0 && state.selectedQuestionIndex < context.config.questions.length - 1) {
    return <Button onClick={() => context.actions.openQuestion(context.config.questions[state.selectedQuestionIndex + 1])}>保存完成，下一题</Button>;
  }
  return undefined;
}

export function PaperQuestionDetail({ context }: ViewProps) {
  const { state, latestPaperImport, pendingDrafts } = context;
  const index = state.activeCandidateIndex;
  if (index !== null && latestPaperImport && pendingDrafts[index]) return <PaperCandidateDetail context={context} index={index} />;
  return <ConfigProvider componentDisabled={state.saveBusy}><section className="workspace-section question-editor">
    <PaperQuestionHeading context={context} />
    {state.allSaveMessage && <Alert showIcon type={state.allSaveCompleted ? "success" : "warning"} message={state.allSaveMessage}
      action={savedResultAction(context)} />}
    <div className="paper-detail-tabs" role="tablist" aria-label="题目详情">
      <button type="button" role="tab" aria-selected={state.detailTab === "content"} className={state.detailTab === "content" ? "active" : ""}
        onClick={() => context.actions.setDetailTab("content")}>题目与答案{state.formDirty ? " · 未保存" : ""}</button>
      <button type="button" role="tab" aria-selected={state.detailTab === "scoring"} className={state.detailTab === "scoring" ? "active" : ""}
        onClick={() => context.actions.setDetailTab("scoring")}>评分配置{context.rubricWorkflow.rubricDirty || context.scoringRuleWorkflow.scoringRuleDirty ? " · 未保存" : ""}</button>
    </div>
    {/* 隐藏而不是卸载表单；切换评分标签时保留字段注册和题目草稿。 */}
    <div style={{ display: state.detailTab === "content" ? undefined : "none" }}><PaperQuestionForm context={context} /></div>
    {state.detailTab === "scoring" && <PaperScoringConfiguration context={context} />}
  </section></ConfigProvider>;
}

function PaperCandidateDetail({ context, index }: ViewProps & Readonly<{ index: number }>) {
  const { latestPaperImport: job, pendingDrafts, config, importWorkflow } = context;
  if (!job) return null;
  return <section className="workspace-section question-editor paper-candidate-editor">
    <div className="section-head paper-editor-sticky-head">
      <div><span className="preparation-kicker">资料识别候选</span><h2>第 {pendingDrafts[index].question_no || index + 1} 题</h2>
        <p>已确认 {importWorkflow.confirmedReviewCount} / {pendingDrafts.length} 题。核对本题题干、答案与评分内容后确认；采用试卷结构只决定题目范围。</p></div>
      {job.status === "review_required" && <Space wrap><Button loading={importWorkflow.savingImportReview}
        onClick={() => void importWorkflow.saveImportReview(job, index)}>确认并保存本题</Button></Space>}
    </div>
    <PaperImportReviewPanel job={job} drafts={[pendingDrafts[index]]} existingQuestions={config.questions} showOverview={false}
      readOnly={job.status !== "review_required" || importWorkflow.savingImportReview} onChange={(_, field, patch) => importWorkflow.updateReviewDraft(index, field, patch)}
      onOpenSource={(ref) => void importWorkflow.openImportSource(ref.file_asset_id, ref.page_no)} />
  </section>;
}

function PaperQuestionHeading({ context }: ViewProps) {
  const { config, selectedQuestion, state, access, actions } = context;
  return <div className="section-head paper-editor-sticky-head">
    <div className="paper-editor-title">
      <span className="preparation-kicker">{config.editorMode === "edit" ? `正式题目 · ${state.selectedQuestionIndex + 1} / ${config.questions.length}` : "新增正式题目"}</span>
      <h2>{config.editorMode === "edit" ? `第 ${selectedQuestion?.question_no ?? "—"} 题` : "新建题目"}</h2>
      <p>{questionEditorDescription(selectedQuestion, access.selectedLocked, state.unsavedWork)}</p>
      {state.unsavedWork && <p>核对后可保存本题全部修改，也可分别保存题目与答案、评分配置；分别保存会保留另一处草稿。</p>}
    </div>
    <div className="paper-editor-actions">
      {config.editorMode === "edit" &&
        <div className="paper-editor-pagination" aria-label="切换题目">
          <Button aria-label="上一题" icon={<ChevronLeft size={16} />} disabled={state.selectedQuestionIndex <= 0}
            onClick={() => actions.openQuestion(config.questions[state.selectedQuestionIndex - 1])} />
          <Button aria-label="下一题" icon={<ChevronRight size={16} />} disabled={state.selectedQuestionIndex < 0 || state.selectedQuestionIndex >= config.questions.length - 1}
            onClick={() => actions.openQuestion(config.questions[state.selectedQuestionIndex + 1])} />
        </div>
      }
      {access.selectedLocked && <StatusTag tone="success">已锁定</StatusTag>}
      {config.editorMode === "edit" && <>
        <Button type="text" onClick={() => actions.setSourceOpen(true)}>来源与答题区域</Button>
        <Button type="text" danger aria-label="删除当前题目" title="删除当前题目" icon={<Trash2 size={16} />}
          disabled={access.questionDisabled} onClick={context.questionWorkflow.confirmDeleteQuestion} />
      </>}
      <PaperQuestionSaveActions context={context} />
      <Button type="primary" icon={<Save size={16} />} loading={state.saveBusy} disabled={access.questionDisabled || state.saveBusy || state.saveRefreshPending || (config.editorMode === "edit" && !state.unsavedWork)} onClick={actions.saveAllQuestionChanges}>保存本题全部修改</Button>
      {(state.detailTab === "content" ? state.formDirty : context.rubricWorkflow.rubricDirty || context.scoringRuleWorkflow.scoringRuleDirty) &&
        <Button disabled={state.saveBusy} onClick={actions.discardActiveDraft}>放弃本区修改</Button>}
    </div>
  </div>;
}

export function PaperQuestionSaveActions({ context }: ViewProps) {
  const { state, access, actions, selectedQuestion, options, questionWorkflow, scoringRuleWorkflow, rubricWorkflow } = context;
  if (state.detailTab === "content") return <Button type="primary" icon={<Save size={16} />} disabled={access.questionDisabled || state.saveRefreshPending}
    loading={questionWorkflow.savingQuestion} onClick={actions.saveQuestion}>保存题目与答案</Button>;
  if (state.detailTab !== "scoring") return null;
  if (!access.editorUsesFixedScoring) return <Button type="primary" icon={rubricWorkflow.rubricStatus === "locked" ? <LockKeyhole size={16} /> : <Save size={16} />}
    disabled={access.questionDisabled || state.saveRefreshPending || !selectedQuestion || selectedQuestion.score <= 0 || rubricWorkflow.scoreMismatch}
    loading={rubricWorkflow.savingRubric} onClick={actions.saveRubric}>{rubricSaveLabel(rubricWorkflow.rubricStatus)}</Button>;
  if (!selectedQuestion || !access.objectiveRuleType) return null;
  return <>
    <Button disabled={state.saveBusy || state.saveRefreshPending || !options.canManage || selectedQuestion.score <= 0} loading={scoringRuleWorkflow.savingScoringRule}
      onClick={() => actions.saveScoringRule(false)}>保存规则草稿</Button>
    <Button type="primary" icon={<LockKeyhole size={16} />} disabled={state.saveBusy || state.saveRefreshPending || !options.canManage || selectedQuestion.score <= 0 || !hasStandardAnswer(selectedQuestion)}
      loading={scoringRuleWorkflow.savingScoringRule} onClick={() => actions.saveScoringRule(true)}>发布规则</Button>
  </>;
}
