import type { SetStateAction } from "react";
import type { AuditLog } from "../../../api/audit";
import type { Exam } from "../../../api/exams";
import type { QualityCheckResult, RosterReport, SubmissionGrade } from "../../../api/scores";
import type { RegradeJob, ScoreRelease, ScoreReleaseGate } from "../../../api/scoreReleases";
import type { Submission } from "../../../api/submissions";
import type { ScoreIdentityMaps } from "./useScoreWorkspaceData";

export interface ScoreWorkspaceSnapshot {
  examId: string;
  submissions: Submission[];
  grades: SubmissionGrade[];
  gradeTotal: number;
  filteredGradeTotal: number;
  allGradesLocked: boolean;
  gradeNextCursor: string;
  gradesHaveMore: boolean;
  quality: QualityCheckResult | null;
  releaseGate: ScoreReleaseGate | null;
  scoreReleases: ScoreRelease[];
  regradeJobs: RegradeJob[];
  roster: RosterReport | null;
  identities: ScoreIdentityMaps;
  auditLogs: AuditLog[];
}

export function emptyScoreWorkspaceSnapshot(examId = ""): ScoreWorkspaceSnapshot {
  return {
    examId, submissions: [], grades: [], gradeTotal: 0, filteredGradeTotal: 0,
    allGradesLocked: false, gradeNextCursor: "", gradesHaveMore: false, quality: null,
    releaseGate: null, scoreReleases: [], regradeJobs: [], roster: null,
    identities: { students: {}, classes: {} }, auditLogs: []
  };
}

export interface ScoreWorkspaceState {
  exams: Exam[];
  selectedExamId: string;
  snapshot: ScoreWorkspaceSnapshot;
  keyword: string;
  appliedKeyword: string;
  statusFilter: string;
  loadingExams: boolean;
  loadingScores: boolean;
  loadingMoreGrades: boolean;
  error: string | null;
}

export type ScoreWorkspaceAction =
  | { type: "patch"; patch: Partial<ScoreWorkspaceState> }
  | { type: "examsLoaded"; exams: Exam[]; initialExamId: string; requestedStatus: string }
  | { type: "selectExam"; examId: string }
  | { type: "selectExamUpdate"; value: SetStateAction<string> }
  | { type: "loadStarted"; examId: string }
  | { type: "loadSucceeded"; examId: string; snapshot: ScoreWorkspaceSnapshot }
  | { type: "loadFailed"; examId: string; error: string }
  | { type: "appendGrades"; examId: string; expectedCursor: string; grades: SubmissionGrade[];
      total: number; filteredTotal: number; allLocked: boolean; nextCursor: string; hasMore: boolean;
      identities: ScoreIdentityMaps }
  | { type: "setRoster"; roster: RosterReport | null };

export function initialScoreWorkspaceState(initialExamId: string): ScoreWorkspaceState {
  return {
    exams: [], selectedExamId: initialExamId, snapshot: emptyScoreWorkspaceSnapshot(initialExamId),
    keyword: "", appliedKeyword: "", statusFilter: "all", loadingExams: true,
    loadingScores: false, loadingMoreGrades: false, error: null
  };
}

export function scoreWorkspaceReducer(state: ScoreWorkspaceState, action: ScoreWorkspaceAction): ScoreWorkspaceState {
  switch (action.type) {
    case "patch": return { ...state, ...action.patch };
    case "examsLoaded": {
      const requestedExam = action.requestedStatus
        ? action.exams.find((exam) => exam.status === action.requestedStatus) : undefined;
      const examId = action.initialExamId && action.exams.some((exam) => exam.id === action.initialExamId)
        ? action.initialExamId
        : requestedExam?.id ?? (action.exams.some((exam) => exam.id === state.selectedExamId)
          ? state.selectedExamId : action.exams[0]?.id || "");
      return { ...state, exams: action.exams, selectedExamId: examId,
        snapshot: examId === state.selectedExamId ? state.snapshot : emptyScoreWorkspaceSnapshot(examId) };
    }
    case "selectExam": return action.examId === state.selectedExamId ? state : {
      ...state, selectedExamId: action.examId,
      snapshot: emptyScoreWorkspaceSnapshot(action.examId),
      loadingMoreGrades: false, error: null
    };
    case "selectExamUpdate": return scoreWorkspaceReducer(state, {
      type: "selectExam", examId: typeof action.value === "function" ? action.value(state.selectedExamId) : action.value
    });
    case "loadStarted": return state.selectedExamId !== action.examId ? state : {
      ...state, loadingScores: Boolean(action.examId), loadingMoreGrades: false, error: null,
      snapshot: state.snapshot.examId === action.examId ? state.snapshot : emptyScoreWorkspaceSnapshot(action.examId)
    };
    case "loadSucceeded": return state.selectedExamId === action.examId
      ? { ...state, snapshot: action.snapshot, loadingScores: false, error: null }
      : state;
    case "loadFailed": return state.selectedExamId === action.examId
      ? { ...state, loadingScores: false, error: action.error }
      : state;
    // 翻页结果还须匹配发起时的游标，重复回包或全量刷新后的旧页不能再次拼入。
    case "appendGrades": {
      if (state.selectedExamId !== action.examId || state.snapshot.examId !== action.examId
        || state.snapshot.gradeNextCursor !== action.expectedCursor) return state;
      const byId = new Map(state.snapshot.grades.map((grade) => [grade.id, grade]));
      for (const grade of action.grades) byId.set(grade.id, grade);
      return { ...state, snapshot: {
        ...state.snapshot, grades: [...byId.values()], gradeTotal: action.total,
        filteredGradeTotal: action.filteredTotal, allGradesLocked: action.allLocked,
        gradeNextCursor: action.nextCursor, gradesHaveMore: action.hasMore,
        identities: {
          students: { ...state.snapshot.identities.students, ...action.identities.students },
          classes: { ...state.snapshot.identities.classes, ...action.identities.classes },
          error: [state.snapshot.identities.error, action.identities.error].filter(Boolean).join("；") || undefined
        }
      }, loadingMoreGrades: false };
    }
    case "setRoster": return { ...state, snapshot: { ...state.snapshot, roster: action.roster } };
  }
}
