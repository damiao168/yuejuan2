import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  App,
  Button,
  Drawer,
  Input,
  InputNumber,
  Select,
  Space,
  Tooltip,
  Upload,
  type UploadProps
} from "antd";
import {
  FileUp,
  RefreshCw,
  Search,
} from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import { runImageQualityCheck } from "../api/capture";
import { downloadFileBlob, uploadFile, uploadFileWithProgress } from "../api/files";
import { listExams, type Exam } from "../api/exams";
import type { Student } from "../api/org";
import {
  addSubmissionPage,
  createSubmission,
  getSubmission,
  listOcrTasks,
  replaceSubmissionPage,
  runQualityCheck,
  type Submission,
  type SubmissionPage
} from "../api/submissions";
import { OcrWorkerAlert } from "../components/OcrWorkerAlert";
import { ProcessingOperationsPanel } from "../components/ProcessingOperationsPanel";
import { FilterBar, PageHeader } from "@edugrade/ui";
import { hashQueryParam } from "../router/query";
import { ExclusiveCommandGate, LatestRequestGate, runCaptureCommand } from "../features/capture/captureWorkflow";
import { captureSubmissionColumns, capturePageColumns, captureOcrTaskColumns } from "../features/capture/submissionColumns";
import { SubmissionCaptureSummary } from "../features/capture/SubmissionCaptureSummary";
import {
  qualityFilterOptions,
  formatError,
  compactFileName,
  sourceTypeFor,
  processingState,
  derivedIssues,
  matchesQualityFilter,
  type Filters
} from "../features/capture/submissionPresentation";
import {
  buildSubmissionView,
  loadCapturePage,
  loadNextCapturePage,
  type SubmissionView
} from "../features/capture/captureQueries";
import {
  CaptureRecords,
  SubmissionOcrDetails,
  SubmissionPageDetails,
  UploadQueuePanel,
  type OcrDrawerState,
  type PreviewState,
  type UploadQueueItem,
} from "./SubmissionCaptureViews";

type UploadRequest = Parameters<NonNullable<UploadProps["customRequest"]>>[0];

export function SubmissionCapturePage({
  canManage,
  canManageProcessing = canManage,
  canReadStudentNames,
  initialExamId = "",
  onNavigate
}: Readonly<{
  canManage: boolean;
  canManageProcessing?: boolean;
  canReadStudentNames: boolean;
  initialExamId?: string;
  onNavigate?: (path: string) => void;
}>) {
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
  const [ocrDrawer, setOcrDrawer] = useState<OcrDrawerState | null>(null);
  // 请求序号和命令互斥属于页面实例，展示分支切换不能重置这些状态。
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
      // 页面关联成功后，即使后续质检或刷新失败，上传事实仍成立；不要再报“上传失败”诱发重复提交。
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
        if (latest?.row.submission.id !== current.row.submission.id) return latest;
        return { ...latest, row, tasks, loadingMore: false };
      });
    } catch (currentError) {
      message.error(formatError(currentError));
      setOcrDrawer((latest) => latest ? { ...latest, loadingMore: false } : latest);
    }
  };

  const columns = captureSubmissionColumns({ studentName, canWrite, actioning, runAction, refreshSingle, openPages, openOcrDrawer });
  const pageColumns = capturePageColumns({ previewLoading, previewPage, replacePage, canWrite, actioning });
  const ocrTaskColumns = captureOcrTaskColumns();

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

      {selectedExamId && canManageProcessing ? <ProcessingOperationsPanel key={selectedExamId} examId={selectedExamId} canManage={canManageProcessing} onNavigate={onNavigate} /> : null}

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

      <UploadQueuePanel queue={uploadQueue} percent={uploadPercent} />

      <SubmissionCaptureSummary summary={summary} />

      <CaptureRecords
        state={{ loading, error, exam: selectedExam, captureLoading, captureError }}
        table={{ rows, filteredRows, summary, columns, hasMoreSubmissions, loadingMore, loadMore: loadMoreSubmissions }}
        reloadExams={loadExams}
        reloadCapture={loadCaptureData}
      />

      <Drawer title="答题卡页面" open={Boolean(pageDrawer)} width={860} onClose={() => {
        previewRequestRef.current.invalidate();
        setPreviewLoading(false);
        setPageDrawer(null);
        setPreview(null);
      }}>
        {pageDrawer ? (
          <SubmissionPageDetails row={pageDrawer} preview={preview} studentName={studentName} columns={pageColumns} />
        ) : null}
      </Drawer>

      <Drawer title="文字识别详情" open={Boolean(ocrDrawer)} width={920} onClose={() => setOcrDrawer(null)}>
        <SubmissionOcrDetails drawer={ocrDrawer} columns={ocrTaskColumns} loadMore={loadMoreOcrTasks} />
      </Drawer>
    </div>
  );
}
