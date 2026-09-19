import { describe, expect, it } from "vitest";
import type { SubmissionGrade } from "../../../api/scores";
import {
  emptyScoreWorkspaceSnapshot, initialScoreWorkspaceState, scoreWorkspaceReducer
} from "./scoreWorkspaceState";

function grade(id: string): SubmissionGrade {
  return { id } as SubmissionGrade;
}

describe("score workspace atomic state", () => {
  it("does not publish a partial replacement after a required request fails", () => {
    const initial = initialScoreWorkspaceState("exam-a");
    const old = { ...emptyScoreWorkspaceSnapshot("exam-a"), grades: [grade("old")] };
    const loading = scoreWorkspaceReducer({ ...initial, snapshot: old }, { type: "loadStarted", examId: "exam-a" });
    const failed = scoreWorkspaceReducer(loading, { type: "loadFailed", examId: "exam-a", error: "quality unavailable" });
    expect(failed.snapshot).toBe(old);
    expect(failed.error).toBe("quality unavailable");
  });

  it("clears the old exam before a new request and rejects its stale completion", () => {
    const initial = { ...initialScoreWorkspaceState("exam-a"),
      snapshot: { ...emptyScoreWorkspaceSnapshot("exam-a"), grades: [grade("old")] } };
    const switched = scoreWorkspaceReducer(initial, { type: "selectExam", examId: "exam-b" });
    expect(switched.snapshot.grades).toEqual([]);
    expect(scoreWorkspaceReducer(switched, { type: "loadStarted", examId: "exam-a" })).toBe(switched);
    const stale = scoreWorkspaceReducer(switched, {
      type: "loadSucceeded", examId: "exam-a", snapshot: initial.snapshot
    });
    expect(stale).toBe(switched);
  });

  it("appends one page atomically, deduplicates ids, and rejects a stale cursor", () => {
    const initial = { ...initialScoreWorkspaceState("exam-a"),
      snapshot: { ...emptyScoreWorkspaceSnapshot("exam-a"),
        grades: [grade("existing")], gradeNextCursor: "cursor-1", gradesHaveMore: true } };
    const action = {
      type: "appendGrades" as const, examId: "exam-a", expectedCursor: "cursor-1",
      grades: [grade("existing"), grade("next")], total: 2, filteredTotal: 2,
      allLocked: false, nextCursor: "", hasMore: false,
      identities: { students: {}, classes: {} }
    };
    const appended = scoreWorkspaceReducer(initial, action);
    expect(appended.snapshot.grades.map((item) => item.id)).toEqual(["existing", "next"]);
    expect(appended.snapshot.gradesHaveMore).toBe(false);
    expect(scoreWorkspaceReducer(appended, action)).toBe(appended);
  });

  it("clears a superseded pagination spinner when a full refresh starts", () => {
    const loadingMore = { ...initialScoreWorkspaceState("exam-a"), loadingMoreGrades: true };
    const refreshed = scoreWorkspaceReducer(loadingMore, { type: "loadStarted", examId: "exam-a" });
    expect(refreshed.loadingMoreGrades).toBe(false);
    expect(refreshed.loadingScores).toBe(true);
  });
});
