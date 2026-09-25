import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Drawer, Empty, Spin } from "antd";
import type { ReviewTask } from "../../../../api/review";
import { downloadReviewWorkspaceImage, submitHumanGrade } from "../../../../api/review";
import { getSafeUserText } from "../../../../api/client";
import { appQueryClient } from "../../../../query/client";
import { reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import { loadTaskContext } from "../gradingTaskContext";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { fastConfirmCandidate, type FastConfirmCandidate } from "../fastConfirm";

type Item = { context: WorkbenchContext; candidate: FastConfirmCandidate };

export function FastConfirmDrawer({ tasks, examId, currentUserId, onTasksChanged, onScoringChanged }: {
  tasks: ReviewTask[]; examId: string; currentUserId: string; onTasksChanged: () => Promise<void>; onScoringChanged: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [items, setItems] = useState<Item[]>([]);
  const [inspected, setInspected] = useState<string[]>([]);
  const [activeId, setActiveId] = useState("");
  const [previewUrl, setPreviewUrl] = useState("");
  const [previewReady, setPreviewReady] = useState(false);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [issue, setIssue] = useState("");
  const previewRequest = useRef(0);
  useEffect(() => () => { if (previewUrl) URL.revokeObjectURL(previewUrl); }, [previewUrl]);

  const load = async () => {
    previewRequest.current++;
    setLoading(true); setIssue(""); setInspected([]); setActiveId(""); setPreviewUrl(""); setPreviewReady(false); setPreviewLoading(false);
    try {
      const assigned = tasks.filter((task) => task.exam_id === examId && task.assigned_to === currentUserId && ["assigned", "in_progress", "returned"].includes(task.status)).slice(0, 20);
      const results = await Promise.allSettled(assigned.map(async (task) => {
        appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(task.id) });
        const context = await loadTaskContext(task.id, false);
        const candidate = fastConfirmCandidate(context, currentUserId);
        return candidate ? { context, candidate } : null;
      }));
      setItems(results.flatMap((result) => result.status === "fulfilled" && result.value ? [result.value] : []));
      if (results.some((result) => result.status === "rejected")) setIssue("部分任务加载失败，请刷新后再核对；失败任务未加入快捷确认。 ");
    } finally { setLoading(false); }
  };

  const inspect = async (item: Item) => {
    const request = ++previewRequest.current;
    setIssue(""); setActiveId(item.context.task.id); setPreviewUrl(""); setPreviewReady(false); setPreviewLoading(true);
    try {
      const file = await downloadReviewWorkspaceImage(item.context.segmentImageUrl);
      if (request !== previewRequest.current) return;
      if (!file.contentType.startsWith("image/")) throw new Error("not previewable");
      setPreviewUrl(URL.createObjectURL(file.blob));
    } catch { if (request === previewRequest.current) { setInspected((current) => current.filter((id) => id !== item.context.task.id)); setIssue("题块原图加载失败，当前任务不能快捷确认。请进入完整阅卷工作台。"); } }
    finally { if (request === previewRequest.current) setPreviewLoading(false); }
  };

  const submit = async () => {
    const selected = items.filter((item) => inspected.includes(item.context.task.id));
    if (!selected.length) return;
    setSubmitting(true); setIssue("");
    let completed = 0;
    const completedIds: string[] = [];
    try {
      for (const item of selected) {
        try {
          appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(item.context.task.id) });
          const fresh = await loadTaskContext(item.context.task.id, false);
          const candidate = fastConfirmCandidate(fresh, currentUserId);
          if (!candidate || candidate.grade.id !== item.candidate.grade.id || fresh.task.revision !== item.context.task.revision) throw new Error("任务或 AI 建议已更新");
          await submitHumanGrade(item.context.task.id, {
            expected_revision: fresh.reviewContext.expected_revision,
            score: candidate.score,
            rubric_selections: [], comments: "已核对原图、建议分与证据后快捷确认",
            private_note: "", student_feedback: "", reason: "AI_FAST_CONFIRM 教师明确确认"
          });
          completed++;
          completedIds.push(item.context.task.id);
        } catch { /* Each submission is independent; keep other reviewed items actionable. */ }
      }
      await Promise.allSettled([onTasksChanged(), onScoringChanged()]);
      setItems((current) => current.filter((item) => !completedIds.includes(item.context.task.id)));
      setInspected([]); setActiveId(""); setPreviewUrl(""); setPreviewReady(false); setPreviewLoading(false);
      setIssue(completed === selected.length ? `已由教师确认 ${completed} 份；提交后进入质检，不会直接发布成绩。` : `已确认 ${completed} / ${selected.length} 份；其余任务可能已变化，请刷新并重新核对。`);
    } finally { setSubmitting(false); }
  };

  return <>
    <div className="fast-confirm-entry">
      <div><strong>AI 快捷确认</strong><span>逐份核对原图与建议，批量提交已确认分数</span></div>
      <Button disabled={loading || submitting} onClick={() => { setOpen(true); void load(); }}>查看候选任务</Button>
    </div>
    <Drawer title="AI 快捷确认 · 教师逐份核对" width="min(860px, 100vw)" open={open} closable={!submitting} maskClosable={!submitting} keyboard={!submitting} onClose={() => { if (!submitting) setOpen(false); }}>
      <p>检查当前队列前 20 份已分配给你的任务；仅列出低风险、置信度至少 95%、无风险标记且证据完整的建议。包含评分细则或数学证据的题目请在完整工作台评分。</p>
      <ol className="fast-confirm-steps"><li>选择一份并查看原图</li><li>核对建议分与证据</li><li>逐份勾选后提交</li></ol>
      <Button size="small" disabled={loading || submitting} onClick={() => void load()}>刷新候选任务</Button>
      {issue ? <Alert showIcon type={issue.startsWith("已") ? "success" : "warning"} message={issue} /> : null}
      {loading ? <Spin /> : items.length === 0 ? <Empty description="当前队列前 20 份没有符合快捷确认条件的任务" /> : <>
        <div className="fast-confirm-summary"><strong>符合条件 {items.length} 份</strong><span>已核对 {inspected.length} 份</span><span>未勾选的任务不会提交</span></div>
        <div className="fast-confirm-list">
          {items.map((item) => <div key={item.context.task.id} className="fast-confirm-item">
            <div className="fast-confirm-item-row">
              <strong>{item.context.task.question_no} · 匿名卷 {getSafeUserText(item.context.task.anonymous_code, "—")}</strong>
              <span>建议 {item.candidate.score} / {item.candidate.grade.max_score} · 置信度 {Math.round(item.candidate.grade.confidence * 100)}%</span>
              <Button size="small" disabled={submitting} loading={previewLoading && activeId === item.context.task.id} onClick={() => void inspect(item)}>核对原图与证据</Button>
              <span>{inspected.includes(item.context.task.id) ? "已核对" : "待核对"}</span>
            </div>
            {activeId === item.context.task.id && previewUrl ? <div className="fast-confirm-preview">
              <img src={previewUrl} alt="待核对的学生答题原图" onLoad={() => setPreviewReady(true)} onError={() => { setPreviewReady(false); setInspected((current) => current.filter((id) => id !== item.context.task.id)); setIssue("原图无法显示，请在完整工作台处理该任务。"); }} style={{ maxWidth: "100%", maxHeight: 380, objectFit: "contain" }} />
              <div className="fast-confirm-evidence">
                <strong>本份建议分：{item.candidate.score} / {item.candidate.grade.max_score}</strong>
                <span>置信度 {Math.round(item.candidate.grade.confidence * 100)}% · 准入阈值 {Math.round(item.candidate.threshold * 100)}%</span>
                <ul>{item.candidate.grade.evidence.map((evidence, index) => <li key={index}>{getSafeUserText(evidence.answer_text || evidence.rule || evidence.type, "请在完整工作台核对证据")}</li>)}</ul>
                <Checkbox disabled={!previewReady || submitting} checked={inspected.includes(item.context.task.id)} onChange={(event) => setInspected((current) => event.target.checked ? [...new Set([...current, item.context.task.id])] : current.filter((id) => id !== item.context.task.id))}>已核对原图、证据并确认此分数</Checkbox>
              </div>
            </div> : null}
          </div>)}
        </div>
        <Button type="primary" disabled={!inspected.length} loading={submitting} onClick={() => void submit()}>提交已核对的 {inspected.length} 份</Button>
      </>}
    </Drawer>
  </>;
}
