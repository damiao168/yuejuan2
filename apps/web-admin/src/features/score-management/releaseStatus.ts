import type { RegradeJob, ScoreRelease } from "../../api/scoreReleases";
import type { RosterReport } from "../../api/scores";
import type { StatusTone } from "../../types";
import type { PublicationReadiness } from "./publicationChecklist";

export type ReleasePrimaryAction = Readonly<{ kind: "locate" | "publish" | "correction" | "export"; label: string }>;

export type ReleaseStatusModel = Readonly<{ tone: StatusTone; text: string; primary?: ReleasePrimaryAction }>;

export type ReleaseStatusInput = Readonly<{
  examId: string;
  loading: boolean;
  error: string | null;
  readiness: PublicationReadiness["status"];
  blockedGroups: number;
  publishedRelease?: ScoreRelease;
  pendingCorrections: number;
}>;

/** 发布时间按本机时区显示到分钟；服务端没有返回时间时不编造。 */
export function formatReleaseTime(value?: string | null): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/**
 * 成绩发布页唯一的状态行和主按钮。重读期间保留的旧快照不能当作当前事实，
 * 所以读取中、读取失败都不给出发布或导出入口。
 */
export function releaseStatusModel(input: ReleaseStatusInput): ReleaseStatusModel {
  if (!input.examId) return { tone: "neutral", text: "请选择考试" };
  if (input.loading) return { tone: "neutral", text: "正在核对发布状态" };
  if (input.error) return { tone: "danger", text: "发布状态暂不可用" };
  const release = input.publishedRelease;
  if (release) {
    const time = formatReleaseTime(release.published_at);
    const published = `已发布 V${release.version}${time ? ` · ${time}` : ""}`;
    if (input.pendingCorrections > 0) {
      return { tone: "warning", text: `${published} · 有 ${input.pendingCorrections} 项更正待发布`, primary: { kind: "correction", label: "发布更正" } };
    }
    return { tone: "success", text: published, primary: { kind: "export", label: "导出教师成绩表" } };
  }
  if (input.readiness === "loading") return { tone: "neutral", text: "未发布 · 正在核对" };
  if (input.readiness === "blocked") {
    return { tone: "danger", text: `未发布 · 还有 ${input.blockedGroups} 项待处理`, primary: { kind: "locate", label: `还有 ${input.blockedGroups} 项待处理` } };
  }
  if (input.readiness === "unexplained") return { tone: "danger", text: "未发布 · 检查尚未通过", primary: { kind: "locate", label: "查看检查结果" } };
  return { tone: "processing", text: "未发布 · 检查已通过", primary: { kind: "publish", label: "发布成绩" } };
}

export type PendingCorrection = Readonly<{ job: RegradeJob; draft?: ScoreRelease }>;

/** 复评任务生成的结果版本带 `regrade_job_id`；旧版本 API 不返回该字段时，按“尚未生成结果版本”处理。 */
export function releaseRegradeJobId(release: ScoreRelease): string | undefined {
  return release.regrade_job_id || undefined;
}

/**
 * 复评完成后任务停在 `ready_for_release`，发布结果版本不会改变任务状态；
 * 因此按结果版本是否已发布判断更正是否仍待处理。
 */
export function pendingCorrections(jobs: RegradeJob[], releases: ScoreRelease[]): PendingCorrection[] {
  const byJob = new Map<string, ScoreRelease>();
  for (const release of releases) {
    const jobId = releaseRegradeJobId(release);
    if (jobId) byJob.set(jobId, release);
  }
  return jobs
    .filter(job => job.status === "ready_for_release" && byJob.get(job.id)?.status !== "published")
    .map(job => ({ job, draft: byJob.get(job.id) }))
    .sort((a, b) => (a.job.finalized_at ?? a.job.updated_at).localeCompare(b.job.finalized_at ?? b.job.updated_at));
}

/** 仍在批准、阅卷或差异复核中的复评；已取消和已可发布的不计入。 */
export function activeRegradeCount(jobs: RegradeJob[]): number {
  return jobs.filter(job => job.status !== "ready_for_release" && job.status !== "cancelled").length;
}

export type RosterCounts = Readonly<{ expected: number; received: number; absent: number; withScores: number }>;

/** 有成绩的人数包括已汇总和等待发布时自动汇总的答卷；缺考由服务端按“无匹配答卷”判断。 */
export function rosterCounts(roster: RosterReport | null): RosterCounts | null {
  if (!roster) return null;
  const awaiting = (roster.entries ?? []).filter(entry => entry.resolution_code === "awaiting_aggregation").length;
  return { expected: roster.summary.expected, received: roster.summary.received, absent: roster.summary.absent, withScores: roster.summary.graded + awaiting };
}
