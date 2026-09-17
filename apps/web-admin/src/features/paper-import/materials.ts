import type { PaperImportDraftQuestion, PaperImportIssue, PaperImportJob, PaperImportRole, PaperImportSource } from "../../api/papers";

const supportedExtensions = [".pdf", ".docx", ".png", ".jpg", ".jpeg", ".tif", ".tiff"];
const supportedMimeTypes = new Set([
  "application/pdf",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "image/png",
  "image/jpeg",
  "image/tiff"
]);

export function isSupportedPaperImportFile(file: File) {
  const name = file.name.toLowerCase();
  return supportedMimeTypes.has(file.type.toLowerCase()) || supportedExtensions.some((extension) => name.endsWith(extension));
}

export function filesFromClipboard(data: Pick<DataTransfer, "files" | "items"> | null | undefined, now = new Date()) {
  if (!data) return [];
  const files: File[] = [];
  const seen = new Set<File>();
  const add = (file: File | null) => {
    if (file && !seen.has(file) && isSupportedPaperImportFile(file)) {
      const clipboardName = file.type.startsWith("image/") && (!file.name || file.name.toLowerCase() === "image.png")
        ? `clipboard-${clipboardTimestamp(now)}-${String(files.length + 1).padStart(2, "0")}.${imageExtension(file.type)}`
        : file.name;
      files.push(clipboardName === file.name ? file : new File([file], clipboardName, { type: file.type, lastModified: file.lastModified }));
      seen.add(file);
    }
  };
  const listedFiles = Array.from(data.files ?? []);
  if (listedFiles.length > 0) {
    for (const file of listedFiles) add(file);
  } else {
    for (const item of Array.from(data.items ?? [])) {
      if (item.kind === "file") add(item.getAsFile());
    }
  }
  return files;
}

export function isTextPasteTarget(target: EventTarget | null) {
  return typeof Element !== "undefined" && target instanceof Element && Boolean(target.closest("input, textarea, [contenteditable]:not([contenteditable='false']), [role='textbox']"));
}

function clipboardTimestamp(value: Date) {
  const part = (number: number) => String(number).padStart(2, "0");
  return `${value.getFullYear()}${part(value.getMonth() + 1)}${part(value.getDate())}-${part(value.getHours())}${part(value.getMinutes())}${part(value.getSeconds())}`;
}

function imageExtension(mime: string) {
  if (mime === "image/jpeg") return "jpg";
  if (mime === "image/tiff") return "tiff";
  return "png";
}

export function orderedSourcesAfterMove(sources: PaperImportSource[], sourceID: string, direction: -1 | 1) {
  const ordered = [...sources].sort((left, right) => left.document_index - right.document_index);
  const index = ordered.findIndex((source) => source.id === sourceID);
  const target = index + direction;
  if (index < 0 || target < 0 || target >= ordered.length) return ordered;
  [ordered[index], ordered[target]] = [ordered[target], ordered[index]];
  return ordered.map((source, documentIndex) => ({ ...source, document_index: documentIndex }));
}

export function orderedSourcesAfterRemoval(sources: PaperImportSource[], sourceID: string) {
  return sources
    .filter((source) => source.id !== sourceID)
    .sort((left, right) => left.document_index - right.document_index)
    .map((source, documentIndex) => ({ ...source, document_index: documentIndex }));
}

export function sourcesAfterRoleChange(sources: PaperImportSource[], sourceID: string, roleHint: PaperImportRole) {
  return sources.map((source) => (source.id === sourceID ? { ...source, role_hint: roleHint } : source));
}

export function paperImportSummary(job: PaperImportJob) {
  return {
    questions: job.question_candidates?.length ?? job.questions.length,
    answers: job.answer_candidates?.length ?? 0,
    solutions: job.solution_candidates?.length ?? 0,
    reviewIssues: paperImportReviewIssues(job).length
  };
}

const configurationMismatchCodes = new Set(["QUESTION_COUNT_MISMATCH", "SECTION_COUNT_MISMATCH", "SCORE_TOTAL_MISMATCH"]);

/**
 * Keep the review panel focused on distinct actions instead of repeating one
 * warning for every question. The full issue list remains stored on the job.
 */
export function paperImportReviewIssues(job: Pick<PaperImportJob, "structured_issues">): PaperImportIssue[] {
  const issues = (job.structured_issues ?? []).filter((issue) => issue.severity !== "info");
  const visible = issues.filter((issue) => issue.code !== "HUMAN_REVIEW_REQUIRED" && issue.code !== "MISSING_SCORE" && !configurationMismatchCodes.has(issue.code));
  const missingScores = issues.filter((issue) => issue.code === "MISSING_SCORE");
  const questionCountMismatch = issues.find((issue) => issue.code === "QUESTION_COUNT_MISMATCH");
  const configurationMismatch = questionCountMismatch ?? issues.find((issue) => configurationMismatchCodes.has(issue.code));

  if (missingScores.length) {
    visible.push({
      ...missingScores[0],
      question_no: undefined,
      message: `${missingScores.length} 道题尚未填写分值`,
      source_refs: []
    });
  }
  if (configurationMismatch) {
    visible.push({
      ...configurationMismatch,
      code: "EXAM_CONFIGURATION_MISMATCH",
      question_no: undefined,
      message: questionCountMismatch?.message ?? "识别题量、题型或总分与考试配置不一致",
      source_refs: []
    });
  }
  return visible;
}

export function hasNoExamContentDetected(job: PaperImportJob) {
  return (job.structured_issues ?? []).some((issue) => issue.code === "NO_EXAM_CONTENT_DETECTED");
}

export function isPaperImportCancelled(job: PaperImportJob) {
  return job.status === "cancelled" || job.error_code === "paper_import_cancelled";
}

export function paperImportProgress(job: PaperImportJob): { percent: number | undefined; label: string; detail: string; counter?: string; startedAt?: string; changedAt?: string; updatedAt?: string } {
  if (job.status === "applied") return { percent: 100, label: "导入完成", detail: "题目与评分资料已写入考试" };
	if (isPaperImportCancelled(job)) return { percent: undefined, label: "已停止识别", detail: "识别任务已手动停止，可重新识别" };
  if (job.status === "review_required" && hasNoExamContentDetected(job)) return { percent: 100, label: "未识别到考试内容", detail: "请检查是否上传了无关图片或错误文件" };
  if (job.status === "review_required") return { percent: 100, label: "等待人工核对", detail: "自动识别完成，请核对识别结果" };
  if (job.status === "failed") return { percent: undefined, label: "识别失败", detail: job.issues[0] ?? "请检查资料后重试" };

	const runtime = job.runtime_progress;
	const stage = runtime?.stage || runtime?.task_type;
	const labels: Record<string, string> = {
		layout: "页面预处理",
		page_decode: "页面预处理",
		ocr: "文字识别",
		text_ocr: "文字识别",
		paper_formula: "数学公式识别",
		formula_detection: "公式区域检测",
		formula_recognition: "数学公式识别",
		paper_parse: "AI 内容解析"
	};
	if (runtime) {
		const completed = Math.max(0, runtime.completed ?? 0);
		const total = Math.max(0, runtime.total ?? 0);
		const calculating = runtime.phase === "model_loading";
		const percent = !calculating && total > 0 ? Math.min(100, Math.floor((completed / total) * 100)) : undefined;
		const queued = runtime.task_status === "queued" || runtime.task_status === "leased";
		const parts = [queued ? "任务已进入处理队列" : runtime.message || "Worker 正在处理"];
		const unitLabels: Record<string, string> = { document: "份", page: "页", formula_region: "个 ROI", parse_chunk: "个解析块" };
		const counter = total > 0 ? `${completed}/${total} ${unitLabels[runtime.unit ?? ""] ?? runtime.unit ?? "项"}` : undefined;
		if (runtime.batch_no && runtime.batch_total) parts.push(`批次 ${runtime.batch_no}/${runtime.batch_total}`);
		if (runtime.model) parts.push(`模型 ${runtime.model}`);
		if (runtime.cold_start) parts.push("正在加载本地缓存，不会重新下载模型");
		if (runtime.runtime_plan_source === "compatibility_default") parts.push("未标定兼容模式");
		if (runtime.parse_route === "anchored" || runtime.parse_route === "unrelated_guard") parts.push("本阶段未调用大模型");
		if (runtime.parse_route === "compact_model") parts.push("仅歧义解析块调用大模型");
		return { percent, label: labels[stage ?? ""] ?? "考试资料识别", detail: parts.join(" · "), counter, startedAt: runtime.started_at, changedAt: runtime.progress_changed_at, updatedAt: runtime.updated_at };
	}
	if (!job.sources.length) return { percent: undefined, label: "准备资料", detail: "正在登记上传文件" };
	if (job.sources.some((source) => source.processing_status === "failed")) return { percent: undefined, label: "文字识别失败", detail: "请检查源文件后重试" };
	return { percent: undefined, label: "等待 Worker", detail: "任务已提交，等待 Worker 领取；暂无可计算的完成比例" };
}

export function hasBlockingImportIssues(job: PaperImportJob) {
  return job.questions.length === 0 || (job.structured_issues ?? []).some((issue) => issue.severity === "error");
}

export function markImportFieldConfirmed(draft: PaperImportDraftQuestion, field: string, patch: Partial<PaperImportDraftQuestion>) {
  return {
    ...draft,
    ...patch,
    human_confirmed_fields: Array.from(new Set([...(draft.human_confirmed_fields ?? []), field]))
  };
}
