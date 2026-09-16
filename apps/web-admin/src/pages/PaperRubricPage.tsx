import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  List,
  Progress,
  Select,
  Space,
  Switch,
  Upload,
  type TableColumnsType,
  type UploadProps
} from "antd";
import { CheckCircle2, ChevronDown, ChevronUp, ExternalLink, FileUp, LockKeyhole, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { ApiClientError, getPaperImportUserMessage, getUserErrorMessage } from "../api/client";
import { listExams, type Exam } from "../api/exams";
import {
  createQuestion,
	createPaperImport,
	getPaperImport,
	addPaperImportSources,
	cancelPaperImport,
	retryPaperImportParse,
	replacePaperImportSources,
	savePaperImportReview,
  createRubric,
  createScoringRule,
  deleteQuestion,
  applyPaperImport,
  listPaperImports,
  listPapers,
  listQuestions,
  listScoringRules,
  publishScoringRule,
  updateQuestion,
  updateScoringRule,
  uploadFile,
  validatePaperConfig,
  type PaperVersion,
  type PaperImportJob,
	type PaperImportDraftQuestion,
	type PaperImportRole,
  type Question,
  type QuestionPayload,
  type RubricEvidenceRequirement,
  type RubricPoint,
  type ScoringRule,
  type ValidationResult
} from "../api/papers";
import { downloadFileBlob } from "../api/files";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { AssessmentProfileEditor } from "../components/features/assessment/AssessmentProfileEditor";
import { isFormulaEvidenceSubject } from "../features/grading/workbench/mathEvidenceSubjects";
import type { StatusTone } from "../types";
import { questionTypeOptions } from "../constants/examCatalog";
import { filesFromClipboard, hasBlockingImportIssues, hasNoExamContentDetected, isPaperImportCancelled, isSupportedPaperImportFile, isTextPasteTarget, markImportFieldConfirmed, orderedSourcesAfterMove, orderedSourcesAfterRemoval, paperImportProgress, paperImportSummary, sourcesAfterRoleChange } from "../features/paper-import/materials";
import { PaperImportReviewPanel } from "../features/paper-import/PaperImportReviewPanel";
import { BankQuestionPicker } from "../features/question-bank/BankQuestionPicker";
import { HistoryQuestionImporter } from "../features/question-bank/HistoryQuestionImporter";

const rubricStatusOptions = [
  { label: "草稿", value: "draft" },
  { label: "待审批", value: "pending_review" },
  { label: "已批准", value: "approved" },
  { label: "锁定", value: "locked" }
];

const toleranceQuestionTypes = ["numeric", "formula", "calculation"];

const importRoleOptions = [
  { label: "自动判断", value: "auto" },
  { label: "题目", value: "question" },
  { label: "答案", value: "answer" },
  { label: "解析", value: "solution" },
  { label: "评分标准", value: "rubric" },
  { label: "混合资料", value: "mixed" },
  { label: "未知", value: "unknown" }
];

const formulaEvidenceQuestionTypes = new Set(["formula", "calculation", "numeric"]);
const rubricRequiredArchetypes = new Set(["structured_steps", "short_constructed", "extended_response", "diagram_graph", "table_experiment"]);

const formulaEvidenceOptions: { label: string; value: RubricEvidenceRequirement["type"] }[] = [
  { label: "关键等价变形成立", value: "valid_transformation" },
  { label: "最终结果经核验", value: "final_result" },
  { label: "出现指定概念", value: "concept" },
  { label: "单位正确", value: "unit" },
  { label: "定义域／取值条件", value: "domain" }
];

interface QuestionFormValues {
  exam_paper_id?: string;
  question_no: string;
  question_type: string;
  score: number;
  stem: string;
  knowledge_points: string[];
  answer_area_page: number;
  answer_area_x: number;
  answer_area_y: number;
  answer_area_w: number;
  answer_area_h: number;
  sort_order: number;
  standard_answer: string;
  equivalent_answers: string[];
  tolerance_absolute?: number | null;
  tolerance_relative?: number | null;
}

function labelFrom(options: { label: string; value: string }[], value?: string) {
  return options.find((item) => item.value === value)?.label ?? (value ? "未知类型" : "-");
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function rubricStatusLabel(status?: string) {
  if (!status) {
    return "未配置";
  }
  return rubricStatusOptions.find((item) => item.value === status)?.label ?? "未知状态";
}

function rubricTone(status?: string): StatusTone {
  if (status === "locked" || status === "approved") {
    return "success";
  }
  if (status === "pending_review") {
    return "warning";
  }
  return "neutral";
}

function jsonText(value: unknown, fallback: string) {
  if (value === undefined || value === null) {
    return fallback;
  }
  return JSON.stringify(value, null, 2);
}

function parseArrayJSON(value: string, label: string): unknown[] {
  try {
    const parsed = JSON.parse(value || "[]") as unknown;
    if (!Array.isArray(parsed)) {
      throw new Error(`${label}必须是 JSON 数组`);
    }
    return parsed;
  } catch {
    throw new Error(`${label}解析失败，请检查 JSON 格式`);
  }
}

function pointTotal(points: RubricPoint[]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

function reviewRubricHasScoreMismatch(draft: PaperImportDraftQuestion) {
  return Boolean(draft.rubric && (Math.abs(draft.rubric.max_score - draft.score) > 0.0001 || Math.abs(pointTotal(draft.rubric.points) - draft.score) > 0.0001));
}

function confirmedImportDrafts(drafts: PaperImportDraftQuestion[]) {
  return drafts.map((draft) => {
    const fields = new Set(draft.human_confirmed_fields ?? []);
    for (const field of ["question_no", "question_type", "score", "stem"]) fields.add(field);
    if (draft.answer_key) fields.add("answer");
    if (draft.solution) fields.add("solution");
    if (draft.rubric) fields.add("rubric");
    return { ...draft, human_confirmed_fields: [...fields] };
  });
}

function formulaEvidenceEnabled(subject: string | undefined, questionType: string | undefined) {
  return isFormulaEvidenceSubject(subject) && formulaEvidenceQuestionTypes.has(questionType ?? "");
}

export function PaperRubricPage({
  canManage,
  canManageAssessment,
  canReadQuestionBank=false,
  canImportQuestionBank=false,
  questionBankScope="",
  initialExamId = "",
  onExamChanged,
  onNavigate
}: {
  canManage: boolean;
  canManageAssessment: boolean;
  canReadQuestionBank?:boolean;
  canImportQuestionBank?:boolean;
  questionBankScope?:string;
  initialExamId?: string;
  onExamChanged?: () => void;
  onNavigate?: (path: string) => void;
}) {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<QuestionFormValues>();
  const watchedQuestionType = Form.useWatch("question_type", form);
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
  const configRequestRef = useRef(0);
	const materialUploadRef = useRef<(files: File[]) => void>(() => undefined);
  const [savingQuestion, setSavingQuestion] = useState(false);
  const [savingRubric, setSavingRubric] = useState(false);
  const [parsing, setParsing] = useState(false);
  const [validation, setValidation] = useState<ValidationResult | null>(null);
  const [rubricStatus, setRubricStatus] = useState("draft");
  const [rubricPoints, setRubricPoints] = useState<RubricPoint[]>([]);
  const [deductionsJson, setDeductionsJson] = useState("");
  const [examplesJson, setExamplesJson] = useState("");
  const [scoringRules, setScoringRules] = useState<ScoringRule[]>([]);
  const [scoringRuleConfig, setScoringRuleConfig] = useState<Record<string, unknown>>({});
  const [savingScoringRule, setSavingScoringRule] = useState(false);
  const [showQuestionEditor, setShowQuestionEditor] = useState(false);
	const [reviewDrafts, setReviewDrafts] = useState<PaperImportDraftQuestion[]>([]);
	const [savingImportReview, setSavingImportReview] = useState(false);
	const [updatingImportSources, setUpdatingImportSources] = useState(false);
	const [stoppingImport, setStoppingImport] = useState(false);
	const [retryingParse, setRetryingParse] = useState(false);

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const selectedQuestion = useMemo(
    () => (editorMode === "edit" ? questions.find((question) => question.id === selectedQuestionId) ?? null : null),
    [editorMode, questions, selectedQuestionId]
  );
  const selectedLocked = selectedQuestion?.rubric?.status === "locked";
  const questionDisabled = !canManage || selectedLocked;
  const showTolerance = toleranceQuestionTypes.includes(watchedQuestionType ?? "");
  const showFormulaEvidence = formulaEvidenceEnabled(selectedExam?.subject, selectedQuestion?.question_type);
  const objectiveRuleType = selectedQuestion && ["single_choice", "true_false", "multiple_choice", "fill_blank", "numeric"].includes(selectedQuestion.question_type) ? selectedQuestion.question_type : "";
  const draftScoringRule = scoringRules.find((rule) => rule.status === "draft");
  const publishedScoringRule = scoringRules.find((rule) => rule.status === "published");

  const loadExams = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await listExams();
      setExams(result.exams);
      setSelectedExamId((current) => {
        if (initialExamId && result.exams.some((exam) => exam.id === initialExamId)) return initialExamId;
        return result.exams.some((exam) => exam.id === current) ? current : result.exams[0]?.id || "";
      });
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoading(false);
    }
  }, [initialExamId]);

  const loadConfig = useCallback(async (examId: string, options: { silent?: boolean } = {}) => {
    const requestId = ++configRequestRef.current;
    if (!examId) {
      setPapers([]);
      setQuestions([]);
      setConfigLoading(false);
      return;
    }
    if (!options.silent) {
      setConfigLoading(true);
      setConfigError(null);
    }
    try {
      const [paperResult, questionResult, importResult] = await Promise.all([listPapers(examId), listQuestions(examId), listPaperImports(examId)]);
      if (requestId !== configRequestRef.current) return;
      setPapers(paperResult.papers);
      setPaperImports(importResult.imports);
      setQuestions(questionResult.questions);
      setValidation(null);
      setEditorMode((current) => {
        if (questionResult.questions.length === 0) {
          return "create";
        }
        return current === "create" ? "create" : "edit";
      });
      setSelectedQuestionId((current) => {
        if (questionResult.questions.length === 0) {
          return null;
        }
        return current && questionResult.questions.some((question) => question.id === current) ? current : questionResult.questions[0].id;
      });
    } catch (currentError) {
      if (requestId !== configRequestRef.current) return;
      if (!options.silent) setConfigError(formatError(currentError));
    } finally {
      if (requestId === configRequestRef.current) setConfigLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadExams();
  }, [loadExams]);

  useEffect(() => {
    if (initialExamId) setSelectedExamId(initialExamId);
  }, [initialExamId]);

  useEffect(() => {
    void loadConfig(selectedExamId);
  }, [loadConfig, selectedExamId]);

  const processingImportId = paperImports.find((item) => item.status === "processing")?.id;
  useEffect(() => {
    if (!selectedExamId || !processingImportId) return;
    let pending = false;
    let disposed = false;
    const refreshProgress = async () => {
      if (pending) return;
      pending = true;
      try {
        const result = await getPaperImport(processingImportId);
        if (disposed) return;
        setPaperImports((current) => current.map((item) => item.id === result.import.id ? result.import : item));
        if (result.import.status !== "processing") await loadConfig(selectedExamId, { silent: true });
      } catch {
        // A transient progress request must not discard the current import state.
        // The next interval retries the authoritative import endpoint.
      } finally {
        pending = false;
      }
    };
    void refreshProgress();
    const timer = window.setInterval(() => void refreshProgress(), 1000);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [loadConfig, processingImportId, selectedExamId]);

	useEffect(() => { setReviewDrafts(paperImports[0]?.questions ?? []); }, [paperImports]);

	useEffect(() => {
		const onPaste = (event: ClipboardEvent) => {
			if (isTextPasteTarget(event.target)) return;
			const files = filesFromClipboard(event.clipboardData);
			if (!files.length || !selectedExam || !canManage) return;
			event.preventDefault();
			materialUploadRef.current(files);
		};
		window.addEventListener("paste", onPaste);
		return () => window.removeEventListener("paste", onPaste);
	}, [canManage, selectedExam]);

  useEffect(() => {
    if (selectedQuestion) {
      const answerArea = selectedQuestion.answer_area ?? {};
      const rawTolerance = selectedQuestion.answer_key?.tolerance;
      const tolerance: Record<string, unknown> =
        rawTolerance && typeof rawTolerance === "object" && !Array.isArray(rawTolerance) ? (rawTolerance as Record<string, unknown>) : {};
      form.setFieldsValue({
        exam_paper_id: selectedQuestion.exam_paper_id,
        question_no: selectedQuestion.question_no,
        question_type: selectedQuestion.question_type,
        score: selectedQuestion.score,
        stem: selectedQuestion.stem ?? "",
        knowledge_points: selectedQuestion.knowledge_points,
        answer_area_page: Number(answerArea.page ?? 1),
        answer_area_x: Number(answerArea.x ?? 0),
        answer_area_y: Number(answerArea.y ?? 0),
        answer_area_w: Number(answerArea.w ?? 0),
        answer_area_h: Number(answerArea.h ?? 0),
        sort_order: selectedQuestion.sort_order,
        standard_answer: String(selectedQuestion.answer_key?.standard_answer ?? ""),
        equivalent_answers: (selectedQuestion.answer_key?.equivalent_answers ?? []).map((item) => String(item)),
        tolerance_absolute: tolerance.absolute === undefined || tolerance.absolute === null ? undefined : Number(tolerance.absolute),
        tolerance_relative:
          tolerance.relative === undefined || tolerance.relative === null ? undefined : Number((Number(tolerance.relative) * 100).toFixed(6))
      });
      setRubricStatus(selectedQuestion.rubric?.status ?? "draft");
      setRubricPoints(selectedQuestion.rubric?.points ?? []);
      setDeductionsJson(jsonText(selectedQuestion.rubric?.deductions, ""));
      setExamplesJson(jsonText(selectedQuestion.rubric?.examples, ""));
      return;
    }
    form.setFieldsValue({
      exam_paper_id: papers[0]?.id,
      question_no: "",
      question_type: "short_answer",
      score: 10,
      stem: "",
      knowledge_points: [],
      answer_area_page: 1,
      answer_area_x: 0,
      answer_area_y: 0,
      answer_area_w: 0,
      answer_area_h: 0,
      sort_order: questions.length + 1,
      standard_answer: "",
      equivalent_answers: [],
      tolerance_absolute: undefined,
      tolerance_relative: undefined
    });
    setRubricStatus("draft");
    setRubricPoints([{ id: "p1", description: "", score: 10, required: true }]);
    setDeductionsJson("");
    setExamplesJson("");
  }, [form, papers, questions.length, selectedQuestion]);

  useEffect(() => {
    let active = true;
    async function loadRules() {
      if (!selectedQuestion || !objectiveRuleType) {
        setScoringRules([]);
        setScoringRuleConfig({});
        return;
      }
      try {
        const result = await listScoringRules(selectedQuestion.id);
        if (!active) return;
        setScoringRules(result.scoring_rules);
        const editable = result.scoring_rules.find((rule) => rule.status === "draft") ?? result.scoring_rules.find((rule) => rule.status === "published");
        setScoringRuleConfig(editable?.config ?? {});
      } catch (currentError) {
        if (active) message.error(formatError(currentError));
      }
    }
    void loadRules();
    return () => { active = false; };
  }, [message, objectiveRuleType, selectedQuestion]);

  function setRuleConfig(key: string, value: unknown) {
    setScoringRuleConfig((current) => ({ ...current, [key]: value }));
  }

  async function saveScoringRule(publish: boolean) {
    if (!selectedQuestion || !objectiveRuleType) return;
    setSavingScoringRule(true);
    try {
      const saved = draftScoringRule
        ? (await updateScoringRule(draftScoringRule.id, scoringRuleConfig, draftScoringRule.revision)).scoring_rule
        : (await createScoringRule(selectedQuestion.id, objectiveRuleType, scoringRuleConfig)).scoring_rule;
      if (publish) {
        await publishScoringRule(saved.id);
        message.success("评分规则已发布并锁定版本");
      } else {
        message.success("评分规则草稿已保存");
      }
      const result = await listScoringRules(selectedQuestion.id);
      setScoringRules(result.scoring_rules);
      const editable = result.scoring_rules.find((rule) => rule.status === "draft") ?? result.scoring_rules.find((rule) => rule.status === "published");
      setScoringRuleConfig(editable?.config ?? {});
      onExamChanged?.();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setSavingScoringRule(false);
    }
  }

  const rubricColumns: TableColumnsType<RubricPoint> = [
    {
      title: "采分点",
      dataIndex: "description",
      render: (_, point, index) => (
        <Input
          value={point.description}
          disabled={questionDisabled}
          placeholder="采分点描述"
          onChange={(event) => updatePoint(index, { description: event.target.value })}
        />
      )
    },
    {
      title: "分值",
      dataIndex: "score",
      width: 120,
      render: (_, point, index) => (
        <InputNumber
          value={point.score}
          disabled={questionDisabled}
          min={0}
          precision={1}
          className="full-width-control"
          onChange={(value) => updatePoint(index, { score: Number(value ?? 0) })}
        />
      )
    },
    {
      title: "必需",
      dataIndex: "required",
      width: 90,
      render: (_, point, index) => (
        <Switch checked={point.required} disabled={questionDisabled} onChange={(checked) => updatePoint(index, { required: checked })} />
      )
    },
    ...(showFormulaEvidence
      ? [
          {
            title: "识别证据（仅供教师参考）",
            width: 380,
            render: (_: unknown, point: RubricPoint, index: number) => {
              const requirements = point.evidence_requirements ?? [];
              return (
                <Space direction="vertical" size={6} className="full-width-control">
                  {requirements.map((requirement, requirementIndex) => (
                    <Space key={`${requirement.type}-${requirementIndex}`} wrap size={6}>
                      <Select
                        value={requirement.type}
                        options={formulaEvidenceOptions}
                        disabled={questionDisabled}
                        className="rubric-evidence-type-select"
                        onChange={(type: RubricEvidenceRequirement["type"]) =>
                          updateEvidenceRequirement(index, requirementIndex, {
                            type,
                            target: type === "concept" || type === "unit" || type === "domain" ? requirement.target ?? "" : undefined,
                            minimum: undefined,
                            children: undefined
                          })
                        }
                      />
                      {requirement.type === "concept" || requirement.type === "unit" || requirement.type === "domain" ? (
                        <Input
                          value={requirement.target}
                          disabled={questionDisabled}
                          placeholder={requirement.type === "concept" ? "例如：配方法" : requirement.type === "unit" ? "例如：m/s" : "例如：x ≥ 0"}
                          className="rubric-evidence-target-input"
                          onChange={(event) => updateEvidenceRequirement(index, requirementIndex, { target: event.target.value })}
                        />
                      ) : null}
                      <Button
                        type="text"
                        danger
                        size="small"
                        aria-label="删除识别证据"
                        disabled={questionDisabled}
                        icon={<Trash2 size={14} />}
                        onClick={() => removeEvidenceRequirement(index, requirementIndex)}
                      />
                    </Space>
                  ))}
                  <Button
                    type="link"
                    size="small"
                    icon={<Plus size={14} />}
                    disabled={questionDisabled}
                    onClick={() => addEvidenceRequirement(index)}
                  >
                    添加识别证据
                  </Button>
                </Space>
              );
            }
          } satisfies TableColumnsType<RubricPoint>[number]
        ]
      : []),
    {
      title: "操作",
      width: 90,
      render: (_, __, index) => (
        <Button danger size="small" icon={<Trash2 size={14} />} disabled={questionDisabled || rubricPoints.length <= 1} onClick={() => removePoint(index)} />
      )
    }
  ];

  function updatePoint(index: number, patch: Partial<RubricPoint>) {
    setRubricPoints((current) => current.map((point, currentIndex) => (currentIndex === index ? { ...point, ...patch } : point)));
  }

  function addEvidenceRequirement(pointIndex: number) {
    updatePoint(pointIndex, {
      evidence_requirements: [
        ...(rubricPoints[pointIndex]?.evidence_requirements ?? []),
        { type: "valid_transformation" }
      ]
    });
  }

  function updateEvidenceRequirement(pointIndex: number, requirementIndex: number, patch: Partial<RubricEvidenceRequirement>) {
    const requirements = rubricPoints[pointIndex]?.evidence_requirements ?? [];
    updatePoint(pointIndex, {
      evidence_requirements: requirements.map((requirement, index) => (index === requirementIndex ? { ...requirement, ...patch } : requirement))
    });
  }

  function removeEvidenceRequirement(pointIndex: number, requirementIndex: number) {
    const requirements = rubricPoints[pointIndex]?.evidence_requirements ?? [];
    updatePoint(pointIndex, { evidence_requirements: requirements.filter((_, index) => index !== requirementIndex) });
  }

  function removePoint(index: number) {
    setRubricPoints((current) => current.filter((_, currentIndex) => currentIndex !== index));
  }

  function addPoint() {
    setRubricPoints((current) => [...current, { id: `p${current.length + 1}`, description: "", score: 0, required: false }]);
  }

  const uploadProps: UploadProps = {
    showUploadList: false,
		multiple: true,
    accept: ".pdf,.docx,.png,.jpg,.jpeg,.tif,.tiff,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,image/png,image/jpeg,image/tiff",
    beforeUpload: (file, fileList) => {
			if (file.uid === fileList[0]?.uid) void handleMaterialFiles(fileList as File[]);
      return false;
    }
  };

  async function handleMaterialFiles(files: File[]) {
		if (!selectedExam || !files.length) { message.error("请先选择考试，再添加考试资料"); return; }
		if (paperImports.some((item) => item.status === "processing")) { message.warning("当前资料仍在识别，请完成后再继续添加"); return; }
		const supportedFiles = files.filter(isSupportedPaperImportFile);
		if (supportedFiles.length !== files.length) message.warning("已忽略不支持的文件，仅接受 PDF、Word、PNG、JPG、JPEG、TIFF");
		if (!supportedFiles.length) return;
    setParsing(true);
    try {
			const uploaded = [];
			for (const file of supportedFiles) uploaded.push((await uploadFile(file, { owner_type: "import", owner_id: selectedExam.id, exam_id: selectedExam.id, school_id: selectedExam.school_id })).file);
			const activeImport = paperImports.find((item) => item.status === "review_required" || item.status === "failed" || item.status === "cancelled");
			const startIndex = activeImport?.sources.length ?? 0;
			const sources = uploaded.map((file, index) => ({ file_asset_id: file.id, document_index: startIndex + index, role_hint: "auto" as const }));
			const commandId = crypto.randomUUID();
			const result = activeImport ? await addPaperImportSources(activeImport.id, activeImport.generation, sources, commandId) : await createPaperImport(selectedExam.id, { exam_paper_id: papers[0]?.id, subject: selectedExam.subject, sources }, commandId);
      if (result.import.status === "failed") {
				message.error(getPaperImportUserMessage(result.import.issues[0], "考试资料识别失败，请检查资料后重试"));
      } else {
				message.success(`已添加 ${supportedFiles.length} 份资料，系统正在识别和匹配`);
      }
      await loadConfig(selectedExam.id, { silent: true });
    } catch (currentError) {
			await loadConfig(selectedExam.id, { silent: true }).catch(() => undefined);
      message.error(formatError(currentError));
    } finally {
			setParsing(false);
    }
  }
	materialUploadRef.current = (files) => { void handleMaterialFiles(files); };

	async function replaceImportSources(job: PaperImportJob, sources: PaperImportJob["sources"]) {
		if (job.status !== "processing" && job.status !== "review_required" && job.status !== "failed" && job.status !== "cancelled") return;
		setUpdatingImportSources(true);
		try {
			await replacePaperImportSources(job.id, job.generation, sources.map((source, documentIndex) => ({ id: source.id, document_index: documentIndex, role_hint: source.role_hint })), crypto.randomUUID());
			message.success(sources.length ? "资料顺序或类型已更新，系统正在重新匹配" : "已删除全部考试资料，请重新上传正确的资料");
			if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
		} catch (currentError) {
			message.error(formatError(currentError));
		} finally {
			setUpdatingImportSources(false);
		}
	}

	function stopPaperImport(job: PaperImportJob) {
		if (job.status !== "processing" || stoppingImport) return;
		modal.confirm({
			title: "停止当前识别？",
			content: "系统会停止尚未完成的文字识别和 AI 解析；已上传资料会保留，之后可以重新识别。",
			okText: "停止识别",
			cancelText: "继续识别",
			okButtonProps: { danger: true },
			onOk: async () => {
				setStoppingImport(true);
				try {
					await cancelPaperImport(job.id, job.generation, crypto.randomUUID());
					message.success("已停止识别，上传资料仍然保留");
					if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
				} catch (currentError) {
					message.error(formatError(currentError));
				} finally {
					setStoppingImport(false);
				}
			}
		});
	}

	async function retryImportParse(job: PaperImportJob) {
		if (job.status !== "failed" || job.error_code !== "ai_parse_failed" || retryingParse) return;
		setRetryingParse(true);
		try {
			await retryPaperImportParse(job.id, job.generation);
			message.success("已复用文字和公式识别结果，正在重新解析题目结构");
			if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
		} catch (currentError) {
			message.error(formatError(currentError));
		} finally {
			setRetryingParse(false);
		}
	}

	async function removeImportSource(job: PaperImportJob, sourceID: string) {
		await replaceImportSources(job, orderedSourcesAfterRemoval(job.sources, sourceID));
	}

	async function openImportSource(fileAssetID: string, pageNo?: number) {
		try {
			const file = await downloadFileBlob(fileAssetID);
			const url = URL.createObjectURL(file.blob);
			window.open(pageNo && pageNo > 0 ? `${url}#page=${pageNo}` : url, "_blank", "noopener,noreferrer");
			window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
		} catch (currentError) {
			message.error(formatError(currentError));
		}
	}

  async function confirmPaperImport(job: PaperImportJob) {
    if (!selectedExam) return;
    const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
    if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
    setParsing(true);
    try {
      await savePaperImportReview(job.id, job.generation, confirmedImportDrafts(reviewDrafts));
      await applyPaperImport(job.id);
      message.success("题目、标准答案、教师解析和评分点已写入当前考试");
      await loadConfig(selectedExam.id);
      onExamChanged?.();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setParsing(false);
    }
  }

	async function saveImportReview(job: PaperImportJob) {
		if (!selectedExam) return;
		const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
		if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
		setSavingImportReview(true);
		try {
			const confirmedDrafts = confirmedImportDrafts(reviewDrafts);
			await savePaperImportReview(job.id, job.generation, confirmedDrafts);
			message.success("人工核对结果已保存，后续追加资料不会覆盖已确认字段");
			await loadConfig(selectedExam.id);
		} catch (currentError) {
			message.error(formatError(currentError));
		} finally {
			setSavingImportReview(false);
		}
	}

  async function saveQuestion() {
    if (!selectedExam) {
      message.error("请先选择考试");
      return;
    }
    const values = await form.validateFields();
    const answerAreaBase = editorMode === "edit" ? selectedQuestion?.answer_area ?? {} : {};
    const answerArea: Record<string, unknown> = {
      ...answerAreaBase,
      page: values.answer_area_page,
      x: values.answer_area_x,
      y: values.answer_area_y,
      w: values.answer_area_w,
      h: values.answer_area_h
    };
    const rawTolerance = editorMode === "edit" ? selectedQuestion?.answer_key?.tolerance : undefined;
    const tolerance: Record<string, unknown> =
      rawTolerance && typeof rawTolerance === "object" && !Array.isArray(rawTolerance) ? { ...(rawTolerance as Record<string, unknown>) } : {};
    if (toleranceQuestionTypes.includes(values.question_type)) {
      if (values.tolerance_absolute === undefined || values.tolerance_absolute === null) {
        delete tolerance.absolute;
      } else {
        tolerance.absolute = values.tolerance_absolute;
      }
      if (values.tolerance_relative === undefined || values.tolerance_relative === null) {
        delete tolerance.relative;
      } else {
        tolerance.relative = Number((values.tolerance_relative / 100).toFixed(8));
      }
    }
    const payload: QuestionPayload = {
      exam_paper_id: values.exam_paper_id,
      question_no: values.question_no,
      question_type: values.question_type,
      score: values.score,
      stem: values.stem,
      knowledge_points: values.knowledge_points ?? [],
      answer_area: answerArea,
      sort_order: values.sort_order,
      answer_key: {
        standard_answer: values.standard_answer,
        equivalent_answers: values.equivalent_answers ?? [],
        tolerance
      }
    };
    setSavingQuestion(true);
    try {
      if (editorMode === "edit" && selectedQuestion) {
        await updateQuestion(selectedQuestion.id, payload);
        message.success("题目已更新，标准答案将产生新版本");
      } else {
        await createQuestion(selectedExam.id, payload);
        message.success("题目已创建");
      }
      await loadConfig(selectedExam.id);
      onExamChanged?.();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setSavingQuestion(false);
    }
  }

  function confirmDeleteQuestion() {
    if (!selectedQuestion || !selectedExam) {
      return;
    }
    modal.confirm({
      title: "删除题目",
      content: `确认删除 ${selectedQuestion.question_no}？`,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deleteQuestion(selectedQuestion.id);
        message.success("题目已删除");
        setEditorMode("create");
        setSelectedQuestionId(null);
        await loadConfig(selectedExam.id);
        onExamChanged?.();
      }
    });
  }

  async function saveRubric() {
    if (!selectedQuestion || !selectedExam) {
      message.error("请先选择已保存的题目");
      return;
    }
    const total = pointTotal(rubricPoints);
    if (Math.abs(total - selectedQuestion.score) > 0.0001) {
      message.error("评分细则采分点总分必须等于题目分值");
      return;
    }
    let deductions: unknown[];
    let examples: unknown[];
    try {
      deductions = parseArrayJSON(deductionsJson, "扣分点");
      examples = parseArrayJSON(examplesJson, "样例答案");
    } catch (currentError) {
      message.error(formatError(currentError));
      return;
    }
    setSavingRubric(true);
    try {
      await createRubric(selectedQuestion.id, {
        status: rubricStatus,
        max_score: selectedQuestion.score,
        points: rubricPoints,
        deductions,
        examples
      });
      message.success(rubricStatus === "locked" ? "评分细则已锁定" : "评分细则新版本已提交");
      await loadConfig(selectedExam.id);
      onExamChanged?.();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setSavingRubric(false);
    }
  }

  async function runValidation() {
    if (!selectedExam) {
      return;
    }
    try {
      const result = await validatePaperConfig(selectedExam.id);
      setValidation(result.result);
      if (result.result.valid) {
        message.success("试卷配置检查通过");
      } else {
        message.warning("试卷配置存在问题，请查看下方详情");
      }
    } catch (currentError) {
      message.error(formatError(currentError));
    }
  }

  const scoreMismatch = selectedQuestion ? Math.abs(pointTotal(rubricPoints) - selectedQuestion.score) > 0.0001 : false;
	const latestPaperImport = paperImports[0];
	const importCancelled = latestPaperImport ? isPaperImportCancelled(latestPaperImport) : false;
	const noExamContentDetected = latestPaperImport ? hasNoExamContentDetected(latestPaperImport) : false;
	const visibleImportIssues = importCancelled || latestPaperImport?.status === "processing" ? [] : noExamContentDetected
		? (latestPaperImport?.structured_issues ?? []).filter((issue) => issue.code === "NO_EXAM_CONTENT_DETECTED")
		: latestPaperImport?.structured_issues?.length
		? latestPaperImport.structured_issues.filter((issue) => issue.severity !== "info")
		: (latestPaperImport?.issues ?? []).map((message) => ({ message, certainty: "unknown" as const }));
	const importSummary = latestPaperImport ? paperImportSummary(latestPaperImport) : null;
	const excludedPageFurniture = importCancelled || latestPaperImport?.status === "processing" ? [] : (latestPaperImport?.structured_issues ?? []).filter((issue) => issue.code === "PAGE_FURNITURE_EXCLUDED");
	const importProgress = latestPaperImport ? paperImportProgress(latestPaperImport) : null;
	const progressTiming = importProgress?.startedAt
		? `已运行 ${Math.max(0, Math.floor((Date.now() - new Date(importProgress.startedAt).getTime()) / 1000))} 秒 · ${Math.max(0, Math.floor((Date.now() - new Date(importProgress.changedAt ?? importProgress.startedAt).getTime()) / 1000))} 秒前有新进展 · 心跳 ${Math.max(0, Math.floor((Date.now() - new Date(importProgress.updatedAt ?? importProgress.startedAt).getTime()) / 1000))} 秒前`
		: "";
	const invalidReviewRubric = reviewDrafts.some(reviewRubricHasScoreMismatch);
	const canEditImportSources = latestPaperImport?.status === "processing" || latestPaperImport?.status === "review_required" || latestPaperImport?.status === "failed" || latestPaperImport?.status === "cancelled";
	function updateReviewDraft(index: number, field: string, patch: Partial<PaperImportDraftQuestion>) {
		setReviewDrafts((current) => current.map((draft, draftIndex) => draftIndex === index ? markImportFieldConfirmed(draft, field, patch) : draft));
	}
  const editorVisible = !loading && !error && Boolean(selectedExam) && !configLoading && !configError;

  return (
    <div className="page-stack">
      {!editorVisible ? <Form form={form} component={false} /> : null}
      <section className="page-heading">
        <div>
          <Space>
            <h1>考试资料与评分配置</h1>
            {canManage&&canReadQuestionBank&&selectedExam&&["draft","configured"].includes(selectedExam.status)?<BankQuestionPicker scopeKey={questionBankScope} examId={selectedExam.id} nextOrder={questions.length+1} onCopied={async()=>{await loadConfig(selectedExam.id);onExamChanged?.();}}/>:null}
			{canImportQuestionBank&&selectedExam&&!["draft","configured"].includes(selectedExam.status)&&questions.length?<HistoryQuestionImporter key={`${questionBankScope}:${selectedExam.id}`} scopeKey={questionBankScope} examId={selectedExam.id} questions={questions} onImported={async()=>{await loadConfig(selectedExam.id,{silent:true});}} onOpenBank={onNavigate?()=>onNavigate("/question-bank"):undefined}/>:null}
          </Space>
          <p>手里有什么考试资料就直接添加；系统自动识别题目、答案与解析，并提示需要核对的缺失或冲突。</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} onClick={() => void loadExams()} loading={loading}>
            刷新
          </Button>
          <Button icon={<CheckCircle2 size={16} />} disabled={!selectedExam} onClick={() => void runValidation()}>
            完整性检查
          </Button>
        </Space>
      </section>

      <section className="workspace-section filter-panel">
        <div className="paper-topline">
          <Select
            className="exam-picker"
            placeholder="选择考试"
            value={selectedExamId || undefined}
            options={exams.map((exam) => ({ label: exam.name, value: exam.id }))}
            onChange={(value) => setSelectedExamId(value)}
            loading={loading}
          />
          {selectedExam ? <span className="muted">当前考试总分：{selectedExam.total_score}</span> : null}
        </div>
      </section>

      {loading ? (
        <section className="workspace-section">
          <LoadingState label="正在读取考试列表" />
        </section>
      ) : error ? (
        <ErrorState message={error} onRetry={() => void loadExams()} />
      ) : !selectedExam ? (
        <section className="workspace-section">
          <EmptyState title="暂无考试" description="还没有可配置的考试。请先在「考试管理」中创建考试。" />
        </section>
      ) : configLoading ? (
        <section className="workspace-section">
          <LoadingState label="正在读取试卷与题目配置" />
        </section>
      ) : configError ? (
        <ErrorState message={configError} onRetry={() => void loadConfig(selectedExam.id)} />
      ) : (
        <>
          <section className="workspace-section paper-source-files">
            <div className="section-head">
              <div>
						<h2>上传考试资料</h2>
						<p>把试题、答案、解析、评分标准直接放到这里，系统会自动识别内容并进行匹配。</p>
              </div>
            </div>
				<Upload.Dragger {...uploadProps} disabled={!canManage || parsing} className="paper-import-dropzone">
					<div className="paper-import-upload-content">
						<FileUp size={22} />
						<strong>{parsing ? "正在上传并识别…" : "点击选择、拖拽，或直接 Ctrl+V / Cmd+V 粘贴"}</strong>
						<span>PDF、Word、PNG、JPG、JPEG、TIFF；可一次添加多份资料</span>
					</div>
				</Upload.Dragger>
				{latestPaperImport?.sources.length ? (
					<List size="small" className="paper-import-source-list" dataSource={latestPaperImport.sources} renderItem={(source, index) => (
						<List.Item actions={[
							<Button key="open" type="text" size="small" icon={<ExternalLink size={14} />} onClick={() => void openImportSource(source.file_asset_id)}>来源</Button>,
							<Button key="up" type="text" size="small" aria-label="上移资料" icon={<ChevronUp size={14} />} disabled={index === 0 || !canEditImportSources || updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, orderedSourcesAfterMove(latestPaperImport.sources, source.id, -1))} />,
							<Button key="down" type="text" size="small" aria-label="下移资料" icon={<ChevronDown size={14} />} disabled={index === latestPaperImport.sources.length - 1 || !canEditImportSources || updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, orderedSourcesAfterMove(latestPaperImport.sources, source.id, 1))} />,
							<Select<PaperImportRole> key="role" size="small" aria-label="资料类型" value={source.role_hint} options={importRoleOptions} disabled={!canEditImportSources || updatingImportSources} onChange={(roleHint) => void replaceImportSources(latestPaperImport, sourcesAfterRoleChange(latestPaperImport.sources, source.id, roleHint))} />,
							<Button key="remove" type="text" danger size="small" aria-label="删除资料" icon={<Trash2 size={14} />} disabled={!canEditImportSources || updatingImportSources} onClick={() => void removeImportSource(latestPaperImport, source.id)} />
						]} extra={<StatusTag tone={importCancelled ? "neutral" : source.processing_status === "processed" ? "success" : source.processing_status === "failed" ? "danger" : "processing"}>{importCancelled ? "已停止" : source.processing_status === "processed" ? "已识别" : source.processing_status === "failed" ? "失败" : "处理中"}</StatusTag>}>
							<List.Item.Meta title={`${source.document_index + 1}. ${source.original_name || "考试资料"}`} description={`识别内容：${source.detected_role === "question" ? "题目" : source.detected_role === "answer" ? "答案" : source.detected_role === "solution" ? "解析" : source.detected_role === "rubric" ? "评分标准" : source.detected_role === "mixed" ? "混合内容" : "识别中"}${source.role_confidence ? ` · 资料类型判断 ${Math.round(source.role_confidence * 100)}%（不代表逐字准确率）` : ""}`} />
						</List.Item>
					)} />
				) : null}
          </section>

          {latestPaperImport ? (
            <section className="workspace-section paper-import-review">
              <div className="section-head">
                <div>
                  <h2>自动识别结果</h2>
                  <p>
                    {latestPaperImport.status === "review_required"
						? noExamContentDetected
							? "未识别到与考试有关的题目、答案、解析或评分标准，请检查是否上传了无关图片或错误文件"
							: `已识别题目 ${latestPaperImport.question_candidates?.length ?? latestPaperImport.questions.length}、答案 ${latestPaperImport.answer_candidates?.length ?? 0}、解析 ${latestPaperImport.solution_candidates?.length ?? 0}、评分标准 ${latestPaperImport.rubric_candidates?.length ?? 0}`
                      : latestPaperImport.status === "applied"
                        ? "已确认并写入当前考试"
                        : latestPaperImport.status === "failed"
                          ? getPaperImportUserMessage(latestPaperImport.issues[0], "考试资料识别失败，请检查资料后重试")
						  : latestPaperImport.status === "cancelled"
							? "识别任务已手动停止，已上传资料仍然保留"
                          : "正在识别考试资料中的题目、答案与解析"}
                  </p>
                </div>
                {latestPaperImport.status === "review_required" ? (
					noExamContentDetected ? (
						<Space wrap><Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别</Button><Button onClick={() => { setShowQuestionEditor(true); document.getElementById("paper-question-summary")?.scrollIntoView({ behavior: "smooth", block: "start" }); }}>手动补充题目</Button></Space>
					) : <Space><Button loading={savingImportReview} onClick={() => void saveImportReview(latestPaperImport)}>保存人工核对</Button><Button type="primary" loading={parsing} disabled={hasBlockingImportIssues(latestPaperImport) || invalidReviewRubric} onClick={() => void confirmPaperImport(latestPaperImport)}>确认导入</Button></Space>
                ) : latestPaperImport.status === "failed" && latestPaperImport.error_code === "ai_parse_failed" ? (
					<Space wrap><Button type="primary" loading={retryingParse} onClick={() => void retryImportParse(latestPaperImport)}>仅重新解析</Button><Button loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别全部</Button></Space>
				) : latestPaperImport.status === "failed" && latestPaperImport.sources.length ? (
					<Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别</Button>
				) : latestPaperImport.status === "cancelled" && latestPaperImport.sources.length ? (
					<Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别</Button>
				) : latestPaperImport.status === "processing" ? (
					<Button danger loading={stoppingImport} onClick={() => stopPaperImport(latestPaperImport)}>停止识别</Button>
				) : null}
              </div>
			  {latestPaperImport.status === "processing" && importProgress ? (
				<div className="paper-import-progress" aria-live="polite">
					<div><strong>{importProgress.label}</strong><span>{importProgress.detail}{progressTiming ? ` · ${progressTiming}` : ""}</span></div>
					{importProgress.percent === undefined ? <div className="paper-import-progress-indeterminate" role="progressbar" aria-label="正在处理，暂无可计算的完成比例"><span /></div> : <Progress percent={importProgress.percent} format={() => importProgress.counter ?? `${importProgress.percent}%`} status="active" />}
				</div>
			  ) : null}
			  {latestPaperImport.status === "cancelled" && importProgress ? (
				<div className="paper-import-progress" aria-live="polite">
					<div><strong>{importProgress.label}</strong><span>{importProgress.detail}</span></div>
					{importProgress.percent === undefined ? null : <Progress percent={importProgress.percent} status="normal" />}
				</div>
			  ) : null}
              {latestPaperImport.status === "review_required" ? (
                <div className="paper-import-facts">
					<span><strong>{importSummary?.questions ?? 0}</strong> 道题目</span>
					<span><strong>{importSummary?.answers ?? 0}</strong> 个答案</span>
					<span><strong>{importSummary?.solutions ?? 0}</strong> 份解析</span>
					<span><strong>{latestPaperImport.rubric_candidates?.length ?? 0}</strong> 份评分标准</span>
					<span><strong>{importSummary?.reviewIssues ?? 0}</strong> 项需核对</span>
                  <span><strong>{latestPaperImport.questions.reduce((sum, item) => sum + item.score, 0)}</strong> 分</span>
                </div>
              ) : null}
			{latestPaperImport.status === "review_required" && reviewDrafts.length ? <PaperImportReviewPanel job={latestPaperImport} drafts={reviewDrafts} onChange={updateReviewDraft} onOpenSource={(ref) => void openImportSource(ref.file_asset_id, ref.page_no)} /> : null}
			{excludedPageFurniture.length ? <details>
				<summary>已自动排除 {excludedPageFurniture.length} 项页眉、页脚或水印</summary>
				<ul className="validation-issue-list">{excludedPageFurniture.map((issue, index) => <li key={`${issue.message}-${index}`}>
					{issue.message}{issue.source_refs?.[0]?.file_asset_id ? <Button type="link" size="small" onClick={() => void openImportSource(issue.source_refs[0].file_asset_id, issue.source_refs[0].page_no)}>核对来源</Button> : null}
				</li>)}</ul>
				<p>原图和 OCR 原文仍保留；如果正文被误排除，请对照来源补充。</p>
			</details> : null}
			{latestPaperImport.status === "applied" ? <div className="paper-import-result"><div className="paper-import-facts"><span>已写入题目：<strong>{latestPaperImport.questions.length}</strong></span><span>已配置答案：<strong>{latestPaperImport.questions.filter((item) => item.answer_key).length}</strong></span><span>已导入解析：<strong>{latestPaperImport.questions.filter((item) => item.solution).length}</strong></span><span>已配置评分标准：<strong>{latestPaperImport.questions.filter((item) => item.rubric).length}</strong></span><span>已锁定评分标准：<strong>{latestPaperImport.questions.filter((item) => item.rubric?.status === "locked").length}</strong></span><span>仍需处理：<strong>{latestPaperImport.questions.filter((item) => rubricRequiredArchetypes.has(item.assessment_archetype ?? "") && item.rubric?.status !== "locked").length}</strong></span></div><Space wrap><Button onClick={() => document.getElementById("paper-question-summary")?.scrollIntoView({ behavior: "smooth", block: "start" })}>去逐题校对</Button>{initialExamId && onNavigate ? <Button type="primary" onClick={() => onNavigate(`/exams/${encodeURIComponent(initialExamId)}/settings`)}>去考试准备</Button> : null}</Space></div> : null}
			  {visibleImportIssues.length ? (
				<Alert type={latestPaperImport.status === "failed" || (latestPaperImport.structured_issues ?? []).some((item) => item.severity === "error") ? "error" : "warning"} showIcon message={noExamContentDetected ? "未识别到考试内容" : "需要核对"} description={
					<ul className="validation-issue-list">{visibleImportIssues.map((issue, index) => <li key={`${issue.message}-${index}`}><strong>{"question_no" in issue && issue.question_no ? `第${issue.question_no}题：` : ""}</strong>{getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}{issue.certainty === "suspected" ? "（疑似）" : ""}{"source_refs" in issue && issue.source_refs?.[0]?.file_asset_id ? <Button type="link" size="small" onClick={() => void openImportSource(issue.source_refs[0].file_asset_id, issue.source_refs[0].page_no)}>查看来源</Button> : null}</li>)}</ul>
				} />
              ) : null}
            </section>
          ) : null}

          {validation ? (
            <Alert
              type={validation.valid ? "success" : "warning"}
              showIcon
              message={validation.valid ? "试卷配置完整" : "试卷配置存在问题"}
              description={
                validation.valid ? (
                  "检查完成，未发现配置问题。"
                ) : (
                  <ul className="validation-issue-list">
                    {validation.issues.map((issue, index) => (
                      <li key={`${issue.code}-${index}`}>
                        {getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}
                      </li>
                    ))}
                  </ul>
                )
              }
            />
          ) : null}

          <section id="paper-question-summary" className="workspace-section paper-question-summary">
            <div>
              <strong>{questions.length}</strong>
              <span>道题目</span>
              <small>{questions.filter((question) => question.answer_key).length} 道已配置标准答案</small>
            </div>
            <Button onClick={() => setShowQuestionEditor((current) => !current)}>
              {showQuestionEditor ? "收起逐题校对" : questions.length ? "逐题校对" : "手动补充题目"}
            </Button>
          </section>

          {showQuestionEditor ? <section className="paper-workbench">
            <aside className="workspace-section question-list-panel">
              <div className="section-head">
                <div>
                  <h2>题目列表</h2>
                  <p>{questions.length} 道题</p>
                </div>
                <Button
                  size="small"
                  icon={<Plus size={14} />}
                  disabled={!canManage}
                  onClick={() => {
                    setEditorMode("create");
                    setSelectedQuestionId(null);
                  }}
                >
                  新建
                </Button>
              </div>
              <List
                className="question-list"
                dataSource={questions}
                locale={{ emptyText: <EmptyState title="暂无题目" description="先新建题目配置。" /> }}
                renderItem={(question) => (
                  <List.Item
                    className={selectedQuestionId === question.id ? "question-list-item active" : "question-list-item"}
                    onClick={() => {
                      setEditorMode("edit");
                      setSelectedQuestionId(question.id);
                    }}
                  >
                    <div>
                      <strong>{question.question_no}</strong>
                      <span>{labelFrom(questionTypeOptions, question.question_type)}</span>
                    </div>
                    <div>
                      <span>{question.score} 分</span>
                      <span title={question.rubric?.status}>
                        <StatusTag tone={rubricTone(question.rubric?.status)}>{rubricStatusLabel(question.rubric?.status)}</StatusTag>
                      </span>
                    </div>
                  </List.Item>
                )}
              />
            </aside>

            <section className="workspace-section question-editor">
              <div className="section-head">
                <div>
                  <h2>{editorMode === "edit" ? "题目配置" : "新建题目"}</h2>
                  <p>{selectedLocked ? "评分细则已锁定，题目和细则不可编辑。" : "保存题目修改会生成新的答案版本。"}</p>
                </div>
                <Space>
                  {selectedLocked ? <StatusTag tone="success">已锁定</StatusTag> : null}
                  {editorMode === "edit" ? (
                    <Button danger icon={<Trash2 size={16} />} disabled={questionDisabled} onClick={confirmDeleteQuestion}>
                      删除
                    </Button>
                  ) : null}
                  <Button type="primary" icon={<Save size={16} />} disabled={questionDisabled} loading={savingQuestion} onClick={() => void saveQuestion()}>
                    保存题目
                  </Button>
                </Space>
              </div>

              <Form form={form} layout="vertical" disabled={questionDisabled}>
                <div className="form-grid">
                  <Form.Item label="关联试卷版本" name="exam_paper_id">
                    <Select
                      allowClear
                      options={papers.map((paper) => ({ label: `v${paper.version_no} ${paper.file.original_name || "未命名文件"}`, value: paper.id }))}
                      placeholder="可不关联"
                    />
                  </Form.Item>
                  <Form.Item label="题号" name="question_no" rules={[{ required: true, message: "请输入题号" }]}>
                    <Input placeholder="Q1" />
                  </Form.Item>
                  <Form.Item label="题型" name="question_type" rules={[{ required: true, message: "请选择题型" }]}>
                    <Select options={questionTypeOptions} />
                  </Form.Item>
                  <Form.Item label="分值" name="score" rules={[{ required: true, message: "请输入分值" }]}>
                    <InputNumber min={0.5} precision={1} className="full-width-control" />
                  </Form.Item>
                  <Form.Item label="排序" name="sort_order" rules={[{ required: true, message: "请输入排序" }]}>
                    <InputNumber min={1} precision={0} className="full-width-control" />
                  </Form.Item>
                  <Form.Item label="知识点" name="knowledge_points">
                    <Select mode="tags" placeholder="输入后回车" />
                  </Form.Item>
                </div>
                <Form.Item label="题干" name="stem">
                  <Input.TextArea rows={3} />
                </Form.Item>
                <details className="paper-advanced-settings"><summary>高级题目设置</summary><Form.Item label="答题区域位置" required>
                  <div className="form-grid">
                    <Form.Item label="所在页码" name="answer_area_page" rules={[{ required: true, message: "请输入所在页码" }]}>
                      <InputNumber min={1} precision={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="左边距" name="answer_area_x" rules={[{ required: true, message: "请输入左边距" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="上边距" name="answer_area_y" rules={[{ required: true, message: "请输入上边距" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="宽度" name="answer_area_w" rules={[{ required: true, message: "请输入宽度" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="高度" name="answer_area_h" rules={[{ required: true, message: "请输入高度" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                  </div>
                  <p className="muted">也可以在「答题卡模板」中拖拽框选，更直观。</p>
                </Form.Item></details>
                <Form.Item label="标准答案" name="standard_answer" rules={[{ required: true, message: "请输入标准答案" }]}>
                  <Input.TextArea rows={3} />
                </Form.Item>
                <details className="paper-advanced-settings"><summary>高级答案设置</summary><Form.Item label="等价答案" name="equivalent_answers">
                  <Select mode="tags" placeholder="输入后回车" />
                </Form.Item>
                {showTolerance ? (
                  <Form.Item label="答案误差范围（选填）">
                    <div className="form-grid">
                      <Form.Item label="允许绝对误差" name="tolerance_absolute">
                        <InputNumber min={0} className="full-width-control" />
                      </Form.Item>
                      <Form.Item label="允许相对误差（%）" name="tolerance_relative">
                        <InputNumber min={0} className="full-width-control" />
                      </Form.Item>
                    </div>
                  </Form.Item>
                ) : null}</details>
              </Form>

              <details className="paper-advanced-settings"><summary>高级评分配置</summary><AssessmentProfileEditor
                examId={selectedExam.id}
                examSubject={selectedExam.subject}
                examStatus={selectedExam.status}
                question={selectedQuestion}
                canManage={canManageAssessment}
                onChanged={onExamChanged}
              /></details>

              {selectedQuestion && objectiveRuleType ? <details className="paper-advanced-settings"><summary>客观题评分规则</summary><section className="scoring-rule-editor">
                <div className="section-head">
                  <div>
                    <h2>客观题评分规则</h2>
                    <p>规则发布后不可修改；再次调整会创建新版本。</p>
                  </div>
                  <Space>
                    {publishedScoringRule ? <StatusTag tone="success">{`已发布 v${publishedScoringRule.version}`}</StatusTag> : <StatusTag tone="warning">未发布</StatusTag>}
                    <Button icon={<Save size={16} />} disabled={!canManage} loading={savingScoringRule} onClick={() => void saveScoringRule(false)}>保存草稿</Button>
                    <Button type="primary" icon={<LockKeyhole size={16} />} disabled={!canManage} loading={savingScoringRule} onClick={() => void saveScoringRule(true)}>发布规则</Button>
                  </Space>
                </div>
                {objectiveRuleType === "multiple_choice" ? <div className="scoring-rule-grid">
                  <label><span>允许少选得分</span><Switch checked={Boolean(scoringRuleConfig.allow_partial)} onChange={(value) => setRuleConfig("allow_partial", value)} /></label>
                  <label><span>每个正确选项分值</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.score_per_correct_option ?? 0)} onChange={(value) => setRuleConfig("score_per_correct_option", Number(value ?? 0))} /></label>
                  <label><span>每个错误选项扣分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.wrong_option_penalty ?? selectedQuestion.score)} onChange={(value) => setRuleConfig("wrong_option_penalty", Number(value ?? 0))} /></label>
                  <label><span>最低得分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.minimum_score ?? 0)} onChange={(value) => setRuleConfig("minimum_score", Number(value ?? 0))} /></label>
                </div> : null}
                {objectiveRuleType === "fill_blank" ? <div className="scoring-rule-grid">
                  <label><span>忽略大小写</span><Switch checked={Boolean(scoringRuleConfig.ignore_case)} onChange={(value) => setRuleConfig("ignore_case", value)} /></label>
                  <label><span>忽略空格</span><Switch checked={Boolean(scoringRuleConfig.ignore_spaces)} onChange={(value) => setRuleConfig("ignore_spaces", value)} /></label>
                  <label><span>忽略标点</span><Switch checked={Boolean(scoringRuleConfig.ignore_punctuation)} onChange={(value) => setRuleConfig("ignore_punctuation", value)} /></label>
                </div> : null}
                {objectiveRuleType === "numeric" ? <div className="scoring-rule-grid">
                  <label><span>绝对误差</span><InputNumber min={0} precision={6} value={Number(scoringRuleConfig.absolute ?? 0)} onChange={(value) => setRuleConfig("absolute", Number(value ?? 0))} /></label>
                  <label><span>相对误差</span><InputNumber min={0} max={1} step={0.001} precision={6} value={Number(scoringRuleConfig.relative ?? 0)} onChange={(value) => setRuleConfig("relative", Number(value ?? 0))} /></label>
                  <label><span>单位必须填写</span><Switch checked={Boolean(scoringRuleConfig.unit_required)} onChange={(value) => setRuleConfig("unit_required", value)} /></label>
                </div> : null}
                {["single_choice", "true_false"].includes(objectiveRuleType) ? <Alert type="info" showIcon message="使用标准答案精确判定" description="空白、多涂、擦除或识别把握不足的答卷不会自动判零分，将转入人工确认。" /> : null}
              </section></details> : null}

              <div className="rubric-editor">
                <div className="section-head">
                  <div>
                    <h2>评分细则</h2>
                    <p>
                      {selectedQuestion ? `采分点合计 ${pointTotal(rubricPoints)} / ${selectedQuestion.score} 分` : "先保存题目，再配置评分细则"}
                    </p>
                  </div>
                  <Space>
                    <span className="muted">提交为</span>
                    <Select value={rubricStatus} options={rubricStatusOptions} disabled={questionDisabled} onChange={setRubricStatus} className="rubric-status-select" />
                    <Button icon={<Plus size={16} />} disabled={questionDisabled} onClick={addPoint}>
                      添加采分点
                    </Button>
                    <Button type="primary" icon={rubricStatus === "locked" ? <LockKeyhole size={16} /> : <Save size={16} />} disabled={questionDisabled || !selectedQuestion || scoreMismatch} loading={savingRubric} onClick={() => void saveRubric()}>
                      {rubricStatus === "locked" ? "锁定评分细则" : rubricStatus === "pending_review" ? "提交审批" : "保存评分细则"}
                    </Button>
                  </Space>
                </div>
                {scoreMismatch ? <Alert type="error" showIcon message="评分细则分值不匹配" description="采分点总分必须等于题目分值，调整后才能保存。" /> : null}
                {showFormulaEvidence ? (
                  <Alert
                    type="info"
                    showIcon
                    message="公式与步骤证据只作阅卷提示"
                    description="系统只会为数学、物理、化学的公式类题目生成这些证据；不会自动给分，最终分数仍由规则或教师确认。"
                  />
                ) : null}
                <ResponsiveTable<RubricPoint> rowKey="id" dataSource={rubricPoints} columns={rubricColumns} pagination={false} size="small" />
                <details className="paper-advanced-settings"><summary>高级评分数据</summary><div className="form-grid rubric-json-grid">
                  <label>
                    <span>扣分点（JSON 格式，选填）</span>
                    <Input.TextArea
                      value={deductionsJson}
                      disabled={questionDisabled}
                      rows={4}
                      spellCheck={false}
                      placeholder={'示例：[{"description": "缺少必要步骤", "score": -2}]'}
                      onChange={(event) => setDeductionsJson(event.target.value)}
                    />
                  </label>
                  <label>
                    <span>样例答案（JSON 格式，选填）</span>
                    <Input.TextArea
                      value={examplesJson}
                      disabled={questionDisabled}
                      rows={4}
                      spellCheck={false}
                      placeholder={'示例：["满分示例答案", "部分得分示例答案"]'}
                      onChange={(event) => setExamplesJson(event.target.value)}
                    />
                  </label>
                </div></details>
              </div>
            </section>
          </section> : null}
        </>
      )}
    </div>
  );
}
