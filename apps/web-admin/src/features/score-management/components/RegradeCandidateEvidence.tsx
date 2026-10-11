import { useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { getRegradeReviewContext, downloadRegradeReviewImage, type RegradeReviewContext } from "../../../api/regrade";
import { getUserErrorMessage } from "../../../api/client";
import { TaskScoringReference } from "../../../components/TaskScoringReference";
import { MathMarkdown } from "../../../components/MathMarkdown";

export function RegradeCandidateEvidence({ id, revision, onReady }: Readonly<{ id: string; revision: number; onReady: (revision: number) => void }>) {
  const [open, setOpen] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [value, setValue] = useState<RegradeReviewContext>();
  const [url, setUrl] = useState("");
  const [error, setError] = useState("");
  const [imageError, setImageError] = useState("");
  const ready = useRef(onReady); ready.current = onReady;
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController(); let imageUrl = "";
    setValue(undefined); setUrl(""); setError(""); setImageError("");
    getRegradeReviewContext(id, controller.signal).then(response => {
      if (controller.signal.aborted) return;
      const context = response.regrade_review_context;
      if (context.item.id !== id || context.item.revision !== revision || context.context.expected_revision !== revision) throw new Error("候选已更新，请关闭后刷新候选列表再复核");
      setValue(context);
      if (context.context.answer.raw_answer || context.context.answer.ocr_text) ready.current(revision);
      return downloadRegradeReviewImage(id, controller.signal).then(file => {
        if (controller.signal.aborted) return;
        imageUrl = URL.createObjectURL(file.blob); setUrl(imageUrl);
      }).catch(cause => { if (!controller.signal.aborted) setImageError(getUserErrorMessage(cause, "答题图加载失败")); });
    }).catch(cause => { if (!controller.signal.aborted) setError(getUserErrorMessage(cause, "候选材料加载失败")); });
    // 材料属于这条候选及修订号，旧请求和旧图片不能为后来候选开放决定按钮。
    return () => { controller.abort(); if (imageUrl) URL.revokeObjectURL(imageUrl); };
  }, [id, revision, open, attempt]);
  if (!open) return <Button onClick={() => setOpen(true)}>查看答卷与冻结标准</Button>;
  return <section aria-label="重评候选复核材料">
    {error ? <Alert type="error" showIcon message={error} action={<Button onClick={() => setAttempt(current => current + 1)}>重试材料</Button>} /> : null}
    {value && <><p>匿名答卷 {value.item.submission_id} · 第 {value.context.question.question_no} 题</p><p>原发布分 {value.item.old_score} · 候选分 {value.item.candidate_score ?? "未记录"} · 满分 {value.item.max_score}</p><TaskScoringReference question={value.context.question} rubric={value.context.frozen_rubric} /><MathMarkdown>{value.context.answer.raw_answer || value.context.answer.ocr_text || "无可用答题文字"}</MathMarkdown></>}
    {!value && !error && <p><output aria-live="polite" aria-atomic="true">正在读取本条候选材料…</output></p>}
    {url ? <img src={url} alt="本条重评候选答题图" style={{ maxWidth: "100%", maxHeight: 420 }} onLoad={() => ready.current(revision)} onError={() => { setUrl(""); setImageError("答题图无法显示，请重试"); }} /> : null}
    {imageError ? <Alert type="warning" showIcon message={imageError} action={<Button onClick={() => setAttempt(current => current + 1)}>重试答题图</Button>} /> : null}
  </section>;
}
