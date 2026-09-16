import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  App,
  Button,
  Descriptions,
  Dropdown,
  Drawer,
  Input,
  InputNumber,
  List,
  Progress,
  Select,
  Space,
  Tooltip,
  Upload,
  type TableColumnsType,
  type UploadProps
} from "antd";
import {
  Eye,
  FileSearch,
  FileUp,
  Image as ImageIcon,
  MoreHorizontal,
  RefreshCw,
  RotateCcw,
  Search,
} from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { runImageQualityCheck } from "../api/capture";
import { downloadFileBlob, uploadFile, uploadFileWithProgress } from "../api/files";
import { listExams, type Exam } from "../api/exams";
import type { Student } from "../api/org";
import {
  addSubmissionPage,
  createOcrTask,
  createSubmission,
  generateAnswerSegments,
  getSubmission,
  listOcrTasks,
  replaceSubmissionPage,
  runQualityCheck,
  updateSubmissionStatus,
  type OcrTask,
  type QualityIssue,
  type Submission,
  type SubmissionPage
} from "../api/submissions";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { OcrWorkerAlert } from "../components/OcrWorkerAlert";
import { ProcessingOperationsPanel } from "../components/ProcessingOperationsPanel";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { DataTableShell, FilterBar, PageHeader } from "@edugrade/ui";
import type { StatusTone } from "../types";
import { hashQueryParam } from "../router/query";
import { ExclusiveCommandGate, LatestRequestGate, runCaptureCommand } from "../features/capture/captureWorkflow";
import {
  buildSubmissionView,
  loadCapturePage,
  loadNextCapturePage,
  type SubmissionView
} from "../features/capture/captureQueries";

type UploadRequest = Parameters<NonNullable<UploadProps["customRequest"]>>[0];
type QualityFilter = "all" | "blurry" | "missing_page" | "duplicate_page" | "ocr_failed" | "needs_manual_handling";

interface UploadQueueItem {
  id: string;
  fileName: string;
  status: "processing" | "success" | "error";
  phase: string;
  percent: number;
  error?: string;
}

interface PreviewState {
  url: string;
  contentType: string;
  filename?: string;
}

interface Filters {
  search: string;
  quality: QualityFilter;
  issue: "all" | "failed" | "unmatched" | "quality";
}

const qualityFilterOptions: { label: string; value: QualityFilter }[] = [
  { label: "全部问题类型", value: "all" },
  { label: "模糊", value: "blurry" },
  { label: "缺页", value: "missing_page" },
  { label: "重复页", value: "duplicate_page" },
  { label: "识别失败", value: "ocr_failed" },
  { label: "需要人工处理", value: "needs_manual_handling" }
];

const submissionStatusLabels: Record<string, string> = {
  created: "已创建",
  pages_uploaded: "已上传",
  quality_checked: "质量通过",
  ready_for_ocr: "待识别",
  rejected: "已拒绝"
};

const pageStatusLabels: Record<string, string> = {
  uploaded: "已上传",
  quality_checked: "质量通过",
  quality_failed: "质量未通过",
  replaced: "已替换"
};

function pageStatusTone(status: string): StatusTone {
  if (status === "quality_checked") {
    return "success";
  }
  if (status === "quality_failed" || status === "failed" || status === "rejected") {
    return "danger";
  }
  return "neutral";
}

const qualityStatusLabels: Record<string, string> = {
  unchecked: "未检查",
  passed: "通过",
  failed: "未通过"
};

const ocrStatusLabels: Record<string, string> = {
  queued: "排队中",
  processing: "处理中",
  completed: "已完成",
  failed: "失败"
};

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn(`API 请求失败 ${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function compactFileName(name: string) {
  return name.replace(/\.[^.]+$/, "").trim().slice(0, 64) || name.slice(0, 64);
}

function sourceTypeFor(file: File) {
  const name = file.name.toLowerCase();
  return file.type === "application/pdf" || name.endsWith(".pdf") ? "pdf_upload" : "image_upload";
}

function statusTone(status: string): StatusTone {
  if (status === "ready_for_ocr" || status === "quality_checked") {
    return "success";
  }
  if (status === "rejected") {
    return "danger";
  }
  if (status === "created") {
    return "neutral";
  }
  return "processing";
}

function ocrTone(task?: OcrTask): StatusTone {
  if (!task) {
    return "neutral";
  }
  if (task.status === "completed" && !task.requires_human_review) {
    return "success";
  }
  if (task.status === "failed") {
    return "danger";
  }
  if (task.requires_human_review) {
    return "warning";
  }
  return "processing";
}

function latestTask(tasks: OcrTask[]) {
  return [...tasks].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0];
}

function summaryString(row: SubmissionView, key: string) {
  const value = row.submission.summary?.[key];
  return typeof value === "string" ? value : "";
}

function summaryNumber(row: SubmissionView, key: string) {
  const value = row.submission.summary?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function summaryBoolean(row: SubmissionView, key: string) {
  return row.submission.summary?.[key] === true;
}

type SubmissionProcessingState = "pending" | "processing" | "completed" | "failed";

function processingState(row: SubmissionView): SubmissionProcessingState {
  const task = latestTask(row.ocrTasks);
  const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
  const segmentCount = row.segments.length || summaryNumber(row, "segment_count");
  if (row.submission.quality_status === "failed" || taskStatus === "failed") {
    return "failed";
  }
  if (taskStatus === "completed" && segmentCount > 0) {
    return "completed";
  }
  if (taskStatus === "queued" || taskStatus === "processing") {
    return "processing";
  }
  return "pending";
}

function ocrLabel(tasks: OcrTask[]) {
  const task = latestTask(tasks);
  if (!task) {
    return "未触发";
  }
  const suffix = task.requires_human_review ? " / 需人工" : "";
  return `${ocrStatusLabels[task.status] ?? "未知状态"}${suffix}`;
}

function derivedIssues(row: SubmissionView): QualityIssue[] {
  const issues = [...(row.submission.quality_issues ?? [])];
  for (const page of row.pages) {
    issues.push(...(page.quality_issues ?? []).map((issue) => ({ code: issue.code, message: `第 ${page.page_no} 页：${getSafeUserText(issue.message, "图片质量检查未通过")}` })));
  }
  if (row.ocrTasks.some((task) => task.status === "failed")) {
    issues.push({ code: "ocr_failed", message: "文字识别失败" });
  }
  if (summaryNumber(row, "ocr_failed_count") > 0 && !issues.some((issue) => issue.code === "ocr_failed")) {
    issues.push({ code: "ocr_failed", message: "文字识别失败" });
  }
  if (row.ocrTasks.some((task) => task.requires_human_review) || summaryBoolean(row, "latest_ocr_requires_human_review")) {
    issues.push({ code: "needs_manual_handling", message: "识别结果需要人工处理" });
  }
  if (row.segments.some((segment) => segment.status === "needs_manual_review" || segment.status === "rejected") || summaryNumber(row, "manual_segment_count") > 0) {
    issues.push({ code: "needs_manual_handling", message: "切分结果需要人工处理" });
  }
  return issues;
}

function matchesQualityFilter(row: SubmissionView, filter: QualityFilter) {
  if (filter === "all") {
    return true;
  }
  const issues = derivedIssues(row);
  if (filter === "missing_page") {
    return issues.some((issue) => issue.code === "missing_page" || issue.code === "page_count_mismatch" || issue.code === "no_pages");
  }
  if (filter === "ocr_failed") {
    return issues.some((issue) => issue.code === "ocr_failed") || row.ocrTasks.some((task) => task.status === "failed");
  }
  if (filter === "needs_manual_handling") {
    return issues.some((issue) => issue.code === "needs_manual_handling");
  }
  return issues.some((issue) => issue.code === filter);
}

function issueTone(code: string): StatusTone {
  if (code === "missing_page" || code === "page_count_mismatch" || code === "no_pages" || code === "ocr_failed") {
    return "danger";
  }
  if (code === "needs_manual_handling" || code === "blurry" || code === "duplicate_page") {
    return "warning";
  }
  return "neutral";
}

export function SubmissionCapturePage({
  canManage,
  canReadStudentNames,
  initialExamId = ""
}: {
  canManage: boolean;
  canReadStudentNames: boolean;
  initialExamId?: string;
}) {
  const { message } = App.useApp();
  const canWrite = canManage;
  const [filters, setFilters] = useState<Filters>(() => {
    const issue = hashQueryParam("issue");
    return { search: "", quality: "all", issue: ["failed", "unmatched", "quality"].includes(issue) ? issue as Filters["issue"] : "all" };
  });
  const [expectedPages, setExpectedPages] = useState(1);
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState(initialExamId);
  const [rows, setRows] = useState<SubmissionView[]>([]);
  const [students, setStudents] = useState<Student[]>([]);
  const [studentLookupError, setStudentLookupError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [captureLoading, setCaptureLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextSubmissionCursor, setNextSubmissionCursor] = useState("");
  const [hasMoreSubmissions, setHasMoreSubmissions] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [captureError, setCaptureError] = useState<string | null>(null);
  const [uploadQueue, setUploadQueue] = useState<UploadQueueItem[]>([]);
  const [actioning, setActioning] = useState<string | null>(null);
  const [pageDrawer, setPageDrawer] = useState<SubmissionView | null>(null);
  const [preview, setPreview] = useState<PreviewState | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [ocrDrawer, setOcrDrawer] = useState<{ row: SubmissionView; tasks: OcrTask[]; loading: boolean; loadingMore?: boolean; error?: string } | null>(null);
  const captureRequestRef = useRef(new LatestRequestGate());
  const previewRequestRef = useRef(new LatestRequestGate());
  const commandGateRef = useRef(new ExclusiveCommandGate());

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const studentById = useMemo(() => new Map(students.map((student) => [student.id, student])), [students]);

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

  const loadCaptureData = useCallback(
    async (examId: string) => {
      const requestId = captureRequestRef.current.begin();
      if (!examId) {
        setRows([]);
        setNextSubmissionCursor("");
        setHasMoreSubmissions(false);
        setCaptureLoading(false);
        return;
      }
      setCaptureLoading(true);
      setCaptureError(null);
      setStudentLookupError(null);
      try {
        const result = await loadCapturePage(examId, canReadStudentNames);
        if (!captureRequestRef.current.isCurrent(requestId)) return;
        setStudents(result.students);
        setStudentLookupError(result.studentLookupError ? formatError(result.studentLookupError) : null);
        setRows(result.rows);
        setNextSubmissionCursor(result.nextCursor);
        setHasMoreSubmissions(result.hasMore);
      } catch (currentError) {
        if (!captureRequestRef.current.isCurrent(requestId)) return;
        setCaptureError(formatError(currentError));
        setNextSubmissionCursor("");
        setHasMoreSubmissions(false);
      } finally {
        if (captureRequestRef.current.isCurrent(requestId)) setCaptureLoading(false);
      }
    },
    [canReadStudentNames]
  );

  const loadMoreSubmissions = useCallback(async () => {
    if (!selectedExamId || !hasMoreSubmissions || !nextSubmissionCursor || loadingMore) return;
    const requestId = captureRequestRef.current.begin();
    setLoadingMore(true);
    try {
      const result = await loadNextCapturePage(selectedExamId, nextSubmissionCursor, canReadStudentNames);
      if (!captureRequestRef.current.isCurrent(requestId)) return;
      setStudents((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        result.students.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setRows((current) => {
        const byID = new Map(current.map((item) => [item.submission.id, item]));
        result.rows.forEach((row) => byID.set(row.submission.id, row));
        return Array.from(byID.values());
      });
      setNextSubmissionCursor(result.nextCursor);
      setHasMoreSubmissions(result.hasMore);
    } catch (currentError) {
      if (captureRequestRef.current.isCurrent(requestId)) message.error(formatError(currentError));
    } finally {
      if (captureRequestRef.current.isCurrent(requestId)) setLoadingMore(false);
    }
  }, [canReadStudentNames, hasMoreSubmissions, loadingMore, message, nextSubmissionCursor, selectedExamId]);

  useEffect(() => {
    void loadExams();
  }, [loadExams]);

  useEffect(() => {
    if (initialExamId) setSelectedExamId(initialExamId);
  }, [initialExamId]);

  useEffect(() => {
    void loadCaptureData(selectedExamId);
  }, [loadCaptureData, selectedExamId]);

  useEffect(() => {
    return () => {
      if (preview?.url) {
        URL.revokeObjectURL(preview.url);
      }
    };
  }, [preview?.url]);

  const refreshSingle = async (submissionId: string) => {
    const detail = await getSubmission(submissionId);
    const view = await buildSubmissionView(detail.submission, formatError);
    setRows((current) => current.map((item) => (item.submission.id === submissionId ? view : item)));
    setPageDrawer((current) => (current?.submission.id === submissionId ? view : current));
    setOcrDrawer((current) => (current?.row.submission.id === submissionId ? { ...current, row: view } : current));
  };

  const patchUpload = (id: string, patch: Partial<UploadQueueItem>) => {
    setUploadQueue((current) => current.map((item) => (item.id === id ? { ...item, ...patch } : item)));
  };

  const uploadAnswerFile = async (request: UploadRequest) => {
    const file = request.file as File & { uid?: string };
    const uploadId = file.uid ?? `${Date.now()}-${file.name}`;
    let submissionId = "";
    let pageUploaded = false;
    setUploadQueue((current) => [
      { id: uploadId, fileName: file.name, status: "processing", phase: "创建答题卡记录", percent: 2 },
      ...current
    ]);
    if (!selectedExam) {
      const errorMessage = "请先选择考试";
      patchUpload(uploadId, { status: "error", phase: "失败", error: errorMessage, percent: 0 });
      request.onError?.(new Error(errorMessage));
      return;
    }
    try {
      const submissionResult = await createSubmission(selectedExam.id, {
        candidate_no: compactFileName(file.name),
        source_type: sourceTypeFor(file),
        expected_page_count: expectedPages
      });
      submissionId = submissionResult.submission.id;
      patchUpload(uploadId, { phase: "上传文件", percent: 8 });
      const uploadResult = await uploadFileWithProgress(
        file,
        {
          owner_type: "submission",
          owner_id: submissionId,
          school_id: selectedExam.school_id,
          exam_id: selectedExam.id,
          submission_id: submissionId
        },
        (percent) => patchUpload(uploadId, { percent: Math.min(92, Math.max(8, percent)) })
      );
      patchUpload(uploadId, { phase: "关联第 1 页", percent: 94 });
      await addSubmissionPage(submissionId, uploadResult.file.id, 1);
      pageUploaded = true;
      patchUpload(uploadId, { phase: "检查答卷完整性", percent: 96 });
      const integrity = await runQualityCheck(submissionId);
      if (integrity.result.valid) {
        patchUpload(uploadId, { phase: "提交图像质量检测", percent: 98 });
        await runImageQualityCheck(submissionId);
        patchUpload(uploadId, { status: "success", phase: "图像质检已进入队列", percent: 100 });
      } else {
        patchUpload(uploadId, { status: "success", phase: "已上传，答卷完整性需处理", percent: 100 });
      }
      request.onSuccess?.({ ok: true });
      await loadCaptureData(selectedExam.id);
    } catch (currentError) {
      const errorMessage = formatError(currentError);
      if (pageUploaded) {
        patchUpload(uploadId, { status: "success", phase: "答题卡已上传，自动处理未启动", error: errorMessage, percent: 100 });
        request.onSuccess?.({ ok: true });
        if (selectedExam) await loadCaptureData(selectedExam.id);
      } else {
        patchUpload(uploadId, { status: "error", phase: "上传失败", error: errorMessage });
        request.onError?.(new Error(getUserErrorMessage(currentError, errorMessage)));
      }
    }
  };

  const uploadProps: UploadProps = {
    multiple: true,
    showUploadList: false,
    accept: ".pdf,.png,.jpg,.jpeg,.tif,.tiff",
    customRequest: (request) => {
      void uploadAnswerFile(request);
    }
  };

  const filteredRows = useMemo(() => {
    const keyword = filters.search.trim().toLowerCase();
    return rows.filter((row) => {
      const student = row.submission.student_id ? studentById.get(row.submission.student_id) : undefined;
      const keywordMatched =
        !keyword ||
        row.submission.id.toLowerCase().includes(keyword) ||
        (row.submission.candidate_no ?? "").toLowerCase().includes(keyword) ||
        (student?.name ?? "").toLowerCase().includes(keyword) ||
        (student?.student_no ?? "").toLowerCase().includes(keyword);
      const issueMatched =
        filters.issue === "all"
        || (filters.issue === "failed" && processingState(row) === "failed")
        || (filters.issue === "unmatched" && !row.submission.student_id)
        || (filters.issue === "quality" && derivedIssues(row).length > 0);
      return keywordMatched && issueMatched && matchesQualityFilter(row, filters.quality);
    });
  }, [filters.issue, filters.quality, filters.search, rows, studentById]);

  const uploadPercent = useMemo(() => {
    if (uploadQueue.length === 0) {
      return 0;
    }
    return Math.round(uploadQueue.reduce((sum, item) => sum + item.percent, 0) / uploadQueue.length);
  }, [uploadQueue]);

  const summary = useMemo(() => {
    const states = rows.map(processingState);
    return {
      total: rows.length,
      pending: states.filter((state) => state === "pending").length,
      processing: states.filter((state) => state === "processing").length,
      completed: states.filter((state) => state === "completed").length,
      failed: states.filter((state) => state === "failed").length
    };
  }, [rows]);

  const studentName = (submission: Submission) => {
    if (!submission.student_id) {
      return "未关联学生";
    }
    if (!canReadStudentNames) {
      return "无姓名权限";
    }
    return studentById.get(submission.student_id)?.name ?? "已关联（姓名未加载）";
  };

  const runAction = async (key: string, action: () => Promise<void>, successText: string, reload?: () => Promise<void>) => {
    try {
      const result = await runCaptureCommand({
        key, gate: commandGateRef.current, command: action, reload,
        onStart: () => setActioning(key), onSettled: () => setActioning((current) => current === key ? null : current)
      });
      if (result === "completed") message.success(successText);
    } catch (currentError) {
      message.error(formatError(currentError));
    }
  };

  const openPages = async (row: SubmissionView) => {
    previewRequestRef.current.invalidate();
    setPageDrawer(row);
    setPreview(null);
    if (row.pages.length === 0 && row.submission.actual_page_count > 0) {
      const detailed = await buildSubmissionView(row.submission, formatError);
      setRows((current) => current.map((item) => item.submission.id === detailed.submission.id ? detailed : item));
      setPageDrawer(detailed);
    }
  };

  const previewPage = async (page: SubmissionPage) => {
    const requestId = previewRequestRef.current.begin();
    setPreviewLoading(true);
    try {
      const file = await downloadFileBlob(page.file_asset_id);
      if (!previewRequestRef.current.isCurrent(requestId)) return;
      setPreview((current) => {
        if (current?.url) {
          URL.revokeObjectURL(current.url);
        }
        return { url: URL.createObjectURL(file.blob), contentType: file.contentType, filename: file.filename };
      });
    } catch (currentError) {
      if (previewRequestRef.current.isCurrent(requestId)) message.error(formatError(currentError));
    } finally {
      if (previewRequestRef.current.isCurrent(requestId)) setPreviewLoading(false);
    }
  };

  const replacePage = async (page: SubmissionPage, file: File) => {
    if (!pageDrawer || !selectedExam) {
      message.error("请先选择答题卡");
      return;
    }
    await runAction(
      `replace-${page.id}`,
      async () => {
        const upload = await uploadFile(file, {
          owner_type: "submission",
          owner_id: pageDrawer.submission.id,
          school_id: selectedExam.school_id,
          exam_id: selectedExam.id,
          submission_id: pageDrawer.submission.id
        });
        await replaceSubmissionPage(pageDrawer.submission.id, page.page_no, upload.file.id);
      },
      "页面已替换，系统将重新检查图片质量",
      () => refreshSingle(pageDrawer.submission.id)
    );
  };

  const openOcrDrawer = async (row: SubmissionView) => {
    setOcrDrawer({ row, tasks: [], loading: true });
    try {
      const detailed = row.ocrTasks.length > 0 ? row : await buildSubmissionView(row.submission, formatError);
      setRows((current) => current.map((item) => item.submission.id === detailed.submission.id ? detailed : item));
      setOcrDrawer({ row: detailed, tasks: detailed.ocrTasks, loading: false });
    } catch (currentError) {
      setOcrDrawer({ row, tasks: [], loading: false, error: formatError(currentError) });
    }
  };

  const loadMoreOcrTasks = async () => {
    const current = ocrDrawer;
    if (!current || current.loading || current.loadingMore || !current.row.ocrHasMore || !current.row.ocrNextCursor) return;
    setOcrDrawer({ ...current, loadingMore: true });
    try {
      const result = await listOcrTasks(current.row.submission.id, { limit: 20, cursor: current.row.ocrNextCursor });
      const byId = new Map(current.tasks.map((task) => [task.id, task]));
      for (const task of result.tasks) byId.set(task.id, task);
      const tasks = [...byId.values()];
      const row = { ...current.row, ocrTasks: tasks, ocrNextCursor: result.next_cursor, ocrHasMore: result.has_more };
      setRows((rows) => rows.map((item) => item.submission.id === row.submission.id ? row : item));
      setOcrDrawer((latest) => {
        if (!latest || latest.row.submission.id !== current.row.submission.id) return latest;
        return { ...latest, row, tasks, loadingMore: false };
      });
    } catch (currentError) {
      message.error(formatError(currentError));
      setOcrDrawer((latest) => latest ? { ...latest, loadingMore: false } : latest);
    }
  };

  const columns: TableColumnsType<SubmissionView> = [
    {
      title: (
        <Tooltip title="上传时取自文件名，关联学生后显示准考证号">
          <span>答题卡编号</span>
        </Tooltip>
      ),
      fixed: "left",
      width: 210,
      render: (_, row) => (
        <div className="capture-identity-cell">
          <strong>{row.submission.candidate_no || "暂未生成"}</strong>
        </div>
      )
    },
    { title: "学生姓名", width: 130, render: (_, row) => studentName(row.submission) },
    {
      title: "页数",
      width: 92,
      render: (_, row) => `${row.pages.length || row.submission.actual_page_count}/${row.submission.expected_page_count || "未设"}`
    },
    {
      title: "当前状态",
      width: 190,
      render: (_, row) => {
        const task = latestTask(row.ocrTasks);
        const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
        const state = processingState(row);
        if (state === "failed") return <StatusTag tone="danger">{taskStatus === "failed" ? "文字识别失败" : "图片质量未通过"}</StatusTag>;
        if (state === "completed") return <StatusTag tone="success">处理完成</StatusTag>;
        if (state === "processing") return <StatusTag tone="processing">{taskStatus === "processing" ? "正在识别" : "识别排队中"}</StatusTag>;
        if (taskStatus === "completed") return <StatusTag tone="warning">等待生成题目区域</StatusTag>;
        return <StatusTag tone={statusTone(row.submission.status)}>等待自动处理</StatusTag>;
      }
    },
    {
      title: "问题说明",
      width: 300,
      render: (_, row) => {
        const issues = derivedIssues(row);
        return issues.length > 0 ? (
          <Space wrap size={[0, 4]}>
            {issues.slice(0, 3).map((issue, index) => (
              <StatusTag key={`${issue.code}-${index}`} tone={issueTone(issue.code)}>
                {getSafeUserText(issue.message, "处理检查未通过")}
              </StatusTag>
            ))}
            {issues.length > 3 ? <StatusTag tone="neutral">{`+${issues.length - 3}`}</StatusTag> : null}
          </Space>
        ) : (
          <span className="muted-text">暂无问题</span>
        );
      }
    },
    {
      title: "下一步",
      fixed: "right",
      width: 220,
      render: (_, row) => {
        const id = row.submission.id;
        const task = latestTask(row.ocrTasks);
        const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
        const segmentCount = row.segments.length || summaryNumber(row, "segment_count");
        const qualityPending = row.submission.quality_status !== "passed";
        const needsReady = !qualityPending && row.submission.status !== "ready_for_ocr";
        const ocrFailed = taskStatus === "failed";
        const needsOcr = !qualityPending && !needsReady && (!taskStatus || ocrFailed);
        const needsSegments = taskStatus === "completed" && segmentCount === 0;

        const runNext = () => {
          if (qualityPending) {
            return runAction(`quality-${id}`, async () => {
              const integrity = await runQualityCheck(id);
              if (!integrity.result.valid) {
                throw new Error("答卷完整性未通过，请先补齐或更正页面");
              }
              await runImageQualityCheck(id);
            }, "图像质量检测已进入队列", () => refreshSingle(id));
          }
          if (needsReady) {
            return runAction(`ready-${id}`, async () => {
              await updateSubmissionStatus(id, "ready_for_ocr", row.submission.revision);
            }, "答题卡已转入待识别，可点击『开始识别』继续", () => refreshSingle(id));
          }
          if (needsOcr) {
            return runAction(`ocr-${id}`, async () => {
              await createOcrTask(id);
            }, "文字识别已开始", () => refreshSingle(id));
          }
          if (needsSegments) {
            return runAction(`segment-${id}`, async () => {
              await generateAnswerSegments(id);
            }, "题目区域已生成", () => refreshSingle(id));
          }
          void openPages(row);
          return Promise.resolve();
        };

        const primaryLabel = qualityPending
          ? "检查图片质量"
          : needsReady
            ? "转入待识别"
            : needsOcr
              ? ocrFailed ? "重试识别" : "开始识别"
              : needsSegments
                ? "生成题目区域"
                : "查看答题卡";
        return (
          <Space className="table-actions" size={6}>
            <Button
              type="primary"
              disabled={!canWrite && primaryLabel !== "查看答题卡"}
              loading={Boolean(actioning?.endsWith(id))}
              onClick={() => void runNext()}
            >
              {primaryLabel}
            </Button>
            <Dropdown
              menu={{
                items: [
                  { key: "pages", label: "查看原始页面", icon: <ImageIcon size={14} />, onClick: () => openPages(row) },
                  { key: "ocr", label: "查看识别详情", icon: <FileSearch size={14} />, onClick: () => void openOcrDrawer(row) }
                ]
              }}
              trigger={["click"]}
            >
              <Button aria-label="更多操作" icon={<MoreHorizontal size={16} />} />
            </Dropdown>
          </Space>
        );
      }
    }
  ];

  const pageColumns: TableColumnsType<SubmissionPage> = [
    { title: "页码", dataIndex: "page_no", width: 72 },
    { title: "状态", dataIndex: "status", width: 110, render: (value: string) => <StatusTag tone={pageStatusTone(value)}>{pageStatusLabels[value] ?? "未知状态"}</StatusTag> },
    {
      title: "质量问题",
      width: 180,
      render: (_, page) =>
        page.quality_issues.length > 0 ? (
          <Space wrap>
            {page.quality_issues.map((issue, index) => (
              <StatusTag key={`${issue.code}-${index}`} tone={issueTone(issue.code)}>
                {getSafeUserText(issue.message, "图片质量检查未通过")}
              </StatusTag>
            ))}
          </Space>
        ) : (
          <StatusTag tone="success">无</StatusTag>
        )
    },
    {
      title: "操作",
      width: 190,
      render: (_, page) => (
        <Space>
          <Button size="small" icon={<Eye size={14} />} loading={previewLoading} onClick={() => void previewPage(page)}>
            查看
          </Button>
          <Upload
            showUploadList={false}
            beforeUpload={(file) => {
              void replacePage(page, file);
              return false;
            }}
          >
            <Button size="small" icon={<RotateCcw size={14} />} disabled={!canWrite || Boolean(actioning)}>
              重传
            </Button>
          </Upload>
        </Space>
      )
    }
  ];

  const ocrTaskColumns: TableColumnsType<OcrTask> = [
    {
      title: "任务",
      dataIndex: "id",
      width: 130,
      render: (value: string, _task, index) => (
        <Tooltip title={value}>
          <span>识别任务 {index + 1}</span>
        </Tooltip>
      )
    },
    { title: "状态", width: 110, render: (_, task) => <StatusTag tone={ocrTone(task)}>{ocrLabel([task])}</StatusTag> },
    { title: "结果数", dataIndex: "result_count", width: 90 },
    { title: "创建时间", dataIndex: "created_at", width: 170, render: formatTime }
  ];

  return (
    <div className="page-stack">
      <PageHeader
        title="答题卡导入"
        description="选择考试并上传扫描件，系统自动完成图片检查和文字识别；失败记录可在下方直接重试。"
        actions={<Space wrap>
          <Tooltip title="上传时用于校验答题卡页数是否齐全">
            <label className="inline-number-field capture-advanced-control">
              <span>每份答题卡页数</span>
              <InputNumber min={1} max={200} precision={0} value={expectedPages} onChange={(value) => setExpectedPages(Number(value ?? 1))} />
            </label>
          </Tooltip>
          <Button icon={<RefreshCw size={16} />} onClick={() => void loadCaptureData(selectedExamId)} loading={captureLoading}>
            刷新
          </Button>
          <Upload {...uploadProps}>
            <Button type="primary" icon={<FileUp size={16} />} disabled={!canWrite || !selectedExam}>
              上传答题卡
            </Button>
          </Upload>
        </Space>}
      />

      <OcrWorkerAlert enabled={canManage} />

      {selectedExamId ? <ProcessingOperationsPanel examId={selectedExamId} canManage={canManage} /> : null}

      {studentLookupError ? (
        <Alert type="info" showIcon message="学生姓名未完全加载" description="部分学生姓名暂时无法加载，不影响答题卡导入，稍后刷新重试即可。" />
      ) : null}

      <FilterBar>
        <div className="capture-toolbar">
          <Select
            className="exam-picker"
            placeholder="选择考试"
            value={selectedExamId || undefined}
            options={exams.map((exam) => ({ label: exam.name, value: exam.id }))}
            onChange={setSelectedExamId}
            loading={loading}
          />
          <Input
            className="toolbar-input"
            prefix={<Search size={16} />}
            placeholder="搜索学生或准考证号"
            value={filters.search}
            onChange={(event) => setFilters((current) => ({ ...current, search: event.target.value }))}
          />
          <Select
            className="toolbar-select"
            value={filters.quality}
            options={qualityFilterOptions}
            onChange={(value) => setFilters((current) => ({ ...current, quality: value, issue: "all" }))}
          />
        </div>
      </FilterBar>

      {uploadQueue.length > 0 ? (
        <section className="workspace-section upload-progress-panel">
          <div className="section-head">
            <div>
              <h2>上传队列</h2>
              <p>{uploadQueue.filter((item) => item.status === "error").length} 个失败</p>
            </div>
            <Progress className="batch-progress" percent={uploadPercent} size="small" />
          </div>
          <List
            className="upload-queue-list"
            dataSource={uploadQueue.slice(0, 8)}
            renderItem={(item) => (
              <List.Item className="upload-queue-item">
                <div>
                  <strong>{item.fileName}</strong>
                  <span>{item.error || item.phase}</span>
                </div>
                <Progress percent={item.percent} size="small" status={item.status === "error" ? "exception" : item.status === "success" ? "success" : "active"} />
              </List.Item>
            )}
          />
        </section>
      ) : null}

      <section className="capture-summary-grid">
        <div className="metric-tile">
          <span>答题卡总数</span>
          <strong>{summary.total}</strong>
          <small>当前考试</small>
        </div>
        <div className="metric-tile">
          <span>待处理</span>
          <strong>{summary.pending}</strong>
          <small>等待检查或切题</small>
        </div>
        <div className="metric-tile">
          <span>处理中</span>
          <strong>{summary.processing}</strong>
          <small>识别排队或执行中</small>
        </div>
        <div className="metric-tile">
          <span>已完成</span>
          <strong>{summary.completed}</strong>
          <small>已识别并生成题目区域</small>
        </div>
        <div className="metric-tile">
          <span>失败</span>
          <strong>{summary.failed}</strong>
          <small>可在列表直接重试</small>
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
          <EmptyState title="暂无考试" description="暂无可采集的考试。请先在『考试管理』创建考试并完成开考准备。" />
        </section>
      ) : captureLoading ? (
        <section className="workspace-section">
          <LoadingState label="正在读取答题卡记录" />
        </section>
      ) : captureError ? (
        <ErrorState message={captureError} onRetry={() => void loadCaptureData(selectedExam.id)} />
      ) : (
        <DataTableShell
          title="答题卡处理"
          description={`${filteredRows.length} / ${rows.length} 条记录，已完成 ${summary.completed} 份${summary.failed > 0 ? `；${summary.failed} 份处理失败，可直接重试` : ""}`}
        >
          <ResponsiveTable<SubmissionView>
            className="dense-data-table"
            rowKey={(row) => row.submission.id}
            dataSource={filteredRows}
            columns={columns}
            pagination={{ pageSize: 10, showSizeChanger: false }}
            size="small"
            rowClassName={(row) => (row.detailError ? "row-with-warning" : "")}
            locale={{ emptyText: <EmptyState title="暂无答题卡" description="当前考试还没有答题卡。点击右上角『上传答题卡』导入扫描件。" /> }}
          />
          {hasMoreSubmissions ? (
            <Button block loading={loadingMore} onClick={() => void loadMoreSubmissions()}>
              加载更多答题卡
            </Button>
          ) : null}
        </DataTableShell>
      )}

      <Drawer title="答题卡页面" open={Boolean(pageDrawer)} width={860} onClose={() => {
        previewRequestRef.current.invalidate();
        setPreviewLoading(false);
        setPageDrawer(null);
        setPreview(null);
      }}>
        {pageDrawer ? (
          <div className="detail-stack">
            <Descriptions bordered size="small" column={2}>
              <Descriptions.Item label="答题卡编号">{pageDrawer.submission.candidate_no || "暂未生成"}</Descriptions.Item>
              <Descriptions.Item label="学生姓名">{studentName(pageDrawer.submission)}</Descriptions.Item>
              <Descriptions.Item label="采集状态">{submissionStatusLabels[pageDrawer.submission.status] ?? "未知状态"}</Descriptions.Item>
              <Descriptions.Item label="质量状态">{qualityStatusLabels[pageDrawer.submission.quality_status] ?? pageDrawer.submission.quality_status}</Descriptions.Item>
            </Descriptions>
            <ResponsiveTable<SubmissionPage>
              className="dense-data-table"
              rowKey="id"
              dataSource={pageDrawer.pages}
              columns={pageColumns}
              pagination={false}
              size="small"
              locale={{ emptyText: <EmptyState title="暂无页面" description="该答题卡还没有关联页面文件。" /> }}
            />
            {preview ? (
              <div className="page-preview">
                <div className="section-head">
                  <div>
                    <h2>{preview.filename ?? "页面预览"}</h2>
                    <p title={preview.contentType}>
                      {preview.contentType === "application/pdf"
                        ? "PDF 文档"
                        : preview.contentType.startsWith("image/")
                          ? "扫描图片"
                          : "其他文件"}
                    </p>
                  </div>
                  <Button icon={<Eye size={16} />} onClick={() => window.open(preview.url, "_blank", "noopener,noreferrer")}>
                    新窗口打开
                  </Button>
                </div>
                {preview.contentType.startsWith("image/") ? (
                  <img src={preview.url} alt={preview.filename ?? "答题卡页面"} />
                ) : preview.contentType === "application/pdf" ? (
                  <iframe title={preview.filename ?? "答题卡 PDF"} src={preview.url} />
                ) : (
                  <Alert type="info" showIcon message="该文件类型不支持内嵌预览" description="可使用新窗口打开或浏览器下载查看。" />
                )}
              </div>
            ) : null}
          </div>
        ) : null}
      </Drawer>

      <Drawer title="文字识别详情" open={Boolean(ocrDrawer)} width={920} onClose={() => setOcrDrawer(null)}>
        {ocrDrawer?.loading ? (
          <LoadingState label="正在读取识别详情" />
        ) : ocrDrawer?.error ? (
          <ErrorState message={ocrDrawer.error} />
        ) : ocrDrawer ? (
          <div className="detail-stack">
            <Descriptions bordered size="small" column={2}>
              <Descriptions.Item label="答题卡编号">{ocrDrawer.row.submission.candidate_no || "暂未生成"}</Descriptions.Item>
              <Descriptions.Item label="识别状态">{ocrLabel(ocrDrawer.row.ocrTasks)}</Descriptions.Item>
            </Descriptions>
            <ResponsiveTable<OcrTask>
              className="dense-data-table"
              rowKey="id"
              dataSource={ocrDrawer.tasks}
              columns={ocrTaskColumns}
              pagination={false}
              size="small"
              locale={{ emptyText: <EmptyState title="暂无识别任务" description="该答题卡还没有文字识别任务。" /> }}
            />
            {ocrDrawer.row.ocrHasMore ? (
              <div className="load-more-row">
                <Button loading={ocrDrawer.loadingMore} onClick={() => void loadMoreOcrTasks()}>加载更早的识别任务</Button>
              </div>
            ) : null}
            {ocrDrawer.tasks.flatMap((task) => task.results ?? []).length > 0 ? (
              <div className="ocr-result-list">
                {ocrDrawer.tasks.map((task, index) => (
                  <section className="ocr-result-block" key={task.id}>
                    <div className="section-head">
                      <div>
                        <h2 title={task.id}>识别任务 {index + 1}</h2>
                        <p>创建于 {formatTime(task.created_at)} · {task.results?.length ?? 0} 条识别结果</p>
                      </div>
                    </div>
                    <ResponsiveTable
                      className="dense-data-table"
                      rowKey="id"
                      dataSource={task.results ?? []}
                      pagination={false}
                      size="small"
                      columns={[
                        {
                          title: "页面",
                          dataIndex: "submission_page_id",
                          width: 100,
                          render: (value: string) => {
                            const pageNo = ocrDrawer.row.pages.find((page) => page.id === value)?.page_no;
                            return <span title={value}>{pageNo ? `第 ${pageNo} 页` : "-"}</span>;
                          }
                        },
                        { title: "文本", dataIndex: "text" },
                        { title: "置信度", dataIndex: "confidence", width: 100, render: (value: number) => `${Math.round(value * 100)}%` }
                      ]}
                    />
                  </section>
                ))}
              </div>
            ) : (
              <EmptyState title="暂无识别结果" description="识别任务已创建，系统处理完成后结果将显示在这里。" />
            )}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
