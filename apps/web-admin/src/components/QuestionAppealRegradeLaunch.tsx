import { useEffect, useRef, useState } from "react";
import { Alert, Button, Select } from "antd";
import { createRegradeJob, getRegradeJob, listRegradeJobs, previewRegrade, type RegradePreview } from "../api/scoreReleases";
import { decideQuestionAppeal, type PublishedQuestionAppeal } from "../api/questionAppeals";
import type { ManagedUser } from "../api/users";
import { getUserErrorMessage } from "../api/client";
import { getBusinessCommandSession } from "../api/businessCommand";
import { matchingAppealRegradeDrafts } from "./questionAppealRegradeRecovery";

/** 只为本申请的冻结答卷创建复评，不把整题所有学生默认带入影响范围。 */
export function QuestionAppealRegradeLaunch({ appeal, reviewers, response, busy, currentUserId = "", onRecoveredResponse, onCreated, onLinked, onWorkingChange }: Readonly<{
  appeal: PublishedQuestionAppeal;
  reviewers: ManagedUser[];
  response: string;
  busy: boolean;
  currentUserId?: string;
  onRecoveredResponse?: (response:string)=>void;
  onCreated: (id: string) => void;
  onLinked?: () => void;
  onWorkingChange: (working: boolean) => void;
}>) {
  const [assignee, setAssignee] = useState("");
  const [preview, setPreview] = useState<RegradePreview | null>(null);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const [createdJob, setCreatedJob] = useState("");
  const [recovering,setRecovering]=useState(Boolean(currentUserId));
  const [recoveryAttempt,setRecoveryAttempt]=useState(0);
  const [recoveryError,setRecoveryError]=useState("");
  const [recoveryCandidates,setRecoveryCandidates]=useState<Array<{id:string;assignee:string;label:string;response:string}>>([]);
  const createdJobRef = useRef("");
  const operation = useRef({ signature: "", key: crypto.randomUUID() });
  const workingRef = useRef(false);
  const mounted = useRef(true);
  const recoveryDisplay = useRef({ reviewers, onRecoveredResponse, onCreated });
  recoveryDisplay.current = { reviewers, onRecoveredResponse, onCreated };
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const selector = { submission_ids: [appeal.submission_id] };
  const [previewAttempt, setPreviewAttempt] = useState(0);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 重试计数用于主动重读；回调和人员显示取最新值，不因父表单渲染反复查询。
  useEffect(()=>{
    if(!currentUserId||createdJobRef.current){setRecovering(false);return;}
    let active=true;const session=getBusinessCommandSession();setRecovering(true);setRecoveryError("");
    void listRegradeJobs(appeal.exam_id).then(async result=>{
      if(!active||session!==getBusinessCommandSession())return;
      const target={id:appeal.id,exam_id:appeal.exam_id,question_id:appeal.question_id,source_release_id:appeal.source_release_id,submission_id:appeal.submission_id};
      const matches=matchingAppealRegradeDrafts(result.regrade_jobs,target,currentUserId,response);
      const candidates=await Promise.all(matches.map(async job=>{
        const detail=await getRegradeJob(job.id);const item=detail.regrade.items[0];
        if(detail.regrade.items.length!==1||item?.submission_id!==appeal.submission_id||!item.assigned_to)return null;
        return {id:job.id,assignee:item.assigned_to,response:job.reason_text,label:`${new Date(job.created_at).toLocaleString("zh-CN")} · ${recoveryDisplay.current.reviewers.find(user=>user.id===item.assigned_to)?.display_name || "已分派阅卷员"}`};
      }));
      if(!active||session!==getBusinessCommandSession())return;
      const valid=candidates.filter((item):item is NonNullable<typeof item>=>Boolean(item));setRecoveryCandidates(valid);
      if(valid.length===1){createdJobRef.current=valid[0].id;setCreatedJob(valid[0].id);setAssignee(valid[0].assignee);if(!response.trim())recoveryDisplay.current.onRecoveredResponse?.(valid[0].response);recoveryDisplay.current.onCreated(valid[0].id);}
    }).catch(reason=>{if(active&&session===getBusinessCommandSession())setRecoveryError(getUserErrorMessage(reason,"已有任务核对失败，暂不能重复创建，请重试"));})
      .finally(()=>{if(active)setRecovering(false);});
    return()=>{active=false;};
  },[appeal.id,appeal.exam_id,appeal.question_id,appeal.source_release_id,appeal.submission_id,currentUserId,response,recoveryAttempt]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 预览重试计数触发重读，接口参数保持同一冻结来源。
  useEffect(() => {
    let active = true;
    setPreview(null); setError("");
    void previewRegrade(appeal.exam_id, appeal.question_id, { source_release_id: appeal.source_release_id, selector: { submission_ids: [appeal.submission_id] } })
      .then(result => { if (active) setPreview(result.preview); })
      .catch(cause => { if (active) setError(getUserErrorMessage(cause, "复评范围读取失败，请重试")); });
    return () => { active = false; };
  }, [appeal.exam_id, appeal.question_id, appeal.source_release_id, appeal.submission_id, previewAttempt]);
  const create = async () => {
    if (workingRef.current || busy || recovering || recoveryError || (!createdJob && recoveryCandidates.length>1) || !preview || !assignee || !response.trim()) return;
    // 来源、题目和单份答卷必须与展示的预览一致；重试沿用同一幂等键，避免重复创建任务。
    if (preview.source_release_id !== appeal.source_release_id || preview.question_id !== appeal.question_id || preview.affected_count !== 1) {
      setError("影响范围不是本申请的一份答卷，请重新预览并联系学校考试负责人核对。"); return;
    }
    const signature = JSON.stringify([appeal.id, assignee, response.trim()]);
    if (operation.current.signature !== signature) operation.current = { signature, key: crypto.randomUUID() };
    workingRef.current = true; setWorking(true); onWorkingChange(true); setError("");
    const session = getBusinessCommandSession();
    const isCurrent = () => mounted.current && session === getBusinessCommandSession();
    try {
      // 创建和关联是两个事务。保留已创建任务的回执；第二步失败时只续做关联，不能再次创建。
      let jobId = createdJobRef.current;
      if (!jobId) {
        const result = await createRegradeJob(appeal.exam_id, appeal.question_id, { source_release_id: appeal.source_release_id, selector, reason_code: "appeal_pattern", reason_text: response.trim(), strategy: "human_recheck", assignee_id: assignee, idempotency_key: operation.current.key });
        // 离开申请或更换账号后，旧创建回包不能继续替新会话关联申请。
        if (!isCurrent()) return;
        jobId = result.regrade.job.id; createdJobRef.current = jobId; setCreatedJob(jobId); onCreated(jobId);
      }
      await decideQuestionAppeal(appeal.id, { decision: "refer_regrade", public_response: response.trim(), regrade_job_id: jobId, expected_revision: appeal.revision });
      if (isCurrent()) onLinked?.();
    } catch (cause) { if (isCurrent()) setError(getUserErrorMessage(cause, createdJobRef.current ? "任务已创建，关联尚未完成。输入已保留，请继续关联。" : "复评任务创建失败，输入已保留，请重试")); }
    finally { workingRef.current = false; if (isCurrent()) { setWorking(false); onWorkingChange(false); } }
  };
  return <section className="question-appeal-actions"><h3>为本申请创建题目复评</h3><p>来源：第 {appeal.source_release_version} 版 · 第 {appeal.question_no} 题。仅重评本申请对应的一份答卷。</p>
    {recovering ? <p>正在核对本申请已有复评任务…</p> : null}
    {recoveryError ? <Alert type="warning" message={recoveryError} /> : null}
    {recoveryError ? <Button onClick={()=>setRecoveryAttempt(value=>value+1)}>重新核对已有任务</Button> : null}
    {!createdJob && recoveryCandidates.length>1 ? <label>已有同范围复评任务<Select aria-label="选择已有复评任务" placeholder="核对创建时间和阅卷员后继续" options={recoveryCandidates.map(item=>({value:item.id,label:item.label}))} onChange={id=>{const chosen=recoveryCandidates.find(item=>item.id===id);if(chosen){createdJobRef.current=id;setCreatedJob(id);setAssignee(chosen.assignee);if(!response.trim())onRecoveredResponse?.(chosen.response);onCreated(id);}}}/></label> : null}
    <Select aria-label="分派本题重评阅卷员" value={assignee || undefined} placeholder="选择重评阅卷员" options={reviewers.filter((user) => user.roles.includes("grader")).map((user) => ({ value: user.id, label: user.display_name || user.username }))} onChange={setAssignee} disabled={working || busy || Boolean(createdJob)} />
    {!preview ? <Button onClick={() => setPreviewAttempt(value => value + 1)} disabled={working || busy}>重新读取影响范围</Button> : null}
    {preview ? <Alert type={preview.affected_count === 1 ? "info" : "error"} showIcon message={`将影响 ${preview.affected_count} 份答卷`} description="创建后任务尚未改分；还需在考试成绩工作台批准、启动、复核候选、完成复评并发布新成绩。" /> : null}
    <Button onClick={() => void create()} loading={working} disabled={busy || recovering || Boolean(recoveryError) || (!createdJob && recoveryCandidates.length>1) || !assignee || !response.trim() || preview?.affected_count !== 1}>{createdJob ? "继续关联本次复评" : "创建并关联本次复评"}</Button>
    {error ? <Alert type="error" showIcon message={error} /> : null}
  </section>;
}
