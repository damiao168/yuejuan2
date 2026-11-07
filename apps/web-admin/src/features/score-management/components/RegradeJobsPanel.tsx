import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, List, Space } from "antd";
import { GitCompareArrows, RefreshCw } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { getCurrentPublishedScoreRelease, listRegradeJobs, listScoreReleases, type RegradeJob, type ScoreRelease } from "../../../api/scoreReleases";
import { listExamGrades, type SubmissionGrade } from "../../../api/scores";
import { StatusTag } from "../../../components/StatusTag";
import type { StatusTone } from "../../../types";
import { useRegradeWorkflow } from "../hooks/useRegradeWorkflow";
import { useScoreAction } from "../hooks/useScoreAction";
import { pendingCorrections } from "../releaseStatus";
import { RegradeWorkflowModals } from "./RegradeWorkflowModals";

const regradeStatusLabels: Record<string, string> = {
  awaiting_approval: "待批准",
  approved: "已批准",
  running: "复评中",
  paused: "已暂停",
  diff_review: "差异复核",
  ready_for_release: "结果待发布",
  cancelled: "已取消"
};
const noGrades: SubmissionGrade[] = [];

function regradeStatusTone(status: RegradeJob["status"], correctionPending: boolean): StatusTone {
  if (status === "ready_for_release") return correctionPending ? "warning" : "success";
  if (status === "cancelled") return "neutral";
  return "processing";
}

async function loadCurrentPublishedRelease(examId: string) {
  try {
    return (await getCurrentPublishedScoreRelease(examId)).release;
  } catch (error) {
    // 只有“尚未发布”是正常空状态；其他读取失败不能当成没有正式成绩。
    if (error instanceof ApiClientError && error.status === 404 && error.code === "score_release_not_found") return null;
    throw error;
  }
}

// 复评操作不会改变考试目录，操作后只重读本面板。
const keepExamList = async () => undefined;

type RegradePanelSnapshot = { examId: string; jobs: RegradeJob[]; publishedRelease: ScoreRelease | null; releases: ScoreRelease[]; grades: SubmissionGrade[] };

/**
 * “复核与异常”页的题目级复评面板：按 examId 自行读取复评任务、当前正式发布版本、版本列表和发起复评所需的小题。
 * 调用方须同时具备 review:manage（复评任务接口）和 score:manage（成绩和发布版本读取）。
 * 更正待发布的任务只跳到成绩发布页，由那里生成并发布结果版本；本面板不生成结果版本。
 * focusedRegradeId 不为 undefined 时，首次读取结束后滚动到面板；非空时同时把该任务排到最前并高亮。
 */
export function RegradeJobsPanel({ examId, focusedRegradeId, onNavigate }: Readonly<{
  examId: string;
  focusedRegradeId?: string;
  onNavigate?: (path: string) => void;
}>) {
  const [snapshot, setSnapshot] = useState<RegradePanelSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  // 每次读取取新代次；换考试、卸载或更晚的读取开始后，旧回包不能写回面板。
  const request = useRef(0);
  const section = useRef<HTMLElement>(null);
  const scrolledFor = useRef<string | undefined>(undefined);

  const load = useCallback(async () => {
    const ticket = ++request.current;
    setLoading(true);
    setError("");
    try {
      const [jobs, publishedRelease] = await Promise.all([listRegradeJobs(examId), loadCurrentPublishedRelease(examId)]);
      // 首次正式发布前没有可复评的来源版本，也不会有待发布的更正，不读成绩和版本列表。
      // 小题选项沿用发布页原来的取法，读取前 50 份成绩中出现的小题。
      const [grades, releases] = publishedRelease
        ? await Promise.all([listExamGrades(examId, { limit: 50 }).then(result => result.grades), listScoreReleases(examId).then(result => result.score_releases)])
        : [noGrades, []];
      if (ticket !== request.current) return;
      setSnapshot({ examId, jobs: jobs.regrade_jobs, publishedRelease, releases, grades });
    } catch (cause) {
      if (ticket === request.current) setError(getUserErrorMessage(cause, "题目级复评任务读取失败，请稍后重试"));
    } finally {
      if (ticket === request.current) setLoading(false);
    }
  }, [examId]);

  useEffect(() => {
    void load();
    return () => { request.current += 1; };
  }, [load]);

  const { actioning, setActioning, runAction } = useScoreAction({ selectedExamId: examId, loadExamList: keepExamList, loadScores: load });
  const current = snapshot?.examId === examId ? snapshot : null;
  const publishedRelease = current?.publishedRelease ?? undefined;
  const regradeWorkflow = useRegradeWorkflow({
    canManage: true, selectedExamId: examId, publishedRelease,
    grades: current?.grades ?? noGrades, runAction, setActioning
  });
  // 重读期间、读取失败后或已有提交进行中，都不能按旧任务状态再提交。
  const canWrite = Boolean(current) && !loading && !error && !actioning;

  useEffect(() => {
    // 深链接只在首次读取结束后定位一次；之后的刷新和操作不能把页面拉回面板。
    if (focusedRegradeId === undefined || loading || scrolledFor.current === focusedRegradeId) return;
    scrolledFor.current = focusedRegradeId;
    section.current?.scrollIntoView?.({ block: "start", behavior: "smooth" });
  }, [focusedRegradeId, loading]);

  const jobs = [...(current?.jobs ?? [])].sort((a, b) => Number(b.id === focusedRegradeId) - Number(a.id === focusedRegradeId));
  // 发布结果版本不会改变任务状态；是否仍待发布沿用成绩发布页的 pendingCorrections，按结果版本是否已发布判断。
  const pending = new Set(pendingCorrections(current?.jobs ?? [], current?.releases ?? []).map(item => item.job.id));
  const statusLabel = (job: RegradeJob) => job.status === "ready_for_release" && !pending.has(job.id) ? "更正已发布" : regradeStatusLabels[job.status] ?? "未知状态";
  const releasePath = (job: RegradeJob) => `/exams/${encodeURIComponent(examId)}/scores?regrade=${encodeURIComponent(job.id)}`;

  return <section id="regrade-jobs" ref={section} className="score-release-workspace" aria-labelledby="regrade-jobs-title">
    <div className="score-release-heading">
      <div>
        <h2 id="regrade-jobs-title">题目级复评</h2>
        <p>题目级复评由管理员发起，重新评阅所选小题；学生申请的成绩复核见“申诉处理”。</p>
      </div>
      <Space wrap>
        <Button icon={<RefreshCw size={15} />} loading={loading} disabled={Boolean(actioning)} onClick={() => void load()}>刷新复评任务</Button>
        {publishedRelease ? <Button icon={<GitCompareArrows size={15} />} disabled={!canWrite || regradeWorkflow.regradeQuestionOptions.length === 0} onClick={regradeWorkflow.openRegradeModal}>新建复评</Button> : null}
      </Space>
    </div>
    {error ? <Alert type="error" showIcon message={error} action={<Button size="small" disabled={loading} onClick={() => void load()}>重新读取</Button>} /> : null}
    {!current && loading ? <p className="score-release-note"><output aria-live="polite">正在读取题目级复评任务…</output></p> : null}
    {current && !publishedRelease ? <p className="score-release-note">本场成绩首次正式发布后，才能发起题目级复评。</p> : null}
    {current && publishedRelease && jobs.length === 0 ? <p className="score-release-note">当前没有题目级复评任务。</p> : null}
    {jobs.length ? <List
      size="small"
      rowKey="id"
      dataSource={jobs}
      renderItem={(job) => <List.Item className={job.id === focusedRegradeId ? "score-regrade-focused" : undefined} aria-current={job.id === focusedRegradeId ? "true" : undefined} actions={[
        ...(job.status === "awaiting_approval" ? [<Button key="approve" size="small" disabled={!canWrite} loading={actioning === "regrade-approve"} onClick={() => regradeWorkflow.transitionRegrade(job, "approve")}>批准</Button>] : []),
        ...(job.status === "approved" ? [<Button key="start" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-start"} onClick={() => regradeWorkflow.transitionRegrade(job, "start")}>启动并开放给阅卷员</Button>] : []),
        ...(job.status === "running" || job.status === "diff_review" ? [<Button key="pause" size="small" disabled={!canWrite} loading={actioning === "regrade-pause"} onClick={() => regradeWorkflow.transitionRegrade(job, "pause")}>暂停</Button>] : []),
        ...(job.status === "paused" ? [<Button key="resume" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-resume"} onClick={() => regradeWorkflow.transitionRegrade(job, "resume")}>恢复</Button>] : []),
        ...(job.status === "diff_review" ? [<Button key="finalize" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-finalize"} onClick={() => regradeWorkflow.transitionRegrade(job, "finalize")}>完成复评</Button>] : []),
        ...(job.status === "diff_review" ? [<Button key="review" size="small" disabled={!canWrite} loading={actioning === "regrade-load-review"} onClick={() => void regradeWorkflow.openRegradeReview(job)}>复核候选</Button>] : []),
        // 结果版本由成绩发布页的“发布更正”生成并发布，这里只带上任务号跳转。
        ...(pending.has(job.id) && onNavigate ? [<Button key="release" size="small" type="primary" disabled={Boolean(actioning)} onClick={() => onNavigate(releasePath(job))}>去发布更正</Button>] : [])
      ]}>
        <div className="score-release-row">
          <div><strong>题目复评 · 影响 {job.affected_count} 份</strong><span>{job.reason_text}</span></div>
          <StatusTag tone={regradeStatusTone(job.status, pending.has(job.id))}>{statusLabel(job)}</StatusTag>
        </div>
      </List.Item>}
    /> : null}
    <RegradeWorkflowModals publishedReleaseExists={Boolean(publishedRelease)} actioning={actioning} regradeWorkflow={regradeWorkflow} />
  </section>;
}
