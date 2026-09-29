import { useMemo, useState } from "react";
import { Alert, App, Button, Divider, Empty, Input, Select, Space, type TableColumnsType } from "antd";
import { Calculator, CheckCircle2, Download, FileWarning, LockKeyhole, RefreshCw, Search, Send, UserCheck, UserX } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { examStatusLabels, examSubjectLabel } from "../constants/examStatus";
import {
  confirmExamGrades,
  exportExamGrades,
  finalizeExamGrades,
  publishExamGrades,
  type RosterEntry,
  type SubmissionGrade
} from "../api/scores";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import type { ProductExperience } from "../router/experience";
import { AuditPanel } from "../features/score-management/components/AuditPanel";
import { QualityPanel } from "../features/score-management/components/QualityPanel";
import { ScoreReleaseWorkspace } from "../features/score-management/components/ScoreReleaseWorkspace";
import { ScoreWorkflowModals } from "../features/score-management/components/ScoreWorkflowModals";
import { useScoreWorkspaceData } from "../features/score-management/hooks/useScoreWorkspaceData";
import { useScoreReleaseWorkflow } from "../features/score-management/hooks/useScoreReleaseWorkflow";
import { useRegradeWorkflow } from "../features/score-management/hooks/useRegradeWorkflow";
import { useRosterAttendance } from "../features/score-management/hooks/useRosterAttendance";
import { isStepUpCancelledError, useStepUp } from "../auth/stepUpContext";
import {
  createSummary,
  formatScore,
  rosterResolutionLabels,
  rosterStatusLabels,
  sourceLabels,
  statusLabels,
  statusTone
} from "../features/score-management/scorePresentation";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

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

export function ScoreManagementPage({
  mode,
  canManage,
  canReadStudentNames,
  canReadAudit,
  initialExamId = ""
}: {
  mode: ProductExperience;
  canManage: boolean;
  canReadStudentNames: boolean;
  canReadAudit: boolean;
  initialExamId?: string;
}) {
  const { message, modal } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const canWrite = canManage;
  const workspace = useScoreWorkspaceData({ canManage, canReadAudit, canReadStudentNames, initialExamId });
  const {
    exams, selectedExamId, setSelectedExamId, submissions, grades, gradeTotal,
    filteredGradeTotal, allGradesLocked, quality, releaseGate, scoreReleases,
    regradeJobs, roster, setRoster, identities, auditLogs, keyword, setKeyword,
    setAppliedKeyword, statusFilter, setStatusFilter, loadingExams, loadingScores,
    loadingMoreGrades, error, loadExamList, loadScores, refresh, loadMoreGrades,
    gradesHaveMore
  } = workspace;
  const [lastWatermark, setLastWatermark] = useState("");
  const [confirmReason, setConfirmReason] = useState("");
  const [publishReason, setPublishReason] = useState("");
  const [actioning, setActioning] = useState<string | null>(null);

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const summary = useMemo(() => createSummary(submissions, gradeTotal, quality, roster), [gradeTotal, quality, roster, submissions]);
  const scoreAuditLogs = useMemo(() => auditLogs.filter((item) => item.action.startsWith("score.")), [auditLogs]);
  const publishedOrLocked = selectedExam?.status === "published" || allGradesLocked;
  const publishedRelease = useMemo(() => scoreReleases.find((release) => release.status === "published"), [scoreReleases]);
  const filteredGrades = grades;

  const runAction = async (key: string, action: () => Promise<void>, successText: string) => {
    setActioning(key);
    try {
      await action();
      message.success(successText);
      if (selectedExamId) {
        await loadScores(selectedExamId);
      }
    } catch (currentError) {
      if (!isStepUpCancelledError(currentError)) message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
  };

  const releaseWorkflow = useScoreReleaseWorkflow({
    selectedExamId, selectedExam, gradeTotal, releaseGate, runAction
  });
  const regradeWorkflow = useRegradeWorkflow({
    canManage, selectedExamId, publishedRelease, grades, runAction, setActioning
  });
  const attendanceWorkflow = useRosterAttendance({ selectedExamId, setRoster, runAction });
  const { openAttendanceEditor } = attendanceWorkflow;

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
      content: `将发布《${selectedExam?.name ?? "未选择考试"}》共 ${gradeTotal} 份成绩，发布后学生成绩即被锁定，如需修改须走成绩申诉流程。确定发布吗？`,
      okText: "发布",
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
          "成绩已发布"
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

  const rosterColumns: TableColumnsType<RosterEntry> = [
    {
      title: "学生",
      key: "student",
      width: 180,
      render: (_value, record) => record.student_name ? (
        <div className="score-roster-student"><strong>{record.student_name}</strong><span>{record.student_no || "无学号"}</span></div>
      ) : <span className="muted">身份待确认</span>
    },
    { title: "班级", dataIndex: "class_name", width: 130, render: (value?: string) => value || "-" },
    { title: "答卷号", dataIndex: "candidate_no", width: 150, render: (value?: string) => value || "-" },
    {
      title: "对账状态",
      dataIndex: "status",
      width: 150,
      render: (value: string) => <StatusTag tone={value === "graded" ? "success" : value === "absent" ? "neutral" : "danger"}>{rosterStatusLabels[value] ?? "未知状态"}</StatusTag>
    },
    {
      title: "核对说明",
      dataIndex: "resolution_code",
      render: (value: string, record) => (
        <div className="score-roster-resolution">
          <span>{rosterResolutionLabels[value] ?? "未知处理方式"}</span>
          {record.expected_page_count > 0 ? <small>{`页数 ${record.actual_page_count}/${record.expected_page_count}`}</small> : null}
          {record.attendance_reason ? <small>{`处置原因：${record.attendance_reason}`}</small> : null}
        </div>
      )
    },
    {
      title: "操作",
      key: "actions",
      width: 130,
      render: (_value, record) => {
        if (!canWrite || publishedOrLocked || !record.student_id || record.key.startsWith("submission:")) return "-";
        if (record.status === "absent") {
          return <Button size="small" icon={<UserCheck size={14} />} onClick={() => openAttendanceEditor(record, "expected")}>恢复应考</Button>;
        }
        if (record.resolution_code === "missing_submission") {
          return <Button size="small" danger icon={<UserX size={14} />} onClick={() => openAttendanceEditor(record, "absent")}>标记缺考</Button>;
        }
        return <span className="muted">请先处理答卷</span>;
      }
    }
  ];

  const columns: TableColumnsType<SubmissionGrade> = [
    { title: "匿名码", dataIndex: "anonymous_code", width: 150 },
    {
      title: "学生姓名",
      dataIndex: "student_id",
      width: 140,
      render: (value?: string) => {
        if (!canReadStudentNames) {
          return <span className="muted">权限受限</span>;
        }
        return value ? identities.students[value]?.name ?? "未匹配" : "未关联";
      }
    },
    {
      title: "班级",
      dataIndex: "student_id",
      width: 140,
      render: (value?: string) => {
        if (!canReadStudentNames) {
          return <span className="muted">权限受限</span>;
        }
        const student = value ? identities.students[value] : undefined;
        return student ? identities.classes[student.class_id]?.name ?? "未匹配" : "未关联";
      }
    },
    {
      title: "总分",
      dataIndex: "total_score",
      width: 120,
      render: (_value: number, record) => (
        <strong>
          {formatScore(record.total_score)} / {formatScore(record.max_score)}
        </strong>
      )
    },
    {
      title: "各题分",
      dataIndex: "items",
      render: (_value: unknown, record) => (
        <div className="score-item-strip">
          {(record.items ?? []).length > 0 ? (
            (record.items ?? []).map((item) => (
              <span key={item.id} title={`${sourceLabels[item.source] ?? "其他来源"} · ${statusLabels[item.status] ?? "未知状态"}`}>
                {item.question_no}: {formatScore(item.score)}/{formatScore(item.max_score)}
              </span>
            ))
          ) : (
            <span>无题目分</span>
          )}
        </div>
      )
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 120,
      render: (value: string) => (
        <span title={value}>
          <StatusTag tone={statusTone(value)}>{statusLabels[value] ?? "未知状态"}</StatusTag>
        </span>
      )
    },
    {
      title: "锁定",
      dataIndex: "locked",
      width: 88,
      render: (value: boolean) => <StatusTag tone={value ? "success" : "neutral"}>{value ? "已锁定" : "未锁定"}</StatusTag>
    }
  ];

  const statusOptions = useMemo(() => {
    const statuses = Array.from(new Set(grades.map((grade) => grade.status)));
    return [{ label: "全部状态", value: "all" }, ...statuses.map((status) => ({ label: statusLabels[status] ?? "未知状态", value: status }))];
  }, [grades]);

  return (
    <div className={mode === "teacher" ? "score-shell read-only" : "score-shell"}>
      <section className="score-topbar">
        <div>
          <Space>
            <h1>{mode === "teacher" ? "班级成绩" : "成绩发布"}</h1>
          </Space>
          <p>{mode === "teacher" ? "查看当前授权考试的班级成绩与阅卷完成情况。" : "先处理发布前检查发现的问题，检查无误后确认并发布成绩。"}</p>
        </div>
        <Space wrap>
          <Select
            className="score-exam-select"
            loading={loadingExams}
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

      {identities.error ? <Alert type="warning" showIcon message="学生身份信息读取不完整" description={identities.error} /> : null}

      {error ? <ErrorState message={error} onRetry={() => void refresh()} /> : null}

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
          <span>{mode === "teacher" ? "当前状态" : publishedOrLocked ? "发布状态" : "是否可发布"}</span>
          <StatusTag tone={mode === "teacher" ? "neutral" : publishedOrLocked || summary.canPublish ? "success" : "danger"}>{mode === "teacher" ? (selectedExam ? examStatusLabels[selectedExam.status] ?? "未知状态" : "未选择") : publishedOrLocked ? "已发布" : summary.canPublish ? "可发布" : "不可发布"}</StatusTag>
        </div>
      </section>

      <section className={mode === "teacher" ? "score-workspace read-only" : "score-workspace"}>
        <main className="score-main">
          <section className="score-table-panel score-roster-panel">
            <div className="panel-head">
              <div>
                <h2>名册对账</h2>
                <p>以本场考试关联班级为应考名单；未交卷、身份冲突和缺页未解决时不能发布成绩。</p>
              </div>
              <StatusTag tone={(roster?.summary.unresolved ?? 0) === 0 ? "success" : "danger"}>
                {(roster?.summary.unresolved ?? 0) === 0 ? "对账完成" : `${roster?.summary.unresolved ?? 0} 项待处理`}
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
                <p>{selectedExam ? selectedExam.name : "未选择考试"}</p>
              </div>
              {quality ? (
                quality.quality.passed ? (
                  <StatusTag tone="success">检查通过</StatusTag>
                ) : (
                  <StatusTag tone="danger">{`${quality.quality.issues.filter((issue) => issue.blocking).length} 项须处理`}</StatusTag>
                )
              ) : null}
            </div>
            {loadingScores ? <LoadingState label="正在读取质量检查" /> : <QualityPanel quality={quality} publishedOrLocked={publishedOrLocked} />}
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
                locale={{ emptyText: <Empty description={mode === "teacher" ? "该考试暂无成绩，请等待阅卷完成。" : "该考试暂无成绩。请先完成阅卷，再点击『汇总最终成绩』生成成绩。"} image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
              />
            )}
            {!loadingScores && gradesHaveMore ? (
              <div className="load-more-row">
                <Button loading={loadingMoreGrades} onClick={() => void loadMoreGrades()}>加载更多成绩</Button>
              </div>
            ) : null}
          </section>
        </main>

        {mode === "admin" ? <aside className="score-actions-panel">
          <div className="panel-head">
            <div>
              <h2>发布步骤</h2>
              <p>{publishedOrLocked ? "成绩已锁定" : "按顺序完成汇总、确认、发布"}</p>
            </div>
            {publishedOrLocked ? <LockKeyhole size={18} /> : <Calculator size={18} />}
          </div>

          {publishedOrLocked ? (
            <Alert type="success" showIcon message="成绩已发布并锁定" description="成绩已发布并锁定，不能直接修改。如需调整，请通过成绩申诉流程处理。" />
          ) : null}

          <div className="score-step-group">
            <span className="score-step-title">第 1 步 · 汇总最终成绩</span>
            <Button block icon={<Calculator size={16} />} disabled={!canWrite || !selectedExamId || publishedOrLocked} loading={actioning === "finalize"} onClick={() => void finalize()}>
              汇总最终成绩
            </Button>
          </div>

          <div className="score-step-group">
            <span className="score-step-title">第 2 步 · 确认成绩</span>
            <label className="score-step-label" htmlFor="score-confirm-reason">确认原因（必填）</label>
            <Input.TextArea id="score-confirm-reason" rows={3} value={confirmReason} placeholder="例如：已由学科组长复核，成绩无误" onChange={(event) => setConfirmReason(event.target.value)} />
            <Button block icon={<CheckCircle2 size={16} />} disabled={!canWrite || gradeTotal === 0 || publishedOrLocked} loading={actioning === "confirm"} onClick={() => void confirmGrades()}>
              确认成绩
            </Button>
          </div>

          <div className="score-step-group">
            <span className="score-step-title">第 3 步 · 发布成绩</span>
            <label className="score-step-label" htmlFor="score-publish-reason">发布原因（必填）</label>
            <Input.TextArea id="score-publish-reason" rows={3} value={publishReason} placeholder="例如：经教务处审批，同意发布" onChange={(event) => setPublishReason(event.target.value)} />
            <Button block type="primary" icon={<Send size={16} />} disabled={!canWrite || !quality?.can_publish || publishedOrLocked} loading={actioning === "publish"} onClick={publish}>
              发布成绩
            </Button>
          </div>

          <Divider className="score-step-divider" />

          <div className="score-step-group">
            <span className="score-step-title">导出</span>
            <Button block icon={<Download size={16} />} disabled={!canWrite || gradeTotal === 0} loading={actioning === "export"} onClick={exportGrades}>
              导出成绩
            </Button>
          </div>

          <details className="score-advanced-details">
            <summary>操作记录与导出水印</summary>
          <Alert
            type="info"
            showIcon
            icon={<FileWarning size={18} />}
            message="导出记录与水印"
            description={lastWatermark ? `最近一次导出的水印编号：${lastWatermark}` : "每次导出都会记录操作人和时间，导出文件自带可追溯水印。"}
          />

          <AuditPanel canRead={canReadAudit} logs={scoreAuditLogs} />
          </details>
        </aside> : null}
      </section>

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
      /> : null}

      <ScoreWorkflowModals
        selectedExam={selectedExam}
        gradeTotal={gradeTotal}
        publishedReleaseExists={Boolean(publishedRelease)}
        actioning={actioning}
        attendanceWorkflow={attendanceWorkflow}
        releaseWorkflow={releaseWorkflow}
        regradeWorkflow={regradeWorkflow}
      />
    </div>
  );
}
