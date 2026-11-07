import { useEffect, useMemo, useState } from "react";
import { ApiClientError } from "../../../api/client";
import type { Exam } from "../../../api/exams";
import { getCurrentPublishedScoreRelease } from "../../../api/scoreReleases";
import { examSubjectLabel } from "../../../constants/examStatus";

export type SessionPublication = Readonly<{
  /** 同场（同一 exam_session_id）当前账号可见的学科数，包括当前学科。 */
  total: number;
  published: number;
  missingSubjects: string[];
  /** 有学科的发布状态读取失败；此时不据此禁用导出，由服务端导出时再核对。 */
  unknown: boolean;
  loading: boolean;
}>;

async function isPublished(examId: string, signal: AbortSignal): Promise<boolean> {
  try {
    await getCurrentPublishedScoreRelease(examId, signal);
    return true;
  } catch (error) {
    // 只有“尚未发布”属于确定结果；其它失败按未知处理，不能误报为未发布。
    if (error instanceof ApiClientError && error.status === 404 && error.code === "score_release_not_found") return false;
    throw error;
  }
}

/**
 * 教师成绩表要求同场各科都已发布（服务端 `formal_export_incomplete`）。这里只用于提前说明还差哪些学科；
 * 只统计当前账号能看到的考试，冻结名册等其它条件仍以导出接口的判断为准。本科尚未发布时返回 null。
 */
export function useSessionPublication(exams: Exam[], selectedExam: Exam | undefined, currentPublished: boolean, enabled: boolean): SessionPublication | null {
  const siblings = useMemo(() => {
    if (!selectedExam?.exam_session_id) return selectedExam ? [selectedExam] : [];
    return exams.filter(exam => exam.exam_session_id === selectedExam.exam_session_id);
  }, [exams, selectedExam]);
  const selectedId = selectedExam?.id ?? "";
  const otherIds = siblings.filter(exam => exam.id !== selectedId).map(exam => exam.id).join(",");
  const requestKey = `${selectedId}|${otherIds}|${currentPublished}`;
  const [state, setState] = useState<{ key: string; published: Record<string, boolean>; unknown: boolean } | null>(null);

  useEffect(() => {
    // 本科尚未发布时导出按钮已禁用，不必读取其它学科；请求以字符串键区分，迟到结果不会写入另一场考试。
    const ids = otherIds ? otherIds.split(",") : [];
    if (!enabled || !selectedId || !currentPublished || ids.length === 0) return;
    const key = `${selectedId}|${otherIds}|${currentPublished}`;
    const controller = new AbortController();
    void Promise.allSettled(ids.map(id => isPublished(id, controller.signal))).then(results => {
      if (controller.signal.aborted) return;
      const published: Record<string, boolean> = {};
      let unknown = false;
      results.forEach((result, index) => {
        if (result.status === "fulfilled") published[ids[index]] = result.value;
        else unknown = true;
      });
      setState({ key, published, unknown });
    });
    return () => controller.abort();
  }, [enabled, selectedId, otherIds, currentPublished]);

  if (!enabled || !selectedExam || !currentPublished) return null;
  if (siblings.length <= 1) return { total: 1, published: 1, missingSubjects: [], unknown: false, loading: false };
  const current = state?.key === requestKey ? state : null;
  if (!current) return { total: siblings.length, published: 1, missingSubjects: [], unknown: true, loading: true };
  const missing = siblings.filter(exam => exam.id !== selectedExam.id && current.published[exam.id] === false);
  const known = siblings.filter(exam => exam.id === selectedExam.id || current.published[exam.id] !== undefined);
  return {
    total: siblings.length,
    published: known.length - missing.length,
    missingSubjects: missing.map(exam => examSubjectLabel(exam.subject)),
    unknown: current.unknown,
    loading: false
  };
}

/** 同场说明文字；单科考试不显示。 */
export function sessionPublicationText(value: SessionPublication | null): string {
  if (!value || value.total <= 1 || value.loading) return "";
  if (value.missingSubjects.length) return `同场 ${value.total} 科已发布 ${value.published} 科，还差${value.missingSubjects.join("、")}`;
  if (value.unknown) return "同场其他学科的发布状态暂未读取完整，导出时会再次核对";
  return `同场 ${value.total} 科均已发布`;
}
