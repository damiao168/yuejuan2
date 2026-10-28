import type { Dispatch, SetStateAction } from "react";
import { Button, Checkbox, Input, Popconfirm, Space } from "antd";
import { Check, RotateCcw } from "lucide-react";
import type { RubricPoint } from "../../../../api/papers";
import type { AiGrade } from "../../../../api/review";
import { StatusTag } from "../../../../components/StatusTag";
import { SharedScoreControl } from "../../SharedScoreControl";
import { requiresExplicitSecondOpinion } from "../reviewContext";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import { useSingleKeyShortcuts } from "../shortcutPreference";

export interface ScoreEditorProps {
  context: WorkbenchContext;
  draft: ScoreDraft;
  setDraft: Dispatch<SetStateAction<ScoreDraft>>;
  maxScore: number;
  rubricPoints: RubricPoint[];
  selectedGrade?: AiGrade;
  canEditDraft: boolean;
  canReturn: boolean;
  actioning: string | null;
  onAdoptAiScore: () => void;
  onShowAiRecord?: () => void;
  canAdoptAiScore?: boolean;
  bookmarkMarked?: boolean;
  onMarkDispute: () => Promise<void>;
}

export function ScoreEditor({
  context,
  draft,
  setDraft,
  maxScore,
  rubricPoints,
  selectedGrade,
  canEditDraft,
  canReturn,
  actioning,
  onAdoptAiScore,
  onShowAiRecord,
  canAdoptAiScore = true,
  bookmarkMarked,
  onMarkDispute
}: Readonly<ScoreEditorProps>) {
  const { enabled: singleKeyShortcutsEnabled, setEnabled: setSingleKeyShortcutsEnabled } = useSingleKeyShortcuts();
  const requiresSecondOpinion = requiresExplicitSecondOpinion(context.reviewContext);
  const aiRecordButton = onShowAiRecord ? <Button className="score-ai-basis" type="link" size="small" onClick={onShowAiRecord}>评分依据</Button> : null;
  // 管理端查看和不可编辑任务只展示已有记录；草稿分不能冒充已确认成绩，也不展示无法执行的评分按钮。
  if (!canEditDraft) return <section className="score-panel" aria-label="本题评分记录">
    <div className="panel-head"><div><h2>评分记录</h2><p>满分 {maxScore || "—"}</p></div><Space size={8}>{!requiresSecondOpinion ? aiRecordButton : null}<StatusTag tone="neutral">只读</StatusTag></Space></div>
    <p>{draft.score === null ? "当前没有可展示的评分记录。" : `${["submitted", "completed"].includes(context.task.status) ? "已提交得分" : "评分草稿"}：${draft.score} / ${maxScore}`}</p>
  </section>;
  return (
    <section className="score-panel" aria-label="题目评分">
      {/* 高风险独立评分只开放第二意见展示，不从此入口显示或采纳预填分数。 */}
      {!requiresSecondOpinion ? (
        <div className="score-ai-summary">
          {selectedGrade ? <>
            <span className="score-ai-result">AI 建议 <strong>{selectedGrade.suggested_score} / {maxScore}</strong> 分</span>
            <Button size="small" icon={<Check size={14} />} aria-label="采纳 AI 建议分" disabled={!canEditDraft || !canAdoptAiScore || Boolean(actioning)} onClick={onAdoptAiScore}>采纳</Button>
          </> : <span className="muted">暂无 AI 建议</span>}
          {aiRecordButton}
        </div>
      ) : null}

      <SharedScoreControl
        disabled={!canEditDraft || actioning === "submit"}
        maxScore={maxScore}
        rubricPoints={rubricPoints}
        showRubric={false}
        value={{ score: draft.score, rubricSelections: draft.rubricSelections }}
        onChange={(value) => setDraft((current) => ({ ...current, ...value }))}
      />
      {/* 标记状态未确认时不能宣称会交分；这里只提示后果，提交条件仍由阅卷控制器判断。 */}
      <p className="muted" role="status">{bookmarkMarked === undefined ? "标记状态尚未确认，暂不能自动提交。" : bookmarkMarked ? "已标记：当前仅编辑草稿；取消标记后恢复自动提交。" : "联网且满足提交条件后，本次修改的有效得分将自动提交；稍后核对请先标记答卷。"}</p>

      <details className="grading-shortcut-help"><summary>键盘帮助</summary><Checkbox data-grading-shortcuts-toggle="true" checked={singleKeyShortcutsEnabled} onChange={(event) => setSingleKeyShortcutsEnabled(event.target.checked)}>启用单字符快捷键</Checkbox><p>{!singleKeyShortcutsEnabled ? "单字符快捷键已关闭。" : actioning ? "当前操作处理中，录分快捷键暂不可用。" : "当前单题可编辑，快捷键可用。"}输入框内不会触发单字符操作。</p><p>G 定位最终分；1–9 录分（不超过满分）。{!requiresSecondOpinion ? "A 采纳建议分。" : null}</p><p>F 标记；R 退回重评（需确认）；Z 撤销未提交修改；+/- 缩放。</p></details>

      <Space wrap className="grading-submit-bar">
        {canReturn ? (
          <Popconfirm
            title="退回这份答卷重新评阅？"
            description={<div><p>该答卷将退出你的队列，等待重新安排教师。</p><Input.TextArea aria-label="退回原因" rows={2} placeholder="退回原因（可选）" value={draft.disputeReason} onChange={(event) => setDraft((current) => ({ ...current, disputeReason: event.target.value }))} /></div>}
            okText="确认退回"
            cancelText="再想想"
            okButtonProps={{ danger: true }}
            disabled={Boolean(actioning)}
            onConfirm={() => void onMarkDispute()}
          >
            <Button type="text" size="small" icon={<RotateCcw size={14} />} disabled={Boolean(actioning)} loading={actioning === "return"}>退回重评</Button>
          </Popconfirm>
        ) : null}
      </Space>
    </section>
  );
}
