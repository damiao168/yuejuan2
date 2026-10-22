import { useState } from "react";
import { Alert, Button, Drawer, InputNumber, Space } from "antd";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { uniformScorePreview, type QuestionScoreGroup } from "./groupedQuestionScores";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";
import "./grouped-question-scores.css";

export function PaperGroupedQuestionScores({ context }: Readonly<{ context: PaperRubricViewContext }>) {
  const [open, setOpen] = useState(false);
  const workflow = context.groupedScores;
  if (!workflow.groups.length) return null;
  const disabled = !workflow.editable || context.state.saveBusy || context.state.saveRefreshPending || context.state.unsavedWork
    || context.importWorkflow.reviewDirty || context.importWorkflow.savingImportReview || context.importWorkflow.parsing || context.importWorkflow.fillingImportContent;
  const previews = workflow.groups.map(group => uniformScorePreview(group, workflow.scoreForGroup(group), context.config.questions));
  const changedCount = previews.reduce((total, preview) => total + (preview?.changed.length ?? 0), 0);
  const total = context.config.questions.reduce((sum, question) => sum + question.score, 0);
  const projectedTotal = Number((total + previews.reduce((sum, preview) => sum + (preview?.difference ?? 0), 0)).toFixed(4));
  const invalid = workflow.hasInvalidDraft;

  return <>
    <Button onClick={() => setOpen(true)}>批量分值{workflow.dirty ? " · 未保存" : ""}</Button>
    <Drawer title="批量设置分值" width="min(640px, 100vw)" open={open} onClose={() => setOpen(false)} forceRender
      footer={<div className="paper-grouped-score-footer"><span>{changedCount ? `将修改 ${changedCount} 题` : "尚未修改分值"}</span><Space wrap>
        <Button disabled={context.state.saveBusy || !workflow.dirty} onClick={workflow.discardChanges}>恢复当前分值</Button>
        <Button type="primary" disabled={disabled || !changedCount || invalid} loading={context.state.saveBusy} onClick={workflow.applyAllScores}>保存分值修改</Button>
      </Space></div>}>
      <section className="paper-grouped-scores" aria-label="按题型统一分值">
        <p>选择题、填空题可批量设置，解答题逐题填写。</p>
        {!workflow.editable && <p>当前考试只读、准备已冻结或资料结构尚未采用。</p>}
        {(context.state.unsavedWork || context.importWorkflow.reviewDirty) && <p>请先保存或放弃题目、评分配置及识别核对中的修改。</p>}
        <ResponsiveTable<QuestionScoreGroup> className="dense-data-table paper-grouped-score-table" rowKey="type" size="small"
          dataSource={workflow.groups} pagination={false} mobilePrimaryKeys={["type", "count", "current", "target"]}
          columns={[
            { title: "题型", key: "type", width: 120, render: (_, group) => <span>{group.label}{group.lockedCount > 0 && <small>{group.lockedCount} 题已锁定</small>}</span> },
            { title: "数量", key: "count", width: 64, render: (_, group) => group.questions.length },
            { title: "当前每题", key: "current", render: (_, group) => group.uniformScore === null ? "分值不同或未填" : `${group.uniformScore} 分` },
            { title: "修改为", key: "target", width: 140, render: (_, group) => <InputNumber aria-label={`${group.label}统一每题分值`} min={0.5} precision={1}
              value={workflow.scoreForGroup(group)} placeholder="每题分值" size="small" disabled={disabled || !group.editableQuestions.length}
              onChange={value => workflow.setScore(group.type, value)} suffix="分" /> }
          ]} />
        <p className="paper-grouped-score-total">题目合计 <strong>{total}</strong> 分{changedCount > 0 && <> → <strong>{projectedTotal}</strong> 分</>}
          <span>考试满分 {context.selectedExam?.total_score} 分</span></p>
        {invalid && <Alert type="warning" showIcon message="请为已修改的题型填写大于 0 的每题分值" />}
        {workflow.result && <Alert showIcon type={workflow.result.success ? "success" : "warning"} message={workflow.result.text}
          action={workflow.result.refreshPending ? <Button loading={context.state.saveBusy} onClick={() => void workflow.rereadSavedResults()}>重新读取分值结果</Button> : undefined} />}
      </section>
    </Drawer>
  </>;
}
