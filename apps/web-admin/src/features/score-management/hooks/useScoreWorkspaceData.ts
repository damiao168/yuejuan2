import { useCallback, useEffect, useReducer, useRef, type SetStateAction } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { listAuditLogs } from "../../../api/audit";
import { listExams } from "../../../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../../../api/org";
import {
  checkExamGradeQuality,
  listExamGrades,
  listExamRoster,
  type RosterReport
} from "../../../api/scores";
import {
  getScoreReleaseGate,
  listRegradeJobs,
  listScoreReleases,
} from "../../../api/scoreReleases";
import { listSubmissions } from "../../../api/submissions";
import { hashQueryParam } from "../../../router/query";
import { LatestRequestController } from "../../shared/latestRequest";
import {
  emptyScoreWorkspaceSnapshot, initialScoreWorkspaceState, scoreWorkspaceReducer,
  type ScoreWorkspaceSnapshot
} from "./scoreWorkspaceState";

export interface ScoreIdentityMaps {
  students: Record<string, Student>;
  classes: Record<string, SchoolClass>;
  error?: string;
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

async function loadIdentities(canReadStudentNames: boolean, studentIds: string[]): Promise<ScoreIdentityMaps> {
  const ids = [...new Set(studentIds.filter(Boolean))];
  if (!canReadStudentNames || ids.length === 0) return { students: {}, classes: {} };

  const [studentsResult, classesResult] = await Promise.allSettled([
    listStudents({ ids, limit: 200 }),
    listClasses()
  ]);
  const maps: ScoreIdentityMaps = { students: {}, classes: {} };
  if (studentsResult.status === "fulfilled") {
    for (const student of studentsResult.value.students) maps.students[student.id] = student;
  } else {
    maps.error = formatError(studentsResult.reason);
  }
  if (classesResult.status === "fulfilled") {
    for (const item of classesResult.value.classes) maps.classes[item.id] = item;
  } else {
    maps.error = [maps.error, formatError(classesResult.reason)].filter(Boolean).join("；");
  }
  return maps;
}

export function useScoreWorkspaceData({
  canManage,
  canReadAudit,
  canReadStudentNames,
  initialExamId
}: {
  canManage: boolean;
  canReadAudit: boolean;
  canReadStudentNames: boolean;
  initialExamId: string;
}) {
  const { message } = App.useApp();
  const [state, dispatch] = useReducer(scoreWorkspaceReducer, initialExamId, initialScoreWorkspaceState);
  // 全量刷新和翻页共用代次；新请求开始后，旧请求即使成功也不能写回当前快照。
  const scoreRequestsRef = useRef(new LatestRequestController());
  const { selectedExamId, snapshot, appliedKeyword, statusFilter } = state;

  const setSelectedExamId = useCallback((value: SetStateAction<string>) => {
    dispatch({ type: "selectExamUpdate", value });
  }, []);
  const setKeyword = useCallback((value: string) => dispatch({ type: "patch", patch: { keyword: value } }), []);
  const setAppliedKeyword = useCallback((value: string) => dispatch({ type: "patch", patch: { appliedKeyword: value } }), []);
  const setStatusFilter = useCallback((value: string) => dispatch({ type: "patch", patch: { statusFilter: value } }), []);
  const setRoster = useCallback((roster: RosterReport | null) => dispatch({ type: "setRoster", roster }), []);

  const loadExamList = useCallback(async () => {
    dispatch({ type: "patch", patch: { loadingExams: true, error: null } });
    try {
      const result = await listExams();
      dispatch({
        type: "examsLoaded", exams: result.exams, initialExamId,
        requestedStatus: hashQueryParam("status") || ""
      });
    } catch (currentError) {
      dispatch({ type: "patch", patch: { error: formatError(currentError) } });
    } finally {
      dispatch({ type: "patch", patch: { loadingExams: false } });
    }
  }, [initialExamId]);

  const loadScores = useCallback(async (examId: string) => {
    const request = scoreRequestsRef.current.begin(examId);
    dispatch({ type: "loadStarted", examId });
    if (!examId) {
      dispatch({ type: "loadSucceeded", examId, snapshot: emptyScoreWorkspaceSnapshot() });
      return;
    }
    try {
      const results = await Promise.allSettled([
        listSubmissions(examId, { limit: 50 }),
        listExamGrades(examId, {
          status: statusFilter === "all" ? undefined : statusFilter,
          q: appliedKeyword || undefined,
          limit: 50
        }),
        checkExamGradeQuality(examId, "publish"),
        canManage ? listExamRoster(examId) : Promise.resolve({ roster: null }),
        canReadAudit ? listAuditLogs({ target_type: "exam", target_id: examId, limit: 20 }) : Promise.resolve({ audit_logs: [] }),
        canManage ? getScoreReleaseGate(examId) : Promise.resolve({ release_gate: null }),
        canManage ? listScoreReleases(examId) : Promise.resolve({ score_releases: [] }),
        canManage ? listRegradeJobs(examId) : Promise.resolve({ regrade_jobs: [] })
      ]);
      if (!request.isCurrent()) return;
      const [submissionResult, gradeResult, qualityResult, rosterResult, auditResult, releaseGateResult, releasesResult, regradesResult] = results;
      // Validate all required data before publishing any part of the workspace.
      if (submissionResult.status !== "fulfilled") throw submissionResult.reason;
      if (gradeResult.status !== "fulfilled") throw gradeResult.reason;
      if (qualityResult.status !== "fulfilled") throw qualityResult.reason;
      if (rosterResult.status !== "fulfilled") throw rosterResult.reason;
      if (releaseGateResult.status !== "fulfilled") throw releaseGateResult.reason;
      if (releasesResult.status !== "fulfilled") throw releasesResult.reason;
      if (regradesResult.status !== "fulfilled") throw regradesResult.reason;
      const identityResult = await loadIdentities(
        canReadStudentNames,
        gradeResult.value.grades.flatMap((grade) => grade.student_id ? [grade.student_id] : [])
      );
      if (!request.isCurrent()) return;
      const complete: ScoreWorkspaceSnapshot = {
        examId, submissions: submissionResult.value.submissions,
        grades: gradeResult.value.grades, gradeTotal: gradeResult.value.total,
        filteredGradeTotal: gradeResult.value.filtered_total,
        allGradesLocked: gradeResult.value.all_locked,
        gradeNextCursor: gradeResult.value.next_cursor,
        gradesHaveMore: gradeResult.value.has_more,
        quality: qualityResult.value, roster: rosterResult.value.roster,
        identities: identityResult,
        auditLogs: auditResult.status === "fulfilled" ? auditResult.value.audit_logs : [],
        releaseGate: releaseGateResult.value.release_gate,
        scoreReleases: releasesResult.value.score_releases,
        regradeJobs: regradesResult.value.regrade_jobs
      };
      dispatch({ type: "loadSucceeded", examId, snapshot: complete });
    } catch (currentError) {
      if (request.isCurrent()) dispatch({ type: "loadFailed", examId, error: formatError(currentError) });
    }
  }, [appliedKeyword, canManage, canReadAudit, canReadStudentNames, statusFilter]);

  useEffect(() => { void loadExamList(); }, [loadExamList]);
  useEffect(() => { if (initialExamId) setSelectedExamId(initialExamId); }, [initialExamId, setSelectedExamId]);
  useEffect(() => { void loadScores(selectedExamId); }, [loadScores, selectedExamId]);
  useEffect(() => () => scoreRequestsRef.current.invalidate(), []);

  const refresh = async () => {
    await loadExamList();
    if (selectedExamId) await loadScores(selectedExamId);
  };

  const loadMoreGrades = async () => {
    if (!selectedExamId || snapshot.examId !== selectedExamId || !snapshot.gradesHaveMore
      || !snapshot.gradeNextCursor || state.loadingMoreGrades || state.loadingScores) return;
    const expectedCursor = snapshot.gradeNextCursor;
    const request = scoreRequestsRef.current.begin(selectedExamId);
    dispatch({ type: "patch", patch: { loadingMoreGrades: true } });
    try {
      const result = await listExamGrades(selectedExamId, {
        status: statusFilter === "all" ? undefined : statusFilter,
        q: appliedKeyword || undefined,
        limit: 50,
        cursor: expectedCursor
      });
      const identityResult = await loadIdentities(
        canReadStudentNames,
        result.grades.flatMap((grade) => grade.student_id ? [grade.student_id] : [])
      );
      if (!request.isCurrent()) return;
      dispatch({
        type: "appendGrades", examId: selectedExamId, expectedCursor,
        grades: result.grades, total: result.total, filteredTotal: result.filtered_total,
        allLocked: result.all_locked, nextCursor: result.next_cursor,
        hasMore: result.has_more, identities: identityResult
      });
    } catch (currentError) {
      if (request.isCurrent()) message.error(formatError(currentError));
    } finally {
      if (request.isCurrent()) dispatch({ type: "patch", patch: { loadingMoreGrades: false } });
    }
  };

  return {
    exams: state.exams, selectedExamId, setSelectedExamId,
    submissions: snapshot.submissions, grades: snapshot.grades,
    gradeTotal: snapshot.gradeTotal, filteredGradeTotal: snapshot.filteredGradeTotal,
    allGradesLocked: snapshot.allGradesLocked, quality: snapshot.quality,
    releaseGate: snapshot.releaseGate, scoreReleases: snapshot.scoreReleases,
    regradeJobs: snapshot.regradeJobs, roster: snapshot.roster, setRoster,
    identities: snapshot.identities, auditLogs: snapshot.auditLogs,
    keyword: state.keyword, setKeyword, appliedKeyword, setAppliedKeyword,
    statusFilter, setStatusFilter, loadingExams: state.loadingExams,
    loadingScores: state.loadingScores, loadingMoreGrades: state.loadingMoreGrades,
    error: state.error, loadExamList, loadScores, refresh, loadMoreGrades,
    gradesHaveMore: snapshot.gradesHaveMore
  };
}
