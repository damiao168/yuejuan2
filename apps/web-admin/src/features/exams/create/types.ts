export type GradingChoice = "auto_objective_only" | "ai_assisted" | "human_review_required" | "double_mark" | "blind_double_mark";

export type ExamCreationMode = "materials" | "quick" | "template";

export interface BlueprintSectionDraft {
  id: string;
  title: string;
  questionType: string;
  questionCount: number;
  scorePerQuestion: number;
}

export interface SubjectExamDraft {
  subject: string;
  totalScore: number;
  durationMinutes: number;
  candidateRule: "all_selected_classes" | "subject_selected_classes";
  classIds: string[];
  sections: BlueprintSectionDraft[];
}

export interface CreateExamDraft {
  version: 2;
  creationMode: ExamCreationMode;
  schoolId: string;
  name: string;
  examType: string;
  gradeId: string;
  templateId: string;
  classIds: string[];
  subjects: SubjectExamDraft[];
  gradingMode: GradingChoice;
  publishPolicy: string;
  appealEnabled: boolean;
}
