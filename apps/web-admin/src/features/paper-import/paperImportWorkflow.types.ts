import type { Exam } from "../../api/exams";
import type { PaperImportJob, PaperVersion } from "../../api/papers";

export interface PaperImportWorkflowOptions {
  canManage: boolean;
  selectedExam?: Exam;
  papers: PaperVersion[];
  paperImports: PaperImportJob[];
  loadConfig: (examId: string, options?: { silent?: boolean; propagateError?: boolean }) => Promise<void>;
  onChanged?: () => void;
}
