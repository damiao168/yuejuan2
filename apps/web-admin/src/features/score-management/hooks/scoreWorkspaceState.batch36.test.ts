import { describe, expect, it } from "vitest";
import type { Exam } from "../../../api/exams";
import type { SchoolClass, Student } from "../../../api/org";
import type { SubmissionGrade } from "../../../api/scores";
import {
  emptyScoreWorkspaceSnapshot, initialScoreWorkspaceState, scoreWorkspaceReducer,
  type ScoreWorkspaceAction, type ScoreWorkspaceState
} from "./scoreWorkspaceState";

function exam(id: string, status = "grading"): Exam {
  return {
    id, tenant_id: "tenant-1", school_id: "school-1", name: id, subject: "math",
    exam_type: "mock", total_score: 100, status, grading_mode: "human_review_required",
    appeal_enabled: false, publish_policy: "manual", created_by: "user-1",
    class_ids: [], revision: 1
  };
}

function grade(id: string, totalScore = 1): SubmissionGrade {
  return {
    id, tenant_id: "tenant-1", exam_id: "current", submission_id: id,
    anonymous_code: id, total_score: totalScore, max_score: 100, status: "confirmed",
    locked: false, revision: 1, created_by: "user-1", created_at: "", updated_at: ""
  };
}

function paginatedState(): ScoreWorkspaceState {
  return {
    ...initialScoreWorkspaceState("current"),
    snapshot: {
      ...emptyScoreWorkspaceSnapshot("current"),
      grades: [grade("existing")], gradeNextCursor: "cursor-1", gradesHaveMore: true
    },
    loadingMoreGrades: true
  };
}

function appendAction(): Extract<ScoreWorkspaceAction, { type: "appendGrades" }> {
  return {
    type: "appendGrades", examId: "current", expectedCursor: "cursor-1",
    grades: [grade("new")], total: 3, filteredTotal: 2, allLocked: true,
    nextCursor: "cursor-2", hasMore: true, identities: { students: {}, classes: {} }
  };
}

const listed = [exam("first"), exam("current"), exam("requested", "reviewing"), exam("initial")];
const selectionCases: Array<{
  name: string; exams: Exam[]; initialExamId: string; requestedStatus: string;
  currentId: string; expectedId: string;
}> = [
  { name: "valid initial examination precedes requested status", exams: listed,
    initialExamId: "initial", requestedStatus: "reviewing", currentId: "current", expectedId: "initial" },
  { name: "missing initial examination falls back to requested status", exams: listed,
    initialExamId: "missing", requestedStatus: "reviewing", currentId: "current", expectedId: "requested" },
  { name: "the first status match wins", exams: [...listed, exam("later-requested", "reviewing")],
    initialExamId: "", requestedStatus: "reviewing", currentId: "current", expectedId: "requested" },
  { name: "unmatched status preserves a valid current selection", exams: listed,
    initialExamId: "missing", requestedStatus: "ready", currentId: "current", expectedId: "current" },
  { name: "empty requested status does not match empty status", exams: [exam("empty-status", ""), ...listed],
    initialExamId: "", requestedStatus: "", currentId: "current", expectedId: "current" },
  { name: "missing current selection falls back to the first examination", exams: listed,
    initialExamId: "missing", requestedStatus: "ready", currentId: "absent", expectedId: "first" },
  { name: "empty list clears a previously selected examination", exams: [],
    initialExamId: "missing", requestedStatus: "reviewing", currentId: "current", expectedId: "" },
  { name: "status match with an empty id remains selected", exams: [exam("", "reviewing"), ...listed],
    initialExamId: "missing", requestedStatus: "reviewing", currentId: "current", expectedId: "" },
  { name: "empty initial id does not override a current examination", exams: [exam(""), ...listed],
    initialExamId: "", requestedStatus: "", currentId: "current", expectedId: "current" },
  { name: "a current empty id remains valid when it is present", exams: [...listed, exam("")],
    initialExamId: "missing", requestedStatus: "", currentId: "", expectedId: "" }
];

describe("score workspace batch36 selection and pagination", () => {
  it.each(selectionCases)("$name", ({ exams, initialExamId, requestedStatus, currentId, expectedId }) => {
    const state: ScoreWorkspaceState = {
      ...initialScoreWorkspaceState(currentId),
      snapshot: { ...emptyScoreWorkspaceSnapshot(currentId), grades: [grade("old")] },
      loadingScores: true, loadingMoreGrades: true, error: "previous read failed"
    };
    const result = scoreWorkspaceReducer(state, { type: "examsLoaded", exams, initialExamId, requestedStatus });
    expect(result.selectedExamId).toBe(expectedId);
    expect(result.exams).toBe(exams);
    expect(result.loadingScores).toBe(true);
    expect(result.loadingMoreGrades).toBe(true);
    expect(result.error).toBe("previous read failed");
    if (expectedId === currentId) {
      expect(result.snapshot).toBe(state.snapshot);
    } else {
      expect(result.snapshot).not.toBe(state.snapshot);
      expect(result.snapshot.examId).toBe(expectedId);
      expect(result.snapshot.grades).toEqual([]);
    }
    expect(state.snapshot.grades[0].id).toBe("old");
  });

  it.each(["selected examination", "snapshot examination", "expected cursor"])(
    "returns the same state when the %s no longer matches the page",
    (boundary) => {
      const state = paginatedState();
      if (boundary === "selected examination") state.selectedExamId = "other";
      if (boundary === "snapshot examination") state.snapshot.examId = "other";
      if (boundary === "expected cursor") state.snapshot.gradeNextCursor = "new-cursor";
      const result = scoreWorkspaceReducer(state, appendAction());
      expect(result).toBe(state);
      expect(result.loadingMoreGrades).toBe(true);
      expect(result.snapshot.grades.map((item) => item.id)).toEqual(["existing"]);
    }
  );

  it("replaces duplicate ids in first-insertion order and merges identities without mutating the old page", () => {
    const first = grade("first", 1);
    const second = grade("second", 2);
    const firstReplacement = grade("first", 10);
    const newReplacement = grade("new", 30);
    const oldStudent = { id: "student", name: "old" } as Student;
    const nextStudent = { id: "student", name: "new" } as Student;
    const retainedStudent = { id: "retained", name: "retained" } as Student;
    const oldClass = { id: "class", name: "old class" } as SchoolClass;
    const nextClass = { id: "class", name: "new class" } as SchoolClass;
    const state = paginatedState();
    state.snapshot.grades = [first, second];
    state.snapshot.identities = {
      students: { student: oldStudent, retained: retainedStudent },
      classes: { class: oldClass }, error: "first identity error"
    };
    const action = {
      ...appendAction(),
      grades: [grade("new", 3), firstReplacement, newReplacement],
      identities: { students: { student: nextStudent }, classes: { class: nextClass }, error: "next identity error" }
    };
    const result = scoreWorkspaceReducer(state, action);
    expect(result.snapshot.grades).toEqual([firstReplacement, second, newReplacement]);
    expect(result.snapshot.gradeTotal).toBe(3);
    expect(result.snapshot.filteredGradeTotal).toBe(2);
    expect(result.snapshot.allGradesLocked).toBe(true);
    expect(result.snapshot.gradeNextCursor).toBe("cursor-2");
    expect(result.snapshot.gradesHaveMore).toBe(true);
    expect(result.loadingMoreGrades).toBe(false);
    expect(result.snapshot.identities.students).toEqual({ student: nextStudent, retained: retainedStudent });
    expect(result.snapshot.identities.classes).toEqual({ class: nextClass });
    expect(result.snapshot.identities.error).toBe("first identity error；next identity error");
    expect(state.snapshot.grades).toEqual([first, second]);
    expect(state.snapshot.identities.students.student).toBe(oldStudent);
    expect(state.snapshot.identities.classes.class).toBe(oldClass);
    expect(state.snapshot.identities.error).toBe("first identity error");
    expect(state.snapshot.gradeNextCursor).toBe("cursor-1");
  });
});
