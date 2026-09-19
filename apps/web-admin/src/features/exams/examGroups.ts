import type { Exam } from "../../api/exams";

export interface ExamGroup {
  id: string;
  name: string;
  examType: string;
  schoolId: string;
  gradeId: string;
  exams: Exam[];
}

export function groupExams(exams: Exam[]): ExamGroup[] {
  const groups = new Map<string, ExamGroup>();
  for (const exam of exams) {
    const id = exam.exam_session_id || exam.id;
    let group = groups.get(id);
    if (!group) {
      group = {
        id,
        name: exam.exam_session_name || exam.name,
        examType: exam.exam_type,
        schoolId: exam.school_id,
        gradeId: exam.exam_session_grade_id || "",
        exams: []
      };
      groups.set(id, group);
    }
    group.exams.push(exam);
  }
  return [...groups.values()];
}

export function selectedGroupExam(group: ExamGroup, selectedId: string | undefined, subjectFilter: string, statusFilter = ""): Exam {
  const matches = (exam: Exam) => (!subjectFilter || exam.subject === subjectFilter)
    && (!statusFilter || (statusFilter === "active" ? !["published", "archived"].includes(exam.status) : exam.status === statusFilter));
  return group.exams.find((exam) => exam.id === selectedId)
    ?? group.exams.find(matches)
    ?? group.exams[0];
}
