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
  const selectedScopeRef = useRef({ examId: selectedExamId });
  if (selectedScopeRef.current.examId !== selectedExamId) selectedScopeRef.current = { examId: selectedExamId };
  const selectedScope = selectedScopeRef.current;
  const [importConnectionError, setImportConnectionError] = useState<string>();
  const [importRetryKey, setImportRetryKey] = useState(0);
  const onImportConnectionError = useCallback((error?: unknown) => setImportConnectionError(error ? getUserErrorMessage(error, "暂时无法获取最新处理状态") : undefined), []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试清除上一场的轮询连接错误，不将旧错误展示在新配置。
  useEffect(() => { setImportConnectionError(undefined); }, [selectedExamId]);

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

  const loadConfig = useCallback(async (examId: string, options: { silent?: boolean; propagateError?: boolean } = {}) => {
    // 旧上传/重试回调也可能晚于换考试才发起刷新；先核对所选范围，不能让它抢占新范围的请求代次。
    if (selectedScope !== selectedScopeRef.current || examId !== selectedScope.examId) return;
    const requestId = ++configRequestRef.current;
    const current = () => selectedScope === selectedScopeRef.current && requestId === configRequestRef.current;
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
      if (!current()) return;
      setPapers(paperResult.papers);
      setPaperImports(importResult.imports);
      setQuestions(questionResult.questions);
      setValidation(null);
      setEditorMode(questionResult.questions.length === 0 ? "create" : "edit");
      setSelectedQuestionId((current) => {
        if (questionResult.questions.length === 0) return null;
        return current && questionResult.questions.some((question) => question.id === current)
          ? current
          : questionResult.questions[0].id;
      });
    } catch (currentError) {
      if (current() && !options.silent) setConfigError(formatError(currentError));
      // 整题保存需要区分写入回执与刷新结果；普通读取和轮询仍由各自状态提示错误。
      if (options.propagateError) throw currentError;
    } finally {
      if (current()) setConfigLoading(false);
    }
  }, [selectedScope]);

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
    onSettled: reloadSettledImport,
    onConnectionError: onImportConnectionError, retryKey: importRetryKey
  });

  return {
    exams, selectedExamId, setSelectedExamId, papers, paperImports, questions,
    selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode,
    loading, configLoading, error, configError, validation, setValidation,
    loadExams, loadConfig, importConnectionError, retryImportConnection: () => setImportRetryKey(value => value + 1)
  };
}
