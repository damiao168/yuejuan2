import { useState } from "react";
import { Button, Input } from "antd";

/** 只提供过时复评的恢复入口；可否重新处理仍由服务端按当前发布事实检查。 */
export function QuestionAppealRestartReview({ busy, onRestart }: Readonly<{ busy: boolean; onRestart: (reason: string) => Promise<void> }>) {
  const [editing, setEditing] = useState(false);
  const [reason, setReason] = useState("");
  return <section className="question-appeal-restart">
    {!editing ? <Button disabled={busy} onClick={() => setEditing(true)}>重新处理</Button> : <>
      <p>其他更正先发布、原复评无法继续时，可基于最新公布成绩重新处理。原申请和历史意见保留，本轮需要重新核对并提交意见。</p>
      <label htmlFor="question-appeal-restart-reason">重新处理原因（必填）</label>
      <Input.TextArea id="question-appeal-restart-reason" maxLength={1000} rows={3} disabled={busy} value={reason} onChange={event => setReason(event.target.value)} />
      <Button type="primary" loading={busy} disabled={busy || !reason.trim()} onClick={() => void onRestart(reason.trim())}>确认重新处理</Button>
      <Button disabled={busy} onClick={() => setEditing(false)}>取消</Button>
    </>}
  </section>;
}
