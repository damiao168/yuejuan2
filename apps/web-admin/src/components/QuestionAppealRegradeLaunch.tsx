import { useRef, useState } from "react";
import { Alert, Button, Select } from "antd";
import { createRegradeJob, previewRegrade, type RegradePreview } from "../api/scoreReleases";
import type { PublishedQuestionAppeal } from "../api/questionAppeals";
import type { ManagedUser } from "../api/users";
import { getUserErrorMessage } from "../api/client";

/** 只为本申请的冻结答卷创建复评，不把整题所有学生默认带入影响范围。 */
export function QuestionAppealRegradeLaunch({ appeal, reviewers, response, busy, onCreated, onWorkingChange }: Readonly<{
  appeal: PublishedQuestionAppeal;
  reviewers: ManagedUser[];
  response: string;
  busy: boolean;
  onCreated: (id: string) => void;
  onWorkingChange: (working: boolean) => void;
}>) {
  const [assignee, setAssignee] = useState("");
  const [preview, setPreview] = useState<RegradePreview | null>(null);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef({ signature: "", key: crypto.randomUUID() });
  const workingRef = useRef(false);
  const selector = { submission_ids: [appeal.submission_id] };
  const previewTarget = async () => {
    if (workingRef.current || busy) return;
    workingRef.current = true; setWorking(true); onWorkingChange(true); setError("");
    try {
      const result = await previewRegrade(appeal.exam_id, appeal.question_id, { source_release_id: appeal.source_release_id, selector });
      setPreview(result.preview);
    } catch (cause) { setError(getUserErrorMessage(cause, "复评范围读取失败，请重试")); }
    finally { workingRef.current = false; setWorking(false); onWorkingChange(false); }
  };
  const create = async () => {
    if (workingRef.current || busy || !preview || !assignee || !response.trim()) return;
    // 来源、题目和单份答卷必须与展示的预览一致；重试沿用同一幂等键，避免重复创建任务。
    if (preview.source_release_id !== appeal.source_release_id || preview.question_id !== appeal.question_id || preview.affected_count !== 1) {
      setError("影响范围不是本申请的一份答卷，请重新预览并联系学校考试负责人核对。"); return;
    }
    const signature = JSON.stringify([appeal.id, assignee, response.trim()]);
    if (operation.current.signature !== signature) operation.current = { signature, key: crypto.randomUUID() };
    workingRef.current = true; setWorking(true); onWorkingChange(true); setError("");
    try {
      const result = await createRegradeJob(appeal.exam_id, appeal.question_id, { source_release_id: appeal.source_release_id, selector, reason_code: "appeal_pattern", reason_text: response.trim(), strategy: "human_recheck", assignee_id: assignee, idempotency_key: operation.current.key });
      onCreated(result.regrade.job.id);
    } catch (cause) { setError(getUserErrorMessage(cause, "复评任务创建失败，输入已保留，请重试")); }
    finally { workingRef.current = false; setWorking(false); onWorkingChange(false); }
  };
  return <section className="question-appeal-actions"><h3>为本申请创建题目复评</h3><p>来源：第 {appeal.source_release_version} 版 · 第 {appeal.question_no} 题。仅重评本申请对应的一份答卷。</p>
    <Select aria-label="分派本题重评阅卷员" value={assignee || undefined} placeholder="选择重评阅卷员" options={reviewers.filter((user) => user.roles.includes("grader")).map((user) => ({ value: user.id, label: user.display_name || user.username }))} onChange={setAssignee} disabled={working || busy} />
    <Button onClick={() => void previewTarget()} loading={working} disabled={busy}>预览本申请影响范围</Button>
    {preview ? <Alert type={preview.affected_count === 1 ? "info" : "error"} showIcon message={`将影响 ${preview.affected_count} 份答卷`} description="创建后任务尚未改分；还需在考试成绩工作台批准、启动、复核候选、完成复评并发布新成绩。" /> : null}
    <Button onClick={() => void create()} loading={working} disabled={busy || !assignee || !response.trim() || preview?.affected_count !== 1}>创建本题复评任务</Button>
    {error ? <Alert type="error" showIcon message={error} /> : null}
  </section>;
}
