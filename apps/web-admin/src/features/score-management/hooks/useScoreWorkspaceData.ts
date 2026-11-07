import { useCallback, useEffect, useReducer, useRef, type SetStateAction } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { listAuditLogs } from "../../../api/audit";
import { listAllExams } from "../../../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../../../api/org";
import {
  checkExamGradeQuality,
  listExamGrades,
  listExamRoster,
  type RosterReport
} from "../../../api/scores";
import {
  getCurrentPublishedScoreRelease,
  getScoreReleaseGate,
  listRegradeJobs,
  listScoreReleases,
} from "../../../api/scoreReleases";
import { listSubmissions } from "../../../api/submissions";
import { hashQueryParam } from "../../../router/query";
import { clearRememberedScoreExamSelection, isScoreExamSelectionLocation, readScoreExamSelection, rememberScoreExamSelection } from "./scoreExamSelection";
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

function requiredResult<T>(result: PromiseSettledResult<T>): T {
  if (result.status === "rejected") throw result.reason;
  return result.value;
}

async function loadCurrentPublishedRelease(examId: string) {
  try {
    return (await getCurrentPublishedScoreRelease(examId)).release;
  } catch (error) {
    // 仅“尚未发布”属于正常空状态；其他读取失败不能伪装成门户没有版本。
    if (error instanceof ApiClientError && error.status === 404 && error.code === "score_release_not_found") return null;
    throw error;
  }
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
  initialExamId,
  selectionScope,
  resetUnavailableRememberedSelection = false
}: {
  canManage: boolean;
  canReadAudit: boolean;
  canReadStudentNames: boolean;
  initialExamId: string;
  selectionScope?: string;
  resetUnavailableRememberedSelection?: boolean;
}) {
  const { message } = App.useApp();
  // 考试内嵌页面始终由路径指定考试；只有总成绩入口恢复当前身份上次选择的考试。
  const scopedSelection = initialExamId ? undefined : selectionScope;
  const [state, dispatch] = useReducer(scoreWorkspaceReducer, undefined, () => initialScoreWorkspaceState(
    initialExamId || (scopedSelection ? readScoreExamSelection(scopedSelection) : "")
  ));
  // 失效的恢复目标保留到用户重选，防止刷新后默选首场；异步读取另跟随最新人工选择。
  const requestedExamRef = useRef(state.selectedExamId);
  const selectedExamRef = useRef(state.selectedExamId);
  const visibleExamIdsRef = useRef<Set<string> | null>(null);
  selectedExamRef.current = state.selectedExamId;
  // 全量刷新和翻页共用代次；新请求开始后，旧请求即使成功也不能写回当前快照。
  const scoreRequestsRef = useRef(new LatestRequestController());
  const { selectedExamId, snapshot, appliedKeyword, statusFilter } = state;

  const setSelectedExamId = useCallback((value: SetStateAction<string>) => {
    const examId = typeof value === "function" ? value(selectedExamRef.current) : value;
    selectedExamRef.current = examId;
    requestedExamRef.current = examId;
    dispatch({ type: "selectExam", examId });
  }, []);
  const setKeyword = useCallback((value: string) => dispatch({ type: "patch", patch: { keyword: value } }), []);
  const setAppliedKeyword = useCallback((value: string) => dispatch({ type: "patch", patch: { appliedKeyword: value } }), []);
  const setStatusFilter = useCallback((value: string) => dispatch({ type: "patch", patch: { statusFilter: value } }), []);
  const setRoster = useCallback((roster: RosterReport | null) => dispatch({ type: "setRoster", roster }), []);

  const loadExamList = useCallback(async () => {
    visibleExamIdsRef.current = null;
    dispatch({ type: "patch", patch: { loadingExams: true, error: null } });
    try {
      const result = await listAllExams();
      visibleExamIdsRef.current = new Set(result.exams.map((exam) => exam.id));
      // 学校管理员的旧个人记忆可能来自范围收紧前；裸入口先清理失效记忆，再从本校目录选择。
      // 显式深链仍提示无权，不能悄悄把指定考试换成另一场并开放操作。
      if (resetUnavailableRememberedSelection && scopedSelection && !hashQueryParam("exam")
        && requestedExamRef.current && !visibleExamIdsRef.current.has(requestedExamRef.current)) {
        clearRememberedScoreExamSelection(scopedSelection);
        requestedExamRef.current = "";
      }
      dispatch({
        type: "examsLoaded", exams: result.exams, initialExamId,
        requestedStatus: hashQueryParam("status") || "",
        requestedExamId: scopedSelection ? requestedExamRef.current : undefined
      });
    } catch (currentError) {
      dispatch({ type: "patch", patch: { error: formatError(currentError) } });
    } finally {
      dispatch({ type: "patch", patch: { loadingExams: false } });
    }
  }, [initialExamId, scopedSelection, resetUnavailableRememberedSelection]);

  const loadScores = useCallback(async (examId: string) => {
    // 刷新或操作后的回调可能仍携带上一场 ID；不能用它淘汰当前考试请求，也不能读取目录已移除的考试。
    if (scopedSelection && (examId !== selectedExamRef.current || !visibleExamIdsRef.current?.has(examId))) return;
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
        canManage ? loadCurrentPublishedRelease(examId) : Promise.resolve(null),
        canManage ? listRegradeJobs(examId) : Promise.resolve({ regrade_jobs: [] })
      ]);
      if (!request.isCurrent()) return;
      const [submissionResult, gradeResult, qualityResult, rosterResult, auditResult, releaseGateResult, releasesResult, currentReleaseResult, regradesResult] = results;
      // 必需数据仍按原顺序校验；全部通过后才读身份并发布完整快照，审计读取失败单独降级。
      const submissionData = requiredResult(submissionResult);
      const gradeData = requiredResult(gradeResult);
      const qualityData = requiredResult(qualityResult);
      const rosterData = requiredResult(rosterResult);
      const releaseGateData = requiredResult(releaseGateResult);
      const releasesData = requiredResult(releasesResult);
      const currentRelease = requiredResult(currentReleaseResult);
      const regradesData = requiredResult(regradesResult);
      const identityResult = await loadIdentities(
        canReadStudentNames,
        gradeData.grades.flatMap((grade) => grade.student_id ? [grade.student_id] : [])
      );
      if (!request.isCurrent()) return;
      const complete: ScoreWorkspaceSnapshot = {
        examId, submissions: submissionData.submissions,
        grades: gradeData.grades, gradeTotal: gradeData.total,
        filteredGradeTotal: gradeData.filtered_total,
        allGradesLocked: gradeData.all_locked,
        gradeNextCursor: gradeData.next_cursor,
        gradesHaveMore: gradeData.has_more,
        quality: qualityData, roster: rosterData.roster,
        identities: identityResult,
        auditLogs: auditResult.status === "fulfilled" ? auditResult.value.audit_logs : [],
        releaseGate: releaseGateData.release_gate,
        scoreReleases: releasesData.score_releases,
        currentPublishedRelease: currentRelease,
        regradeJobs: regradesData.regrade_jobs
      };
      dispatch({ type: "loadSucceeded", examId, snapshot: complete });
    } catch (currentError) {
      if (request.isCurrent()) dispatch({ type: "loadFailed", examId, error: formatError(currentError) });
    }
  }, [appliedKeyword, canManage, canReadAudit, canReadStudentNames, scopedSelection, statusFilter]);

  useEffect(() => { void loadExamList(); }, [loadExamList]);
  useEffect(() => { if (initialExamId) setSelectedExamId(initialExamId); }, [initialExamId, setSelectedExamId]);
  const locationExamId = scopedSelection && isScoreExamSelectionLocation() ? hashQueryParam("exam") : null;
  const lastLocationExamRef = useRef(locationExamId);
  useEffect(() => {
    if (!scopedSelection || locationExamId === null) return;
    const restore = () => {
      if (!isScoreExamSelectionLocation()) return;
      const requested = hashQueryParam("exam");
      if (requested === lastLocationExamRef.current) return;
      lastLocationExamRef.current = requested;
      const examId = readScoreExamSelection(scopedSelection);
      requestedExamRef.current = examId;
      selectedExamRef.current = examId;
      dispatch({ type: "restoreExam", examId });
    };
    // 同一路径的前进/后退不会重新挂载页面，仍须按历史地址恢复考试并清除旧成绩。
    restore();
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, [locationExamId, scopedSelection]);
  useEffect(() => {
    if (!scopedSelection || state.loadingExams || !selectedExamId || state.error
      || !state.exams.some((exam) => exam.id === selectedExamId)) return;
    // 地址先变化时，必须等待对应考试接管状态；不能用上一场的状态把新地址改回去。
    if (requestedExamRef.current && requestedExamRef.current !== selectedExamId) return;
    requestedExamRef.current = selectedExamId;
    lastLocationExamRef.current = selectedExamId;
    rememberScoreExamSelection(scopedSelection, selectedExamId);
  }, [scopedSelection, selectedExamId, state.error, state.exams, state.loadingExams]);
  const selectionValidated = !scopedSelection || (!state.loadingExams && Boolean(selectedExamId)
    && state.exams.some((exam) => exam.id === selectedExamId));
  useEffect(() => {
    // 记忆和地址只是定位线索，最新考试目录核实可访问后才读取成绩。
    if (!selectionValidated) {
      scoreRequestsRef.current.invalidate();
      return;
    }
    void loadScores(selectedExamId);
  }, [loadScores, selectedExamId, selectionValidated]);
  useEffect(() => () => scoreRequestsRef.current.invalidate(), []);

  const refresh = async () => {
    await loadExamList();
    // 等待目录期间用户可能已切换考试，恢复读取必须跟随最新选择。
    if (selectedExamRef.current) await loadScores(selectedExamRef.current);
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
    currentPublishedRelease: snapshot.currentPublishedRelease,
    regradeJobs: snapshot.regradeJobs, roster: snapshot.roster, setRoster,
    identities: snapshot.identities, auditLogs: snapshot.auditLogs,
    keyword: state.keyword, setKeyword, appliedKeyword, setAppliedKeyword,
    statusFilter, setStatusFilter, loadingExams: state.loadingExams,
    loadingScores: state.loadingScores, loadingMoreGrades: state.loadingMoreGrades,
    error: state.error, loadExamList, loadScores, refresh, loadMoreGrades,
    gradesHaveMore: snapshot.gradesHaveMore
  };
}
