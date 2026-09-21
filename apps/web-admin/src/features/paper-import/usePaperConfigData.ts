import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { getExam, listExams, type Exam } from "../../api/exams";
import {
  listPaperImports,
  listPapers,
  listQuestions,
  type PaperImportJob,
  type PaperVersion,
  type Question,
  type ValidationResult
} from "../../api/papers";
import { usePaperImportPolling } from "./usePaperImportPolling";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) console.error("请求失败", error.status, error.code, error.message);
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function usePaperConfigData({ initialExamId = "", fixedExamId }: { initialExamId?: string; fixedExamId?: string } = {}) {
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState(initialExamId);
  const [papers, setPapers] = useState<PaperVersion[]>([]);
  const [paperImports, setPaperImports] = useState<PaperImportJob[]>([]);
  const [questions, setQuestions] = useState<Question[]>([]);
  const [selectedQuestionId, setSelectedQuestionId] = useState<string | null>(null);
  const [editorMode, setEditorMode] = useState<"create" | "edit">("create");
  const [loading, setLoading] = useState(true);
  const [configLoading, setConfigLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [validation, setValidation] = useState<ValidationResult | null>(null);
  const configRequestRef = useRef(0);

  const loadExams = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = fixedExamId ? { exams: [(await getExam(fixedExamId)).exam] } : await listExams();
      setExams(result.exams);
      setSelectedExamId((current) => {
        if (fixedExamId) return fixedExamId;
        if (initialExamId && result.exams.some((exam) => exam.id === initialExamId)) return initialExamId;
        return result.exams.some((exam) => exam.id === current) ? current : result.exams[0]?.id || "";
      });
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoading(false);
    }
  }, [fixedExamId, initialExamId]);

  const loadConfig = useCallback(async (examId: string, options: { silent?: boolean } = {}) => {
    const requestId = ++configRequestRef.current;
    if (!examId) {
      setPapers([]);
      setPaperImports([]);
      setQuestions([]);
      setConfigLoading(false);
      return;
    }
    if (!options.silent) {
      setConfigLoading(true);
      setConfigError(null);
    }
    try {
      const [paperResult, questionResult, importResult] = await Promise.all([
        listPapers(examId),
        listQuestions(examId),
        listPaperImports(examId)
      ]);
      if (requestId !== configRequestRef.current) return;
      setPapers(paperResult.papers);
      setPaperImports(importResult.imports);
      setQuestions(questionResult.questions);
      setValidation(null);
      setEditorMode((current) => questionResult.questions.length === 0 ? "create" : current === "create" ? "create" : "edit");
      setSelectedQuestionId((current) => {
        if (questionResult.questions.length === 0) return null;
        return current && questionResult.questions.some((question) => question.id === current)
          ? current
          : questionResult.questions[0].id;
      });
    } catch (currentError) {
      if (requestId === configRequestRef.current && !options.silent) setConfigError(formatError(currentError));
    } finally {
      if (requestId === configRequestRef.current) setConfigLoading(false);
    }
  }, []);

  useEffect(() => { void loadExams(); }, [loadExams]);
  useEffect(() => { if (fixedExamId || initialExamId) setSelectedExamId(fixedExamId || initialExamId); }, [fixedExamId, initialExamId]);
  useEffect(() => { void loadConfig(selectedExamId); }, [loadConfig, selectedExamId]);

  const processingImportId = paperImports.find((item) => item.status === "processing")?.id;
  const applyImportProgress = useCallback((job: PaperImportJob) => {
    setPaperImports((current) => current.map((item) => item.id === job.id ? job : item));
  }, []);
  const reloadSettledImport = useCallback(async (examId: string) => {
    await loadConfig(examId, { silent: true });
  }, [loadConfig]);
  usePaperImportPolling({
    examId: selectedExamId,
    importId: processingImportId,
    onProgress: applyImportProgress,
    onSettled: reloadSettledImport
  });

  return {
    exams, selectedExamId, setSelectedExamId, papers, paperImports, questions,
    selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode,
    loading, configLoading, error, configError, validation, setValidation,
    loadExams, loadConfig
  };
}
