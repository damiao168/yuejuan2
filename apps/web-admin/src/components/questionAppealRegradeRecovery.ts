import type { PublishedQuestionAppeal } from "../api/questionAppeals";
import type { RegradeJob } from "../api/scoreReleases";

export function matchingAppealRegradeDrafts(jobs: RegradeJob[], appeal: Pick<PublishedQuestionAppeal, "id" | "exam_id" | "question_id" | "source_release_id" | "submission_id">, actorID: string, response: string) {
  // 只回认当前账号为同一冻结单份答卷创建的人工复评；整题范围、他人任务和相似说明不能代替关联证据。
  if (!actorID) return [];
  return jobs.filter(job => job.status === "draft" && job.created_by === actorID && job.exam_id === appeal.exam_id && job.question_id === appeal.question_id
    && job.source_release_id === appeal.source_release_id && job.reason_code === "appeal_pattern" && (!response.trim() || job.reason_text.trim() === response.trim())
    && job.strategy === "human_recheck" && job.affected_count === 1 && job.selector?.submission_ids?.length === 1 && job.selector.submission_ids[0] === appeal.submission_id
    && !job.selector.score_band && !job.new_policy_version && !job.new_rubric_snapshot_id && !job.severity_delta);
}
