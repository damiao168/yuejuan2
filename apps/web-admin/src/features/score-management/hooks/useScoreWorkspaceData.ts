import { useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { listAuditLogs, type AuditLog } from "../../../api/audit";
import { listExams, type Exam } from "../../../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../../../api/org";
import {
  checkExamGradeQuality,
  listExamGrades,
  listExamRoster,
  type QualityCheckResult,
  type RosterReport,
  type SubmissionGrade
} from "../../../api/scores";
import {
  getScoreReleaseGate,
  listRegradeJobs,
  listScoreReleases,
  type RegradeJob,
  type ScoreRelease,
  type ScoreReleaseGate
} from "../../../api/scoreReleases";
import { listSubmissions, type Submission } from "../../../api/submissions";
import { hashQueryParam } from "../../../router/query";
import { LatestRequestController } from "../../shared/latestRequest";

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
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState(initialExamId);
  const [submissions, setSubmissions] = useState<Submission[]>([]);
  const [grades, setGrades] = useState<SubmissionGrade[]>([]);
  const [gradeTotal, setGradeTotal] = useState(0);
  const [filteredGradeTotal, setFilteredGradeTotal] = useState(0);
  const [allGradesLocked, setAllGradesLocked] = useState(false);
  const [gradeNextCursor, setGradeNextCursor] = useState("");
  const [gradesHaveMore, setGradesHaveMore] = useState(false);
  const [quality, setQuality] = useState<QualityCheckResult | null>(null);
  const [releaseGate, setReleaseGate] = useState<ScoreReleaseGate | null>(null);
  const [scoreReleases, setScoreReleases] = useState<ScoreRelease[]>([]);
  const [regradeJobs, setRegradeJobs] = useState<RegradeJob[]>([]);
  const [roster, setRoster] = useState<RosterReport | null>(null);
  const [identities, setIdentities] = useState<ScoreIdentityMaps>({ students: {}, classes: {} });
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [keyword, setKeyword] = useState("");
  const [appliedKeyword, setAppliedKeyword] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [loadingExams, setLoadingExams] = useState(true);
  const [loadingScores, setLoadingScores] = useState(false);
  const [loadingMoreGrades, setLoadingMoreGrades] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const scoreRequestsRef = useRef(new LatestRequestController());

  const loadExamList = useCallback(async () => {
    setLoadingExams(true);
    setError(null);
    try {
      const result = await listExams();
      setExams(result.exams);
      setSelectedExamId((current) => {
        if (initialExamId && result.exams.some((exam) => exam.id === initialExamId)) return initialExamId;
        const requestedStatus = hashQueryParam("status");
        const requestedExam = requestedStatus ? result.exams.find((exam) => exam.status === requestedStatus) : undefined;
        if (requestedExam) return requestedExam.id;
        return result.exams.some((exam) => exam.id === current) ? current : result.exams[0]?.id || "";
      });
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoadingExams(false);
    }
  }, [initialExamId]);

  const loadScores = useCallback(async (examId: string) => {
    const request = scoreRequestsRef.current.begin(examId);
    if (!examId) {
      setSubmissions([]);
      setGrades([]);
      setGradeTotal(0);
      setFilteredGradeTotal(0);
      setAllGradesLocked(false);
      setGradeNextCursor("");
      setGradesHaveMore(false);
      setQuality(null);
      setReleaseGate(null);
      setScoreReleases([]);
      setRegradeJobs([]);
      setRoster(null);
      setAuditLogs([]);
      setLoadingScores(false);
      return;
    }
    setLoadingScores(true);
    setError(null);
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
      if (submissionResult.status !== "fulfilled") throw submissionResult.reason;
      setSubmissions(submissionResult.value.submissions);
      if (gradeResult.status !== "fulfilled") throw gradeResult.reason;
      setGrades(gradeResult.value.grades);
      setGradeTotal(gradeResult.value.total);
      setFilteredGradeTotal(gradeResult.value.filtered_total);
      setAllGradesLocked(gradeResult.value.all_locked);
      setGradeNextCursor(gradeResult.value.next_cursor);
      setGradesHaveMore(gradeResult.value.has_more);
      const identityResult = await loadIdentities(
        canReadStudentNames,
        gradeResult.value.grades.flatMap((grade) => grade.student_id ? [grade.student_id] : [])
      );
      if (!request.isCurrent()) return;
      setIdentities(identityResult);
      if (qualityResult.status !== "fulfilled") throw qualityResult.reason;
      setQuality(qualityResult.value);
      if (rosterResult.status !== "fulfilled") throw rosterResult.reason;
      setRoster(rosterResult.value.roster);
      if (auditResult.status === "fulfilled") setAuditLogs(auditResult.value.audit_logs);
      if (releaseGateResult.status === "fulfilled") setReleaseGate(releaseGateResult.value.release_gate);
      else if (canManage) throw releaseGateResult.reason;
      if (releasesResult.status === "fulfilled") setScoreReleases(releasesResult.value.score_releases);
      else if (canManage) throw releasesResult.reason;
      if (regradesResult.status === "fulfilled") setRegradeJobs(regradesResult.value.regrade_jobs);
      else if (canManage) throw regradesResult.reason;
    } catch (currentError) {
      if (request.isCurrent()) setError(formatError(currentError));
    } finally {
      if (request.isCurrent()) setLoadingScores(false);
    }
  }, [appliedKeyword, canManage, canReadAudit, canReadStudentNames, statusFilter]);

  useEffect(() => { void loadExamList(); }, [loadExamList]);
  useEffect(() => { if (initialExamId) setSelectedExamId(initialExamId); }, [initialExamId]);
  useEffect(() => { void loadScores(selectedExamId); }, [loadScores, selectedExamId]);

  const refresh = async () => {
    await loadExamList();
    if (selectedExamId) await loadScores(selectedExamId);
  };

  const loadMoreGrades = async () => {
    if (!selectedExamId || !gradesHaveMore || !gradeNextCursor || loadingMoreGrades) return;
    const request = scoreRequestsRef.current.begin(selectedExamId);
    setLoadingMoreGrades(true);
    try {
      const result = await listExamGrades(selectedExamId, {
        status: statusFilter === "all" ? undefined : statusFilter,
        q: appliedKeyword || undefined,
        limit: 50,
        cursor: gradeNextCursor
      });
      const identityResult = await loadIdentities(
        canReadStudentNames,
        result.grades.flatMap((grade) => grade.student_id ? [grade.student_id] : [])
      );
      if (!request.isCurrent()) return;
      setGrades((current) => {
        const byId = new Map(current.map((grade) => [grade.id, grade]));
        for (const grade of result.grades) byId.set(grade.id, grade);
        return [...byId.values()];
      });
      setIdentities((current) => ({
        students: { ...current.students, ...identityResult.students },
        classes: { ...current.classes, ...identityResult.classes },
        error: [current.error, identityResult.error].filter(Boolean).join("；") || undefined
      }));
      setGradeTotal(result.total);
      setFilteredGradeTotal(result.filtered_total);
      setAllGradesLocked(result.all_locked);
      setGradeNextCursor(result.next_cursor);
      setGradesHaveMore(result.has_more);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setLoadingMoreGrades(false);
    }
  };

  return {
    exams, selectedExamId, setSelectedExamId, submissions, grades, gradeTotal,
    filteredGradeTotal, allGradesLocked, quality, releaseGate, scoreReleases,
    regradeJobs, roster, setRoster, identities, auditLogs, keyword, setKeyword,
    appliedKeyword, setAppliedKeyword, statusFilter, setStatusFilter, loadingExams,
    loadingScores, loadingMoreGrades, error, loadExamList, loadScores, refresh,
    loadMoreGrades, gradesHaveMore
  };
}
