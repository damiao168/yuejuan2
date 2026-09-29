import type { Grade } from "../../../api/org";
import type { ExamTemplateSubject } from "../../../api/examTemplates";
import type { BlueprintSectionDraft, CreateExamDraft, SubjectExamDraft } from "./types";

function localId(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function createSubjectDraft(subject: string): SubjectExamDraft {
  return { subject, totalScore: 100, durationMinutes: 90, candidateRule: "all_selected_classes", classIds: [], sections: [] };
}

export function subjectDraftFromTemplate(subject: ExamTemplateSubject): SubjectExamDraft {
  // 分区使用本地编辑 ID，不复用模板记录 ID；考试创建后由服务端建立自己的分区记录。
  return {
    subject: subject.subject,
    totalScore: subject.total_score,
    durationMinutes: subject.duration_minutes,
    candidateRule: subject.candidate_rule,
    classIds: [],
    sections: subject.sections.map((section) => ({ id: localId("section"), title: section.title, questionType: section.question_type, questionCount: section.question_count, scorePerQuestion: section.score_per_question }))
  };
}

export function createBlankSection(): BlueprintSectionDraft {
  return { id: localId("section"), title: "新分区", questionType: "short_answer", questionCount: 1, scorePerQuestion: 10 };
}

export function initialCreateExamDraft(schoolId = "", grade?: Grade): CreateExamDraft {
  return {
    version: 2,
    creationMode: "materials",
    schoolId,
    name: "",
    examType: "",
    gradeId: grade?.id ?? "",
    templateId: "",
    classIds: [],
    subjects: [],
    gradingMode: "ai_assisted",
    publishPolicy: "after_admin_approval",
    appealEnabled: true
  };
}

export function subjectScore(subject: SubjectExamDraft) {
  return subject.sections.reduce((total, section) => total + section.questionCount * section.scorePerQuestion, 0);
}
