import type { Dispatch, SetStateAction } from "react";
import type { Exam } from "../../api/exams";
import type { PaperImportDraftQuestion, PaperImportJob, Question } from "../../api/papers";
import type { usePaperConfigData } from "../paper-import/usePaperConfigData";
import type { usePaperImportWorkflow } from "../paper-import/usePaperImportWorkflow";
import type { useScoringRules } from "../paper-import/useScoringRules";
import type { useQuestionEditor } from "./hooks/useQuestionEditor";
import type { useRubricEditor } from "./hooks/useRubricEditor";
import type { QuestionFilter, questionEditingAccess } from "./paperQuestionPresentation";

export interface PaperRubricPageProps {
  canManage: boolean;
  canManageAssessment: boolean;
  canReadQuestionBank?: boolean;
  canImportQuestionBank?: boolean;
  questionBankScope?: string;
  initialExamId?: string;
  fixedExamId?: string;
  mode?: "standalone" | "embedded";
  initialView?: "materials" | "questions";
  onExamChanged?: () => void;
  onNavigate?: (path: string) => void;
}

// 工作流和草稿状态由页面持有；展示层只在用户操作时调用这些回调，不负责刷新或丢弃草稿。
export interface PaperRubricViewContext {
  options: Readonly<PaperRubricPageProps> & {
    mode: "standalone" | "embedded";
    initialView: "materials" | "questions";
    initialExamId: string;
    questionBankScope: string;
  };
  config: ReturnType<typeof usePaperConfigData>;
  importWorkflow: ReturnType<typeof usePaperImportWorkflow>;
  questionWorkflow: ReturnType<typeof useQuestionEditor>;
  rubricWorkflow: ReturnType<typeof useRubricEditor>;
  scoringRuleWorkflow: ReturnType<typeof useScoringRules>;
  selectedExam: Exam | undefined;
  selectedQuestion: Question | null;
  latestPaperImport: PaperImportJob | undefined;
  structureConfirmed: boolean | undefined;
  pendingDrafts: PaperImportDraftQuestion[];
  access: ReturnType<typeof questionEditingAccess>;
  state: {
    formDirty: boolean;
    unsavedWork: boolean;
    saveBusy: boolean;
    allSaveMessage: string | null;
    allSaveCompleted: boolean;
    saveRefreshPending: boolean;
    activeCandidateIndex: number | null;
    detailTab: string;
    questionFilter: QuestionFilter;
    questionSearch: string;
    sourceOpen: boolean;
    indexOpen: boolean;
    visibleQuestions: Question[];
    selectedQuestionIndex: number;
    watchedQuestionType: string | undefined;
  };
  actions: {
    setDetailTab: Dispatch<SetStateAction<string>>;
    setQuestionFilter: Dispatch<SetStateAction<QuestionFilter>>;
    setQuestionSearch: Dispatch<SetStateAction<string>>;
    setSourceOpen: Dispatch<SetStateAction<boolean>>;
    setIndexOpen: Dispatch<SetStateAction<boolean>>;
    markQuestionDirty: () => void;
    openQuestion: (question: Question) => void;
    createQuestion: () => void;
    openCandidate: (index: number) => void;
    saveQuestion: () => void;
    saveAllQuestionChanges: () => void;
    rereadSavedResult: () => void;
    discardActiveDraft: () => void;
    saveRubric: () => void;
    saveScoringRule: (publish: boolean) => void;
    runValidation: () => void;
    onBankCopied: () => Promise<void>;
    onHistoryImported: () => Promise<void>;
    onStructureAdopted: () => Promise<void>;
    openTemplate: () => void;
  };
}
