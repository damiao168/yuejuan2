import { useMemo, useState, type ComponentProps } from "react";
import { Alert, App, Button, Empty, Input, Select, Space } from "antd";
import { RefreshCw, Search } from "lucide-react";
import { examSubjectLabel } from "../constants/examStatus";
import {
  confirmExamGrades,
  exportExamGrades,
  finalizeExamGrades,
  publishExamGrades
} from "../api/scores";
import { ErrorState, LoadingState } from "../components/PageState";
import { usePageLoading } from "../components/pageReadiness";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import type { ProductExperience } from "../router/experience";
import { ScoreActionsPanel } from "../features/score-management/components/ScoreActionsPanel";
import { QualityPanel } from "../features/score-management/components/QualityPanel";
import { ScoreReleaseWorkspace } from "../features/score-management/components/ScoreReleaseWorkspace";
import { ScoreWorkflowModals } from "../features/score-management/components/ScoreWorkflowModals";
import { StudentScorePreviewDialog } from "../features/score-management/components/StudentScorePreviewDialog";
import type { ScoreRelease } from "../api/scoreReleases";
import { useScoreWorkspaceData } from "../features/score-management/hooks/useScoreWorkspaceData";
import { useScoreReleaseWorkflow } from "../features/score-management/hooks/useScoreReleaseWorkflow";
import { useRegradeWorkflow } from "../features/score-management/hooks/useRegradeWorkflow";
import { useRosterAttendance } from "../features/score-management/hooks/useRosterAttendance";
import { useScoreAction } from "../features/score-management/hooks/useScoreAction";
import { useStepUp } from "../auth/stepUpContext";
import { createRosterColumns, createScoreColumns } from "../features/score-management/scoreColumns";
import {
  basePublicationLabel,
  createSummary,
  portalPublicationLabel,
  qualityCheckStatus,
  rosterReconciliationStatus,
  scoreWorkspacePresentation,
  statusLabels,
  summaryStatus
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
}>;

function ScoreResultsTables({
  data, rosterColumns, columns, rosterBadge, qualityBadge, presentation,
  selectedExamName, publishedOrLocked, qualityActionFor, statusOptions
}: ScoreResultsTablesProps) {
  // 筛选和翻页继续使用父层回调，展示层不保存另一份考试快照。
  const {
    grades: filteredGrades, gradeTotal, filteredGradeTotal, roster, quality,
    loadingScores, loadingMoreGrades, gradesHaveMore, keyword, setKeyword,
    setAppliedKeyword, statusFilter, setStatusFilter, loadMoreGrades
  } = data;
  return (
    <main className="score-main">
      <section className="score-table-panel score-roster-panel">
        <div className="panel-head">
          <div>
            <h2>名册对账</h2>
            <p>以本场考试关联班级为应考名单；未交卷、身份冲突和缺页未解决时不能发布成绩。</p>
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
      </section>

      <section className="score-quality-panel">
        <div className="panel-head">
          <div>
            <h2>发布前质量检查</h2>
            <p>{selectedExamName ?? "未选择考试"}</p>
          </div>
          {qualityBadge ? <StatusTag tone={qualityBadge.tone}>{qualityBadge.label}</StatusTag> : null}
        </div>
        {loadingScores ? <LoadingState label="正在读取质量检查" /> : <QualityPanel quality={quality} publishedOrLocked={publishedOrLocked} actionFor={qualityActionFor} />}
      </section>

      <section className="score-table-panel">
        <div className="panel-head">
          <div>
            <h2>成绩列表</h2>
            <p>
              已加载 {filteredGrades.length} / {filteredGradeTotal} 条
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
            pagination={{ pageSize: 8, showSizeChanger: false }}
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
  canReadAudit,
  onNavigate,
  canHandleCapture = false,
  canHandleReview = false,
  canHandleArbitration = false,
  initialExamId = ""
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
}>) {
  const { message, modal } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const workspace = useScoreWorkspaceData({ canManage, canReadAudit, canReadStudentNames, initialExamId });
  const {
    exams, selectedExamId, setSelectedExamId, submissions, grades, gradeTotal,
    allGradesLocked, quality, releaseGate, scoreReleases,
    currentPublishedRelease,
    regradeJobs, roster, setRoster, identities, auditLogs, loadingExams,
    loadingScores, error, loadExamList, loadScores, refresh
  } = workspace;
  // 先读取考试目录，再读取自动选中考试的成绩；目录控件的 loading 也属于首屏等待。
  usePageLoading(loadingExams || loadingScores);
  const [lastWatermark, setLastWatermark] = useState("");
  const [confirmReason, setConfirmReason] = useState("");
  const [publishReason, setPublishReason] = useState("");
  const [previewRelease, setPreviewRelease] = useState<{ examId: string; release: ScoreRelease } | null>(null);
  const portalUrl = import.meta.env.VITE_STUDENT_PORTAL_URL?.trim();
  const { actioning, setActioning, runAction } = useScoreAction({ selectedExamId, loadExamList, loadScores });
  // 数据重读失败时快照保留用于查看；新读数到来前或已有提交进行中，不能再依据旧门禁提交。
  const canWrite = canManage && !loadingScores && !error && !actioning;

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const summary = useMemo(() => createSummary(submissions, gradeTotal, quality, roster), [gradeTotal, quality, roster, submissions]);
  const scoreAuditLogs = useMemo(() => auditLogs.filter((item) => item.action.startsWith("score.")), [auditLogs]);
  const publishedOrLocked = selectedExam?.status === "published" || allGradesLocked;
  // 版本列表按创建序号排序，较早草稿也可能最后发布；门户生效状态以服务端当前指针为准。
  const publishedRelease = currentPublishedRelease ?? undefined;
  const previewStudentLabels = useMemo(() => Object.fromEntries(Object.values(identities.students).map((student) => {
    const className = identities.classes[student.class_id]?.name;
    const classSuffix = className ? ` · ${className}` : "";
    return [student.id, `${student.name} · ${student.student_no}${classSuffix}`];
  })), [identities]);

  const releaseWorkflow = useScoreReleaseWorkflow({
    selectedExamId, selectedExam, gradeTotal, releaseGate,
    currentPublishedRelease: publishedRelease, runAction
  });
  const regradeWorkflow = useRegradeWorkflow({
    canManage, selectedExamId, publishedRelease, grades, runAction, setActioning
  });
  const attendanceWorkflow = useRosterAttendance({ selectedExamId, setRoster, runAction });
  const { openAttendanceEditor } = attendanceWorkflow;
  const qualityActionFor = (code: string) => {
    if (!onNavigate || !selectedExamId) return undefined;
    const base = `/exams/${encodeURIComponent(selectedExamId)}`;
    if (["unfinished_review_tasks", "missing_final_grades"].includes(code) && canHandleReview) return { label: "处理阅卷任务", run: () => onNavigate(`${base}/grading`) };
    if (code === "unfinished_arbitration_tasks" && canHandleArbitration) return { label: "处理仲裁任务", run: () => onNavigate(`/arbitration?exam=${encodeURIComponent(selectedExamId)}`) };
    if (["ocr_failed_unhandled", "missing_pages_unresolved", "unidentified_submission", "missing_submission_unresolved", "no_submission_grades"].includes(code) && canHandleCapture) return { label: "核查答卷与匹配", run: () => onNavigate(`${base}/capture`) };
    if (code === "grades_not_confirmed") return { label: "定位成绩确认", run: () => { const details = document.querySelector<HTMLDetailsElement>(".score-advanced-details"); if (details) { details.open = true; details.scrollIntoView({ block: "nearest" }); } } };
    return undefined;
  };

  const finalize = () =>
    runAction(
      "finalize",
      async () => {
        if (!selectedExamId) {
          throw new Error("请先选择考试");
        }
        await finalizeExamGrades(selectedExamId);
      },
      "最终成绩已汇总"
    );

  const confirmGrades = () =>
    runAction(
      "confirm",
      async () => {
        if (!selectedExamId) {
          throw new Error("请先选择考试");
        }
        if (!confirmReason.trim()) {
          throw new Error("请填写确认原因");
        }
        await confirmExamGrades(selectedExamId, confirmReason.trim());
      },
      "成绩已确认"
    );

  const publish = () => {
    if (!selectedExamId) {
      message.error("请先选择考试");
      return;
    }
    if (!quality?.can_publish) {
      message.error("发布前质量检查未通过");
      return;
    }
    if (!publishReason.trim()) {
      message.error("请填写发布原因");
      return;
    }
    modal.confirm({
      title: "确认发布成绩",
      content: `将《${selectedExam?.name ?? "未选择考试"}》共 ${gradeTotal} 份基础成绩公开并锁定，学校原有的考试成绩查询入口将可读取这些记录。学生门户的成绩版本需单独发布；如需更正，请使用申诉或复评流程。确定继续吗？`,
      okText: "公开并锁定",
      cancelText: "取消",
      onOk: () =>
        runAction(
          "publish",
          async () => {
            // 正式发布前必须再次通过校验；用户取消校验只是结束当前操作，不应被当成发布失败。
            await runWithStepUp({
              reason: "正式发布成绩",
              action: () => publishExamGrades(selectedExamId, publishReason.trim())
            });
            await loadExamList();
          },
          "基础成绩已公开并锁定"
        )
    });
  };

  const exportGrades = () => {
    if (!selectedExamId) {
      message.error("请先选择考试");
      return;
    }
    modal.confirm({
      title: "导出成绩",
      content: "将导出该考试的全部成绩（CSV 表格文件）。导出文件带追溯水印，导出操作会被系统记录。确定导出吗？",
      okText: "导出",
      cancelText: "取消",
      onOk: () =>
        runAction(
          "export",
          async () => {
            const result = await exportExamGrades(selectedExamId);
            setLastWatermark(result.watermark ?? "");
            saveBlob(result.blob, result.filename ?? `exam-${selectedExamId}-grades.csv`);
          },
          "成绩已导出"
        )
    });
  };

  const rosterColumns = createRosterColumns({ canWrite: Boolean(canWrite), publishedOrLocked, openAttendanceEditor, canOpenSubmission: entry => entry.resolution_code === "grading_incomplete" ? canHandleReview && Boolean(entry.submission_id) : canHandleCapture, openSubmission: onNavigate && (canHandleCapture || canHandleReview) ? entry => {
    const base = `/exams/${encodeURIComponent(selectedExamId)}`;
    if (entry.resolution_code === "grading_incomplete" && canHandleReview && entry.submission_id) onNavigate(`${base}/grading?q=${encodeURIComponent(entry.submission_id)}`);
    else if (canHandleCapture) onNavigate(`${base}/capture?submission=${encodeURIComponent(entry.submission_id ?? "")}&student=${encodeURIComponent(entry.student_id ?? "")}`);
  } : undefined });
  const columns = createScoreColumns({ canReadStudentNames, identities });
  const publicationState = {
    examId: selectedExamId, loadingExams, loadingScores, error,
    examStatus: selectedExam?.status, releaseVersion: publishedRelease?.version
  };
  const summaryBadge = summaryStatus(mode, selectedExam?.status, publishedOrLocked, summary.canPublish);
  const qualityBadge = qualityCheckStatus(quality);
  const rosterBadge = rosterReconciliationStatus(roster?.summary.unresolved ?? 0);
  const presentation = scoreWorkspacePresentation(mode);

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
          <p>{presentation.description}</p>
        </div>
        <Space wrap>
          <Select
            className="score-exam-select"
            loading={loadingExams}
            disabled={Boolean(actioning)}
            value={selectedExamId || undefined}
            placeholder="选择考试"
            options={exams.map((exam) => ({ label: `${exam.name} · ${examSubjectLabel(exam.subject)}`, value: exam.id }))}
            onChange={setSelectedExamId}
          />
          <Button icon={<RefreshCw size={16} />} onClick={() => void refresh()} loading={loadingExams || loadingScores}>
            刷新
          </Button>
        </Space>
      </section>

      <ScoreWorkspaceNotices identityError={identities.error} error={error} onRetry={() => void refresh()} />

      {mode === "admin" ? <section className="score-publication-status" aria-label="成绩公开状态">
        <div>
          <span className="score-publication-eyebrow">考试成绩查询</span>
          <strong>{basePublicationLabel(publicationState)}</strong>
          <p>公开并锁定基础成绩后，学校原有的考试成绩查询入口可读取这些记录。</p>
        </div>
        <div>
          <span className="score-publication-eyebrow">学生门户（推荐发布入口）</span>
          <strong>{portalPublicationLabel(publicationState)}</strong>
          <p>门户展示当前已发布的成绩版本；草稿与后续更正需要单独发布。</p>
        </div>
      </section> : null}

      <section className="score-summary-strip">
        <div>
          <span>应考人数</span>
          <strong>{summary.expectedStudents}</strong>
        </div>
        <div>
          <span>实收答卷</span>
          <strong>{summary.receivedSubmissions}</strong>
        </div>
        <div>
          <span>已生成成绩</span>
          <strong>{summary.completedGrades}</strong>
        </div>
        <div>
          <span>已确认缺考</span>
          <strong>{summary.absentStudents}</strong>
        </div>
        <div>
          <span>名册未解决</span>
          <strong>{summary.unresolvedRoster}</strong>
        </div>
        <div>
          <span>流程待办</span>
          <strong>{summary.unfinishedReviews + summary.pendingArbitrations + summary.ocrFailures}</strong>
        </div>
        <div>
          <span>{presentation.summaryTitle}</span>
          <StatusTag tone={summaryBadge.tone}>{summaryBadge.label}</StatusTag>
        </div>
      </section>

      <section className={presentation.workspaceClass}>
        {mode === "admin" ? <ScoreReleaseWorkspace
          canWrite={canWrite}
          selectedExamId={selectedExamId}
          gradeTotal={gradeTotal}
          releaseGate={releaseGate}
          scoreReleases={scoreReleases}
          regradeJobs={regradeJobs}
          publishedRelease={publishedRelease}
          actioning={actioning}
          releaseWorkflow={releaseWorkflow}
          regradeWorkflow={regradeWorkflow}
          onPreviewRelease={(release) => setPreviewRelease({ examId: selectedExamId, release })}
          portalUrl={portalUrl}
        /> : null}
        <ScoreResultsTables
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
        />

        {mode === "admin" ? <ScoreActionsPanel
          publishedOrLocked={publishedOrLocked} canWrite={Boolean(canWrite)} selectedExamId={selectedExamId}
          gradeTotal={gradeTotal} quality={quality} actioning={actioning}
          confirmReason={confirmReason} publishReason={publishReason}
          setConfirmReason={setConfirmReason} setPublishReason={setPublishReason}
          finalize={finalize} confirmGrades={confirmGrades} publish={publish} exportGrades={exportGrades}
          lastWatermark={lastWatermark} canReadAudit={canReadAudit} scoreAuditLogs={scoreAuditLogs}
        /> : null}
      </section>

      <ScoreWorkflowModals
        selectedExam={selectedExam}
        gradeTotal={gradeTotal}
        publishedReleaseExists={Boolean(publishedRelease)}
        actioning={actioning}
        attendanceWorkflow={attendanceWorkflow}
        releaseWorkflow={releaseWorkflow}
        regradeWorkflow={regradeWorkflow}
      />
      {mode === "admin" && previewRelease?.examId === selectedExamId ? <StudentScorePreviewDialog key={`${selectedExamId}:${previewRelease.release.id}`} release={previewRelease.release} currentVersion={publishedRelease?.version} portalUrl={portalUrl} studentLabels={previewStudentLabels} onClose={() => setPreviewRelease(null)} /> : null}
    </div>
  );
}
