export type ViewKey =
  | "dashboard"
  | "exams"
  | "examCreate"
  | "papers"
  | "questionBank"
  | "capture"
  | "grading"
  | "review"
  | "arbitration"
  | "scores"
  | "reports"
  | "appeals"
  | "quality"
  | "desktop"
  | "system"
  | "systemStatus"
  | "modelGovernance"
  | "subjectiveGradingBatches"
  | "audit"
  | "organization"
  | "membersStudents"
  | "membersClasses"
  | "membersTeachers"
  | "platformSchools"
  | "platformGettingStarted"
  | "platformModelConfig"
  | "schoolAiChat"
  | "sessions"
  | "examWorkspace";

export type StatusTone = "success" | "warning" | "danger" | "info" | "processing" | "neutral";

export interface Metric {
  label: string;
  value: string;
  trend: string;
  status: StatusTone;
}

export interface AgentStage {
  name: string;
  state: "done" | "running" | "queued" | "failed";
  count: number;
  detail: string;
}

export interface RubricPoint {
  id: string;
  title: string;
  score: number;
  state: "hit" | "partial" | "missing";
  evidence: string;
}

export interface SubmissionRow {
  id: string;
  question: string;
  status: string;
  score: string;
  confidence: string;
  risk: string;
  owner: string;
}

export interface ExamRow {
  name: string;
  subject: string;
  status: string;
  papers: number;
  progress: number;
  mode: string;
}

export interface AuditItem {
  time: string;
  actor: string;
  action: string;
  target: string;
  result: string;
}
