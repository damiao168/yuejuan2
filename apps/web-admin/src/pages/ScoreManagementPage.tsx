import { useMemo, useState, type ComponentProps } from "react";
import { Alert, Button, Empty, Input, Select, Space } from "antd";
import { RefreshCw, Search } from "lucide-react";
import {
  exportPublishedExamGrades
} from "../api/scores";
import { ErrorState, LoadingState } from "../components/PageState";
import { usePageLoading } from "../components/pageReadiness";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import type { ProductExperience } from "../router/experience";
import { routeQueryParam } from "../router/query";
import { ScorePublicationChecklist } from "../features/score-management/components/ScorePublicationChecklist";
import { publicationReadiness } from "../features/score-management/publicationChecklist";
import { QualityPanel } from "../features/score-management/components/QualityPanel";
import { ScoreCorrectionDialog } from "../features/score-management/components/ScoreCorrectionDialog";
import { ScorePublishDialog } from "../features/score-management/components/ScorePublishDialog";
import { ScoreReleaseHistory } from "../features/score-management/components/ScoreReleaseHistory";
import { ScoreReleaseStatus } from "../features/score-management/components/ScoreReleaseStatus";
import { StudentScorePreviewDialog } from "../features/score-management/components/StudentScorePreviewDialog";
import type { ScoreRelease } from "../api/scoreReleases";
import { useScoreWorkspaceData } from "../features/score-management/hooks/useScoreWorkspaceData";
import { useScoreReleaseWorkflow, type ScorePublicationPreviewOptions } from "../features/score-management/hooks/useScoreReleaseWorkflow";
import { useExamEducationStage } from "../features/score-management/hooks/useExamEducationStage";
import { sessionPublicationText, useSessionPublication } from "../features/score-management/hooks/useSessionPublication";
import { useScoreAction } from "../features/score-management/hooks/useScoreAction";
import { useScoreExport } from "../features/score-management/hooks/useScoreExport";
import { activeRegradeCount, pendingCorrections, releaseStatusModel, rosterCounts } from "../features/score-management/releaseStatus";
import { createRosterColumns, createScoreColumns } from "../features/score-management/scoreColumns";
import {
  qualityCheckStatus,
  rosterReconciliationStatus,
  scoreWorkspacePresentation,
  scoreExamOptionLabel,
  statusLabels,
} from "../features/score-management/scorePresentation";

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

type ScoreWorkspaceData = ReturnType<typeof useScoreWorkspaceData>;
type ScoreTableData = Pick<ScoreWorkspaceData,
  "grades" | "gradeTotal" | "filteredGradeTotal" | "roster" | "quality" |
  "loadingScores" | "loadingMoreGrades" | "gradesHaveMore" | "keyword" |
  "setKeyword" | "setAppliedKeyword" | "statusFilter" | "setStatusFilter" |
  "loadMoreGrades"
>;

function ScoreWorkspaceNotices({ identityError, error, onRetry }: Readonly<{
  identityError: ScoreWorkspaceData["identities"]["error"];
  error: ScoreWorkspaceData["error"];
  onRetry: () => void;
}>) {
  return <>
    {identityError ? <Alert type="warning" showIcon message="学生身份信息读取不完整" description={identityError} /> : null}
    {error ? <ErrorState message={error} onRetry={onRetry} /> : null}
  </>;
}

type ScoreResultsTablesProps = Readonly<{
  data: ScoreTableData;
  rosterColumns: ReturnType<typeof createRosterColumns>;
  columns: ReturnType<typeof createScoreColumns>;
  rosterBadge: ReturnType<typeof rosterReconciliationStatus>;
  qualityBadge: ReturnType<typeof qualityCheckStatus>;
  presentation: ReturnType<typeof scoreWorkspacePresentation>;
  selectedExamName: string | undefined;
  publishedOrLocked: boolean;
  qualityActionFor: NonNullable<ComponentProps<typeof QualityPanel>["actionFor"]>;
  statusOptions: Array<{ label: string; value: string }>;
  compactChecks?: boolean;
}>;

function ScoreResultsTables({
  data, rosterColumns, columns, rosterBadge, qualityBadge, presentation,
  selectedExamName, publishedOrLocked, qualityActionFor, statusOptions, compactChecks = false
}: ScoreResultsTablesProps) {
  // 筛选和翻页继续使用父层回调，展示层不保存另一份考试快照。
  const {
    grades: filteredGrades, gradeTotal, filteredGradeTotal, roster, quality,
    loadingScores, loadingMoreGrades, gradesHaveMore, keyword, setKeyword,
    setAppliedKeyword, statusFilter, setStatusFilter, loadMoreGrades
  } = data;
  return (
    <main className="score-main">
      <details className="score-table-panel score-roster-panel" open={!compactChecks} id="score-roster-details">
        <summary>名册明细 · {rosterBadge.label}</summary>
        <div className="panel-head">
          <div>
            <h2>名册对账</h2>
            <p>按本场冻结名册与已匹配答卷核对；缺考自动计算，身份冲突、缺页和未完成评分仍需处理。</p>
          </div>
          <StatusTag tone={rosterBadge.tone}>
            {rosterBadge.label}
          </StatusTag>
        </div>
        {loadingScores ? <LoadingState label="正在核对考试名册" /> : (
          <ResponsiveTable
            className="dense-data-table"
            rowKey="key"
            size="small"
            columns={rosterColumns}
            dataSource={roster?.entries ?? []}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            locale={{ emptyText: <Empty description="本场考试尚未关联有效班级名册。" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
          />
        )}
      </details>

      <details className="score-quality-panel" open={!compactChecks}>
        <summary>基础成绩检查明细{qualityBadge ? ` · ${qualityBadge.label}` : ""}</summary>
        <div className="panel-head">
          <div>
            <h2>发布前质量检查</h2>
            <p>{selectedExamName ?? "未选择考试"}</p>
          </div>
          {qualityBadge ? <StatusTag tone={qualityBadge.tone}>{qualityBadge.label}</StatusTag> : null}
        </div>
        {loadingScores ? <LoadingState label="正在读取质量检查" /> : <QualityPanel quality={quality} publishedOrLocked={publishedOrLocked} actionFor={qualityActionFor} />}
      </details>

      <section className="score-table-panel score-results-panel" id="score-results" role="group" aria-labelledby="score-results-title" tabIndex={-1}>
        <div className="panel-head">
          <div>
            <h2 id="score-results-title">成绩列表</h2>
            <p>
              当前显示 {filteredGrades.length} 条 · 共 {filteredGradeTotal} 条
              {filteredGradeTotal !== gradeTotal ? `（全场 ${gradeTotal} 条）` : ""}
            </p>
          </div>
          <Space wrap>
            <Input.Search
              prefix={<Search size={16} />}
              placeholder="搜索匿名码、学生、班级"
              value={keyword}
              allowClear
              enterButton="搜索"
              onChange={(event) => {
                setKeyword(event.target.value);
                if (!event.target.value) setAppliedKeyword("");
              }}
              onSearch={(value) => setAppliedKeyword(value.trim())}
            />
            <Select className="toolbar-select" value={statusFilter} options={statusOptions} onChange={setStatusFilter} />
          </Space>
        </div>
        {loadingScores ? (
          <LoadingState label="正在读取成绩" />
        ) : (
          <ResponsiveTable
            className="dense-data-table"
            rowKey="id"
            size="small"
            columns={columns}
            dataSource={filteredGrades}
            pagination={false}
            locale={{ emptyText: <Empty description={presentation.emptyGrades} image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
          />
        )}
        {!loadingScores && gradesHaveMore ? (
          <div className="load-more-row">
            <Button loading={loadingMoreGrades} onClick={() => void loadMoreGrades()}>加载更多成绩</Button>
          </div>
        ) : null}
      </section>
    </main>
  );
}

export function ScoreManagementPage({
  mode,
  canManage,
  canReadStudentNames,
  onNavigate,
  canHandleCapture = false,
  canHandleReview = false,
  canHandleArbitration = false,
  initialExamId = "",
  selectionScope,
  resetUnavailableRememberedSelection = false
}: Readonly<{
  mode: ProductExperience;
  canManage: boolean;
  canReadStudentNames: boolean;
  canReadAudit: boolean;
  onNavigate?: (path: string) => void;
  canHandleCapture?: boolean;
  canHandleReview?: boolean;
  canHandleArbitration?: boolean;
  initialExamId?: string;
  selectionScope?: string;
  resetUnavailableRememberedSelection?: boolean;
}>) {
  // 本页不再重复展示审计流水，审计记录仍由服务端保存并在统一操作审计页查看。
  const workspace = useScoreWorkspaceData({ canManage, canReadAudit: false, canReadStudentNames, initialExamId, selectionScope, resetUnavailableRememberedSelection });
  const {
    exams, selectedExamId, setSelectedExamId, grades,
    allGradesLocked, quality, releaseGate, scoreReleases,
    currentPublishedRelease,
    regradeJobs, roster, identities, loadingExams,
    loadingScores, error, loadExamList, loadScores, refresh
  } = workspace;
  // 先读取考试目录，再读取自动选中考试的成绩；目录控件的 loading 也属于首屏等待。
  usePageLoading(loadingExams || loadingScores);
  const [previewRelease, setPreviewRelease] = useState<{ examId: string; release: ScoreRelease; options?: ScorePublicationPreviewOptions } | null>(null);
  const portalUrl = import.meta.env.VITE_STUDENT_PORTAL_URL?.trim();
  const { actioning, runAction } = useScoreAction({ selectedExamId, loadExamList, loadScores });
  // 数据重读失败时快照保留用于查看；新读数到来前或已有提交进行中，不能再依据旧门禁提交。
  const canWrite = canManage && !loadingExams && !loadingScores && !error && !actioning;

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const publishedOrLocked = selectedExam?.status === "published" || allGradesLocked;
  // 发布记录按版本号排序，较早草稿也可能最后发布；学生端当前看到哪个版本以服务端“当前版本”指针为准。
  const publishedRelease = currentPublishedRelease ?? undefined;
  const runExport = useScoreExport({
    ownerKey: JSON.stringify([selectionScope, initialExamId, selectedExamId, mode, canManage]),
    enabled: Boolean(canWrite), selectedExamId, runAction,
    onExport: (result, fallbackFilename) => {
      saveBlob(result.blob, result.filename ?? fallbackFilename);
    }
  });
  const previewStudentLabels = useMemo(() => Object.fromEntries(Object.values(identities.students).map((student) => {
    const className = identities.classes[student.class_id]?.name;
    const classSuffix = className ? ` · ${className}` : "";
    return [student.id, `${student.name} · ${student.student_no}${classSuffix}`];
  })), [identities]);

  const adminView = mode === "admin";
  const educationStage = useExamEducationStage(selectedExam, adminView && canManage);
  const releaseWorkflow = useScoreReleaseWorkflow({
    selectedExamId, selectedExam, currentPublishedRelease: publishedRelease, educationStage, runAction,
    reload: () => loadScores(selectedExamId),
    onPreview: (release, options) => setPreviewRelease({ examId: selectedExamId, release, options })
  });
  const [historyOpen, setHistoryOpen] = useState(false);
  const readiness = publicationReadiness(quality, releaseGate, roster);
  const corrections = pendingCorrections(regradeJobs, scoreReleases);
  const linkedRegradeId = routeQueryParam("regrade");
  // 从复核申请跳来时优先发布该申请对应的更正；否则按完成先后依次发布。
  const nextCorrection = corrections.find(item => item.job.id === linkedRegradeId) ?? corrections[0];
  const statusModel = releaseStatusModel({
    examId: selectedExamId, loading: loadingExams || loadingScores, error,
    readiness: readiness.status, blockedGroups: readiness.groups.length,
    publishedRelease, pendingCorrections: corrections.length
  });
  const sessionPublication = useSessionPublication(exams, selectedExam, Boolean(publishedRelease), adminView && canManage);
  const exportBlocked = !publishedRelease || Boolean(sessionPublication?.missingSubjects.length);
  // “复核与异常”页约定：?regrade=<任务> 定位并高亮该任务，不带值的 ?regrade 只滚动到复评面板。
  const regradePath = (jobId?: string) => `/exams/${encodeURIComponent(selectedExamId)}/quality?regrade${jobId ? `=${encodeURIComponent(jobId)}` : ""}`;
  const qualityActionFor = (code: string) => {
    if (!selectedExamId) return undefined;
    if (!onNavigate) return undefined;
    const base = `/exams/${encodeURIComponent(selectedExamId)}`;
    if (["unfinished_review_tasks", "missing_final_grades", "missing_submission_grades"].includes(code) && canHandleReview) return { label: "处理阅卷任务", run: () => onNavigate(`${base}/grading`) };
    if (code === "unfinished_arbitration_tasks" && canHandleArbitration) return { label: "处理仲裁任务", run: () => onNavigate(`/arbitration?exam=${encodeURIComponent(selectedExamId)}`) };
    if (["ocr_failed_unhandled", "missing_pages_unresolved", "unidentified_submission", "missing_submission_unresolved"].includes(code) && canHandleCapture) return { label: "核查答卷与匹配", run: () => onNavigate(`${base}/capture`) };
    return undefined;
  };
  const publicationActionFor = (code: string, route?: string) => {
    if (["score_integrity_mismatch", "release_snapshot_incomplete"].includes(code) && onNavigate && canHandleReview) return { label: "核查阅卷结果", run: () => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/grading`) };
    const knownAction = qualityActionFor(code);
    if (knownAction) return knownAction;
    // 复评任务在“复核与异常”页处理；已完成待发布的更正就在本页发布。
    if (route === "regrade" && nextCorrection) return { label: "发布更正", run: () => releaseWorkflow.openCorrection(nextCorrection) };
    if (route === "regrade" && onNavigate && selectedExamId) return { label: "查看复评任务", run: () => onNavigate(regradePath()) };
    if (route === "roster" && onNavigate && canHandleCapture) return { label: "核对学生答卷", run: () => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/capture`) };
    // 结束答卷导入等收卷环节的新检查都带 capture 路由；统一进入答卷与匹配页，不显示“请联系管理员”。
    if (route === "capture" && onNavigate && canHandleCapture) return { label: "核查答卷与匹配", run: () => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/capture`) };
    if (onNavigate && selectedExamId && (route === "questions" || code === "quality_questions_missing")) return { label: "查看本场考试小题", run: () => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/questions`) };
    if (onNavigate && selectedExamId && route === "quality") return { label: "查看质量问题", run: () => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/quality`) };
    return undefined;
  };

  const exportOfficialGrades = () => {
    if (!selectedExamId) return;
    // 按钮只提示当前学科是否已发布；同场其他学科及固定版本由服务端在同一快照再次核验。
    void runExport("export-official", exportPublishedExamGrades, "教师成绩表已导出，班级平均分在每张表最下面", `exam-${selectedExamId}-teacher-scores.xlsx`);
  };

  const rosterColumns = createRosterColumns({ canOpenSubmission: entry => entry.resolution_code === "grading_incomplete" ? canHandleReview && Boolean(entry.submission_id) : canHandleCapture, openSubmission: onNavigate && (canHandleCapture || canHandleReview) ? entry => {
    const base = `/exams/${encodeURIComponent(selectedExamId)}`;
    if (entry.resolution_code === "grading_incomplete" && canHandleReview && entry.submission_id) onNavigate(`${base}/grading?q=${encodeURIComponent(entry.submission_id)}`);
    else if (canHandleCapture) onNavigate(`${base}/capture?submission=${encodeURIComponent(entry.submission_id ?? "")}&student=${encodeURIComponent(entry.student_id ?? "")}`);
  } : undefined });
  // 旧的“公开并锁定”入口已移除，新考试不会再出现锁定成绩；只有确有锁定记录时才显示锁定列。
  const columns = createScoreColumns({ canReadStudentNames, identities, showLock: allGradesLocked || grades.some(grade => grade.locked) });
  const qualityBadge = qualityCheckStatus(quality);
  const rosterBadge = rosterReconciliationStatus(roster?.summary.unresolved ?? 0);
  const presentation = scoreWorkspacePresentation(mode);
  const linkedAppealId = routeQueryParam("appeal");
  const linkedRegrade = regradeJobs.find(job => job.id === linkedRegradeId);
  const activeRegrades = activeRegradeCount(regradeJobs);

  const runPrimary = () => {
    const primary = statusModel.primary;
    if (primary?.kind === "locate") {
      const target = document.getElementById("score-publication-checklist");
      target?.scrollIntoView({ block: "start", behavior: "smooth" });
      target?.focus({ preventScroll: true });
    } else if (primary?.kind === "publish") releaseWorkflow.openPublish();
    else if (primary?.kind === "correction" && nextCorrection) releaseWorkflow.openCorrection(nextCorrection);
    else if (primary?.kind === "export") exportOfficialGrades();
  };

  // 复核申请跳转来的提示：更正已可发布时引导发布；复评仍在进行时引导到复评所在页面。
  const linkedCorrectionPending = Boolean(linkedRegradeId && corrections.some(item => item.job.id === linkedRegradeId));
  const appealNotice = linkedCorrectionPending
    ? "本次申请的复评已完成，点上方「发布更正」后返回原申请公布结果。"
    : linkedRegrade && linkedRegrade.status !== "ready_for_release"
      ? "本次申请的复评仍在进行，请在“复核与异常”页继续处理。"
      : "正在处理复核申请，发布完成后返回原申请公布结果。";

  const statusOptions = useMemo(() => {
    const statuses = Array.from(new Set(grades.map((grade) => grade.status)));
    return [{ label: "全部状态", value: "all" }, ...statuses.map((status) => ({ label: statusLabels[status] ?? "未知状态", value: status }))];
  }, [grades]);

  return (
    <div className={presentation.shellClass}>
      <section className="score-topbar">
        <div>
          <Space>
            <h1>{presentation.title}</h1>
          </Space>
          {mode !== "admin" ? <p>{presentation.description}</p> : null}
        </div>
        <Space wrap>
          <Select
            className="score-exam-select"
            loading={loadingExams}
            disabled={Boolean(actioning)}
            value={selectedExamId || undefined}
            placeholder="选择考试"
            options={exams.map((exam) => ({ label: scoreExamOptionLabel(exam), value: exam.id }))}
            onChange={setSelectedExamId}
          />
          <Button icon={<RefreshCw size={16} />} onClick={() => void refresh()} loading={loadingExams || loadingScores}>
            刷新
          </Button>
        </Space>
      </section>

      <ScoreWorkspaceNotices identityError={identities.error} error={error} onRetry={() => void refresh()} />

      <section className={presentation.workspaceClass}>
        {adminView ? <section className="score-publication-task" aria-label="成绩发布任务">
          <ScoreReleaseStatus
            model={statusModel} canWrite={Boolean(canWrite)} actioning={actioning}
            onPrimary={runPrimary} onExport={exportOfficialGrades} exportBlocked={exportBlocked}
            roster={roster} rosterColumns={rosterColumns} sessionText={sessionPublicationText(sessionPublication)}
            activeRegrades={activeRegrades} onOpenRegrades={onNavigate && selectedExamId ? () => onNavigate(regradePath(regradeJobs.find(job => job.status !== "ready_for_release" && job.status !== "cancelled")?.id)) : undefined}
            historyCount={scoreReleases.length} onOpenHistory={() => setHistoryOpen(true)}
          />
          <ScorePublicationChecklist quality={quality} gate={releaseGate} roster={roster} actionFor={publicationActionFor} />
          {linkedAppealId && onNavigate ? <Alert type="info" showIcon message={appealNotice} action={<Space wrap>
            {linkedRegrade && linkedRegrade.status !== "ready_for_release" ? <Button onClick={() => onNavigate(regradePath(linkedRegrade.id))}>前往复评</Button> : null}
            <Button disabled={Boolean(actioning)} onClick={() => onNavigate(`/exams/${encodeURIComponent(selectedExamId)}/appeals?appeal=${encodeURIComponent(linkedAppealId)}`)}>返回原复核申请</Button>
          </Space>} /> : null}
        </section> : null}
        {mode !== "admin" ? <ScoreResultsTables
          data={workspace}
          rosterColumns={rosterColumns}
          columns={columns}
          rosterBadge={rosterBadge}
          qualityBadge={qualityBadge}
          presentation={presentation}
          selectedExamName={selectedExam?.name}
          publishedOrLocked={publishedOrLocked}
          qualityActionFor={qualityActionFor}
          statusOptions={statusOptions}
        /> : null}
      </section>

      {adminView ? <>
        <ScorePublishDialog
          workflow={releaseWorkflow} exam={selectedExam} counts={rosterCounts(roster)} educationStage={educationStage}
          actioning={actioning} canWrite={Boolean(canWrite)} absentees={(roster?.entries ?? []).filter(entry => entry.status === "absent")}
        />
        <ScoreCorrectionDialog workflow={releaseWorkflow} currentRelease={publishedRelease} remaining={corrections.length} actioning={actioning} canWrite={Boolean(canWrite)} />
        <ScoreReleaseHistory
          open={historyOpen} onClose={() => setHistoryOpen(false)} releases={scoreReleases} currentRelease={publishedRelease}
          canWrite={Boolean(canWrite)} canPublish={readiness.status === "ready"} actioning={actioning}
          onPreview={releaseWorkflow.showDraftPreview}
          onPublish={releaseWorkflow.publishRelease} onRevokePaper={releaseWorkflow.revokeSharedPaper}
          onRepublish={() => { setHistoryOpen(false); releaseWorkflow.openPublish(); }}
        />
      </> : null}
      {adminView && previewRelease?.examId === selectedExamId && (previewRelease.options?.isCurrent?.() ?? true) ? <StudentScorePreviewDialog key={`${selectedExamId}:${previewRelease.release.id}:${previewRelease.options?.retrySnapshot ?? ""}:${Boolean(previewRelease.options?.onConfirm)}`} release={previewRelease.release} currentVersion={publishedRelease?.version} portalUrl={portalUrl} studentLabels={previewStudentLabels} onClose={() => setPreviewRelease(null)}
        onSnapshot={previewRelease.options?.onSnapshot} onConfirm={previewRelease.options?.onConfirm} canConfirm={Boolean(canWrite)} confirming={actioning === "release-publish"} /> : null}
    </div>
  );
}
