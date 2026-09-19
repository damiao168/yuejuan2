import { describe, expect, it } from "vitest";
import type { Exam } from "../../api/exams";
import { groupExams, selectedGroupExam } from "./examGroups";

function exam(id: string, subject: string, sessionId = ""): Exam {
  return {
    id, tenant_id: "tenant", school_id: "school", name: sessionId ? `高二期中考试 · ${subject}` : "单科考试",
    subject, exam_type: "midterm_exam", total_score: 100, status: "draft", grading_mode: "ai_assisted",
    appeal_enabled: true, publish_policy: "manual_publish", created_by: "user", class_ids: ["class"], revision: 1,
    exam_session_id: sessionId || undefined, exam_session_name: sessionId ? "高二期中考试" : undefined,
    exam_session_grade_id: sessionId ? "grade" : undefined
  };
}

describe("exam groups", () => {
  it("shows one row for a multi-subject session and keeps standalone exams separate", () => {
    const grouped = groupExams([exam("math", "math", "session"), exam("physics", "physics", "session"), exam("legacy", "english")]);
    expect(grouped).toHaveLength(2);
    expect(grouped[0]).toMatchObject({ id: "session", name: "高二期中考试", gradeId: "grade" });
    expect(grouped[0].exams.map((item) => item.id)).toEqual(["math", "physics"]);
    expect(grouped[1].id).toBe("legacy");
  });

  it("selects the requested subject when a subject filter is active", () => {
    const group = groupExams([exam("math", "math", "session"), exam("physics", "physics", "session")])[0];
    expect(selectedGroupExam(group, undefined, "physics").id).toBe("physics");
    expect(selectedGroupExam(group, "math", "").id).toBe("math");
    expect(selectedGroupExam(group, "math", "physics").id).toBe("math");
    group.exams[1].status = "published";
    expect(selectedGroupExam(group, undefined, "", "published").id).toBe("physics");
  });
});
