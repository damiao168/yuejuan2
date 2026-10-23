import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { Button, Checkbox, Input, Modal, Popconfirm, Space, Tooltip } from "antd";
import { Award, Check, CheckCircle2, Flag } from "lucide-react";
import type { RubricPoint } from "../../../../api/papers";
import type { AiGrade } from "../../../../api/review";
import { StatusTag } from "../../../../components/StatusTag";
import { SharedScoreControl } from "../../SharedScoreControl";
import { requiresExplicitSecondOpinion } from "../reviewContext";
import { commentPresets } from "../gradingWorkbench.model";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import { useSingleKeyShortcuts } from "../shortcutPreference";
import { aiPointAdoption } from "../aiAdoption";

export interface ScoreEditorProps {
  context: WorkbenchContext;
  draft: ScoreDraft;
  setDraft: Dispatch<SetStateAction<ScoreDraft>>;
  maxScore: number;
  rubricPoints: RubricPoint[];
  selectedGrade?: AiGrade;
  canEditDraft: boolean;
  canSubmit: boolean;
  canReturn: boolean;
  canManageTasks: boolean;
  actioning: string | null;
  onAdoptAiScore: () => void;
  onAdoptAiFeedback?: () => void;
  canAdoptAiScore?: boolean;
  onMarkDispute: () => Promise<void>;
  onSubmit: (nominateAsGold?: boolean) => Promise<void>;
  quickSubmit?: boolean;
  onQuickSubmitChange?: (value: boolean) => void;
  hidePrimarySubmit?: boolean;
}

export function ScoreEditor({
  context,
  draft,
  setDraft,
  maxScore,
  rubricPoints,
  selectedGrade,
  canEditDraft,
  canSubmit,
  canReturn,
  canManageTasks,
  actioning,
  onAdoptAiScore,
  onAdoptAiFeedback,
  canAdoptAiScore = true,
  onMarkDispute,
  onSubmit,
  quickSubmit = false,
  onQuickSubmitChange,
  hidePrimarySubmit = false
}: Readonly<ScoreEditorProps>) {
  const { enabled: singleKeyShortcutsEnabled, setEnabled: setSingleKeyShortcutsEnabled } = useSingleKeyShortcuts();
  const [feedbackOpen, setFeedbackOpen] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切任务后收起上一份答卷的反馈编辑区，不能跨答卷沿用展开状态。
  useEffect(() => { setFeedbackOpen(false); }, [context.task.id]);
  const previewScore = () => {
    if (!selectedGrade) return;
    const mapped = aiPointAdoption(selectedGrade, rubricPoints, context.question?.rubric?.version);
    Modal.confirm({ title: "确认采用 AI 建议分？", okText: "填入评分草稿", cancelText: "继续核对",
      content: <div><p>最终分：{draft.score ?? "未填写"} → {selectedGrade.suggested_score}</p>
        <p>{mapped ? `同时填入 ${rubricPoints.length} 个同版评分点，合计与建议分一致。` : "仅修改总分；未能完整核对评分点绑定，现有评分点保留，请人工核对。"}</p><p>学生反馈和内部备注保持原文。</p></div>,
      onOk: onAdoptAiScore });
  };
  const previewFeedback = () => {
    if (!selectedGrade || !onAdoptAiFeedback) return;
    Modal.confirm({ title: "审读并采用 AI 反馈草稿", okText: "采用并展开编辑", cancelText: "保留现有文字", width: 640,
      content: <div><p>以下文字会替换对应的现有内容，分数不改变。</p>
        <strong>学生可见反馈</strong><p>{selectedGrade.student_feedback ?? "没有建议，保留原文"}</p>
        <strong>内部备注（教师与授权管理员可见）</strong><p>{selectedGrade.teacher_note ?? "没有建议，保留原文"}</p></div>,
      onOk: () => { onAdoptAiFeedback(); setFeedbackOpen(true); } });
  };
  // 管理端查看和不可编辑任务只展示已有记录；草稿分不能冒充已确认成绩，也不展示无法执行的评分按钮。
  if (!canEditDraft) return <section className="score-panel" aria-label="本题评分记录">
    <div className="panel-head"><div><h2>评分记录</h2><p>满分 {maxScore || "—"}</p></div><StatusTag tone="neutral">只读</StatusTag></div>
    <p>{draft.score === null ? "当前没有可展示的评分草稿。" : `评分草稿：${draft.score} / ${maxScore}`}</p>
    {draft.comments || draft.studentFeedback || draft.privateNote ? <details className="grading-more-fields"><summary>评语与备注</summary><div className="grading-more-fields-body">
      {draft.comments ? <p>教师评语：{draft.comments}</p> : null}
      {draft.studentFeedback ? <p>学生反馈：{draft.studentFeedback}</p> : null}
      {draft.privateNote ? <p>内部备注：{draft.privateNote}</p> : null}
    </div></details> : null}
  </section>;
  return (
    <section className="score-panel">
      <div className="panel-head">
        <div>
          <h2>本题评分</h2>
          <p>满分 {maxScore || "—"}</p>
        </div>
        {/* 高风险独立评分只开放第二意见展示，不从此入口采纳预填分数。 */}
        {selectedGrade && !requiresExplicitSecondOpinion(context.reviewContext) ? (
          <Space wrap><Button icon={<Check size={14} />} disabled={!canEditDraft || !canAdoptAiScore} onClick={previewScore}>采纳 AI 建议分</Button>
            {onAdoptAiFeedback && (selectedGrade.student_feedback || selectedGrade.teacher_note) ? <Button disabled={!canEditDraft || !canAdoptAiScore} onClick={previewFeedback}>审读 AI 反馈</Button> : null}</Space>
        ) : null}
      </div>

      <SharedScoreControl
        disabled={!canEditDraft}
        maxScore={maxScore}
        rubricPoints={rubricPoints}
        value={{ score: draft.score, rubricSelections: draft.rubricSelections }}
        onChange={(value) => setDraft((current) => ({ ...current, ...value }))}
      />

      {rubricPoints.length > 0 && draft.score !== null && Math.abs(draft.score - rubricPoints.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0)) > 0.001 ? (
        <div className="grading-field">
          <label htmlFor="score-adjustment-reason">人工调整原因（必填）</label>
          <Input.TextArea id="score-adjustment-reason" disabled={!canEditDraft} value={draft.reason} rows={2} placeholder="说明最终分与评分点合计不同的依据" onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))} />
        </div>
      ) : null}

      <details className="grading-more-fields" open={feedbackOpen} onToggle={(event) => setFeedbackOpen(event.currentTarget.open)}>
        <summary>评语与备注（可选）</summary>
        <div className="grading-more-fields-body">
          <div className="grading-comment-presets">
            <span className="muted">常用评语（点击追加）</span>
            <Space wrap size={4}>
              {commentPresets.map((item) => (
                <Button
                  key={item}
                  size="small"
                  disabled={!canEditDraft}
                  onClick={() => setDraft((current) => ({ ...current, comments: current.comments ? `${current.comments}；${item}` : item }))}
                >
                  {item}
                </Button>
              ))}
            </Space>
          </div>
          <div className="grading-field">
            <label className="grading-field-label muted" htmlFor="grading-comments-input">教师评语（阅卷记录）</label>
            <Input.TextArea id="grading-comments-input" disabled={!canEditDraft} rows={2} placeholder="记录评分依据（可选）" value={draft.comments} onChange={(event) => setDraft((current) => ({ ...current, comments: event.target.value }))} />
          </div>
          <div className="grading-field">
            <div className="grading-field-label">
              <label className="muted" htmlFor="grading-student-feedback-input">学生可见反馈（将展示给学生）</label>
              <StatusTag tone="warning">学生可见</StatusTag>
            </div>
            <Input.TextArea id="grading-student-feedback-input" disabled={!canEditDraft} rows={2} placeholder="这段文字会展示给学生（可选）" value={draft.studentFeedback} onChange={(event) => setDraft((current) => ({ ...current, studentFeedback: event.target.value }))} />
          </div>
          <div className="grading-field">
            <label className="grading-field-label muted" htmlFor="grading-private-note-input">内部备注（阅卷教师及授权管理员可见）</label>
            <Input.TextArea id="grading-private-note-input" disabled={!canEditDraft} rows={2} placeholder="仅教师与管理端可见（可选）" value={draft.privateNote} onChange={(event) => setDraft((current) => ({ ...current, privateNote: event.target.value }))} />
          </div>
          {canReturn ? <Input disabled={!canEditDraft} placeholder="争议原因（可选，退回重评时会一并记录）" prefix={<Flag size={14} />} value={draft.disputeReason} onChange={(event) => setDraft((current) => ({ ...current, disputeReason: event.target.value }))} /> : null}
        </div>
      </details>

      <div className="grading-submit-note">提交后进入质检，不会直接发布成绩</div>
      <Checkbox data-grading-quick-submit="true" checked={quickSubmit} onChange={(event) => onQuickSubmitChange?.(event.target.checked)}>连续阅卷：允许 Enter 提交并下一份</Checkbox>
      <details className="grading-shortcut-help"><summary>键盘帮助 · {quickSubmit ? "Enter" : "Ctrl / ⌘ + Enter"} 提交</summary><Checkbox checked={singleKeyShortcutsEnabled} onChange={(event) => setSingleKeyShortcutsEnabled(event.target.checked)}>启用单字符快捷键（数字、A / F / R / Z、+ / -）</Checkbox><p>{rubricPoints.length ? "1–9 仅切换对应评分点并合计；没有对应评分点的数字键不生效。" : "1–9 直接打分，0分或小数请在最终分输入框填写。"} A 采纳建议分（不采用反馈） · F 标记异常 · R 退回 · Z 撤销本次评分编辑 · +/- 缩放。分数输入框内也可使用提交快捷键；评语框内 Enter 换行，输入法确认或打开弹窗时不会提交。关闭单字符快捷键后仍可使用提交组合键和答题图工具按钮。</p></details>

      <Space wrap className="grading-submit-bar">
        {canReturn ? (
          <Popconfirm
            title="标记争议并退回重评？"
            description="该答卷将退出你的队列，进入重评流程"
            okText="确认退回"
            cancelText="再想想"
            okButtonProps={{ danger: true }}
            onConfirm={() => void onMarkDispute()}
          >
            <Button danger icon={<Flag size={16} />} disabled={!context} loading={actioning === "return"}>标记争议</Button>
          </Popconfirm>
        ) : null}
        {canManageTasks ? (
          <Button icon={<Award size={16} />} disabled={!canSubmit || context.task.status === "submitted"} loading={actioning === "submit"} onClick={() => void onSubmit(true)}>
            提交并提名标准卷
          </Button>
        ) : null}
        {!hidePrimarySubmit ? <Tooltip title={quickSubmit ? "Enter（连续阅卷）" : "Ctrl / ⌘ + Enter"}>
          <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={!canSubmit || context.task.status === "submitted"} loading={actioning === "submit"} onClick={() => void onSubmit()}>
            提交并下一份
          </Button>
        </Tooltip> : null}
      </Space>
    </section>
  );
}
