import { Alert, Button, Empty, List, Space } from "antd";
import { GitCompareArrows } from "lucide-react";
import { getSafeUserText } from "../../../api/client";
import type { RegradeJob, ScoreRelease, ScoreReleaseGate } from "../../../api/scoreReleases";
import { StatusTag } from "../../../components/StatusTag";
import { InternalRouteButton } from "../../../components/InternalRouteButton";
import type { StatusTone } from "../../../types";
import type { useRegradeWorkflow } from "../hooks/useRegradeWorkflow";
import type { useScoreReleaseWorkflow } from "../hooks/useScoreReleaseWorkflow";

const scoreReleaseStatusLabels: Record<string, string> = { draft: "草稿", published: "已发布" };
const scoreReleaseSourceLabels: Record<string, string> = {
  initial: "初次发布",
  regrade: "复评更正",
  appeal: "申诉更正",
  rollback: "回退版本",
  migration: "历史迁移"
};
const regradeStatusLabels: Record<string, string> = {
  awaiting_approval: "待批准",
  approved: "已批准",
  running: "复评中",
  paused: "已暂停",
  diff_review: "差异复核",
  ready_for_release: "可生成新版本",
  cancelled: "已取消"
};

function scoreReleaseTone(status: string): StatusTone {
  return status === "published" ? "success" : "processing";
}

function releaseGateTone(gate: ScoreReleaseGate | null): StatusTone {
  if (!gate) return "neutral";
  return gate.passed ? "success" : "danger";
}

function releaseGateLabel(gate: ScoreReleaseGate | null): string {
  if (!gate) return "门禁待读取";
  return gate.passed ? "门禁通过" : "门禁待处理";
}

function releaseStatusLabel(release: ScoreRelease, publishedRelease?: ScoreRelease): string {
  if (publishedRelease?.id === release.id) return "门户当前版本";
  if (release.status === "published") return "历史已发布";
  return scoreReleaseStatusLabels[release.status] ?? "未知状态";
}

function regradeStatusTone(status: RegradeJob["status"]): StatusTone {
  if (status === "ready_for_release") return "success";
  if (status === "cancelled") return "neutral";
  return "processing";
}

export function ScoreReleaseWorkspace({
  canWrite,
  selectedExamId,
  gradeTotal,
  releaseGate,
  scoreReleases,
  regradeJobs,
  publishedRelease,
  actioning,
  releaseWorkflow,
  regradeWorkflow,
  onPreviewRelease,
  portalUrl
}: Readonly<{
  canWrite: boolean;
  selectedExamId: string;
  gradeTotal: number;
  releaseGate: ScoreReleaseGate | null;
  scoreReleases: ScoreRelease[];
  regradeJobs: RegradeJob[];
  publishedRelease?: ScoreRelease;
  actioning: string | null;
  releaseWorkflow: Pick<ReturnType<typeof useScoreReleaseWorkflow>, "openReleaseModal" | "publishRelease" | "revokeSharedPaper">;
  regradeWorkflow: Pick<ReturnType<typeof useRegradeWorkflow>, "openRegradeModal" | "regradeQuestionOptions" | "transitionRegrade" | "openRegradeReview" | "materializeRegradeRelease">;
  onPreviewRelease?: (release: ScoreRelease) => void;
  portalUrl?: string;
}>) {
  const { openReleaseModal, publishRelease, revokeSharedPaper } = releaseWorkflow;
  const {
    openRegradeModal, regradeQuestionOptions, transitionRegrade,
    openRegradeReview, materializeRegradeRelease
  } = regradeWorkflow;

  return <section className="score-release-workspace">
    <div className="score-release-heading">
      <div>
        <h2>学生门户成绩版本</h2>
        <p>推荐流程：汇总并确认成绩 → 创建门户草稿 → 核对学生视角 → 发布到学生门户。创建草稿不会改变学生所见。</p>
        <p>目标入口：{portalUrl ? <a href={portalUrl} target="_blank" rel="noreferrer">学生门户 · {portalUrl}</a> : "学生门户（入口链接尚未配置，请使用学校发放的地址）"}；学校原有成绩查询入口按需独立公开。</p>
      </div>
      <Space wrap>
        <StatusTag tone={releaseGateTone(releaseGate)}>{releaseGateLabel(releaseGate)}</StatusTag>
        <Button disabled={!canWrite || !selectedExamId || gradeTotal === 0} onClick={openReleaseModal}>创建门户版本草稿</Button>
      </Space>
    </div>

    {!releaseGate && <Alert type="info" showIcon message="等待发布门禁数据" description="门禁结果读取完成后才能发布版本。" />}
    {releaseGate && (!releaseGate.passed || releaseGate.warnings.length > 0) && <List
      className="score-release-gate-list"
      size="small"
      dataSource={[...releaseGate.blocking, ...releaseGate.warnings]}
      renderItem={(issue) => <List.Item>
        <div className="score-release-gate-row">
          <StatusTag tone={issue.blocking ? "danger" : "warning"}>{issue.blocking ? "阻断" : "提醒"}</StatusTag>
          <div><strong>{getSafeUserText(issue.message, "成绩质量检查未通过")}</strong><span>{issue.count} 项 · {issue.code}</span></div>
          {issue.action_route === "quality" ? <InternalRouteButton size="small" href={selectedExamId ? `/admin/exams/${encodeURIComponent(selectedExamId)}/quality` : undefined}>查看质量</InternalRouteButton> : null}
          {issue.action_route === "questions" ? <InternalRouteButton size="small" href={selectedExamId ? `/admin/exams/${encodeURIComponent(selectedExamId)}/questions` : undefined}>配置题目</InternalRouteButton> : null}
          {issue.action_route === "regrade" ? <Button size="small" onClick={openRegradeModal}>查看复评</Button> : null}
        </div>
      </List.Item>}
    />}
    {releaseGate?.passed && releaseGate.warnings.length === 0 && <Alert type="success" showIcon message="当前发布门禁通过" description="创建草稿后，仍会在实际发布时再次核验，避免状态变化后误发布。" />}

    <div className="score-release-columns">
      <section>
        <div className="score-release-subhead"><h3>门户版本记录</h3><span>{scoreReleases.length} 个版本</span></div>
        {scoreReleases.length ? <List
          size="small"
          dataSource={scoreReleases}
          renderItem={(release) => {
            const actions: React.ReactNode[] = onPreviewRelease ? [<Button key="preview" size="small" disabled={!canWrite} onClick={() => onPreviewRelease(release)}>学生视角预览 V{release.version}</Button>] : [];
            if (release.status === "draft") {
              actions.push(<Button key="publish" size="small" type="primary" disabled={!canWrite || !releaseGate?.passed} loading={actioning === "release-publish"} onClick={() => publishRelease(release)}>发布到门户 V{release.version}</Button>);
            } else if (publishedRelease?.id === release.id && release.visibility_policy.show_high_score_paper) {
              actions.push(<Button key="revoke-paper" size="small" danger disabled={!canWrite} loading={actioning === "release-high-score-revoke"} onClick={() => revokeSharedPaper(release)}>撤回范例卷分享</Button>);
            }
            return <List.Item actions={actions}>
            <div className="score-release-row">
              <div><strong>V{release.version} · {scoreReleaseSourceLabels[release.source] ?? "其他来源"}</strong><span>{release.reason}</span></div>
              <StatusTag tone={scoreReleaseTone(release.status)}>{releaseStatusLabel(release, publishedRelease)}</StatusTag>
            </div>
          </List.Item>;
          }}
        /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未创建正式成绩版本" />}
      </section>

      <section>
        <div className="score-release-subhead"><h3>题目级复评</h3><Button size="small" icon={<GitCompareArrows size={15} />} disabled={!canWrite || !publishedRelease || regradeQuestionOptions.length === 0} onClick={openRegradeModal}>新建复评</Button></div>
        <p className="score-release-note">复评只处理指定题目，完成后必须生成并发布新的成绩版本，不会直接改分。</p>
        {regradeJobs.length ? <List
          size="small"
          dataSource={regradeJobs}
          renderItem={(job) => <List.Item actions={[
            ...(job.status === "awaiting_approval" ? [<Button key="approve" size="small" disabled={!canWrite} loading={actioning === "regrade-approve"} onClick={() => transitionRegrade(job, "approve")}>批准</Button>] : []),
            ...(job.status === "approved" ? [<Button key="start" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-start"} onClick={() => transitionRegrade(job, "start")}>启动并开放给阅卷员</Button>] : []),
            ...(job.status === "running" || job.status === "diff_review" ? [<Button key="pause" size="small" disabled={!canWrite} loading={actioning === "regrade-pause"} onClick={() => transitionRegrade(job, "pause")}>暂停</Button>] : []),
            ...(job.status === "paused" ? [<Button key="resume" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-resume"} onClick={() => transitionRegrade(job, "resume")}>恢复</Button>] : []),
            ...(job.status === "diff_review" ? [<Button key="finalize" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-finalize"} onClick={() => transitionRegrade(job, "finalize")}>完成复评</Button>] : []),
            ...(job.status === "diff_review" ? [<Button key="review" size="small" disabled={!canWrite} loading={actioning === "regrade-load-review"} onClick={() => void openRegradeReview(job)}>复核候选</Button>] : []),
            ...(job.status === "ready_for_release" ? [<Button key="release" size="small" type="primary" disabled={!canWrite} loading={actioning === "regrade-release"} onClick={() => materializeRegradeRelease(job)}>生成新版本</Button>] : [])
          ]}>
            <div className="score-release-row">
              <div><strong>题目复评 · 影响 {job.affected_count} 份</strong><span>{job.reason_text}</span></div>
              <StatusTag tone={regradeStatusTone(job.status)}>{regradeStatusLabels[job.status] ?? "未知状态"}</StatusTag>
            </div>
          </List.Item>}
        /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={publishedRelease ? "当前没有题目复评任务" : "发布首个成绩版本后，可发起题目级复评"} />}
      </section>
    </div>
  </section>;
}
