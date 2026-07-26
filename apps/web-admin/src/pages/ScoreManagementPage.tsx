import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Empty, Input, List, Progress, Select, Space, Table, Tabs, type TableColumnsType } from "antd";
import { Calculator, CheckCircle2, Download, FileWarning, LockKeyhole, RefreshCw, Search, Send, ShieldCheck } from "lucide-react";
import { ApiClientError } from "../api/client";
import { listAuditLogs, type AuditLog } from "../api/audit";
import { listExams, type Exam } from "../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../api/org";
import {
  checkExamGradeQuality,
  confirmExamGrades,
  exportExamGrades,
  finalizeExamGrades,
  listExamGrades,
  publishExamGrades,
  type QualityCheckResult,
  type QualityIssue,
  type SubmissionGrade
} from "../api/scores";
import { listSubmissions, type Submission } from "../api/submissions";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import type { StatusTone } from "../types";
import type { ProductExperience } from "../router/experience";

interface IdentityMaps {
  students: Record<string, Student>;
  classes: Record<string, SchoolClass>;
  error?: string;
}

interface ScoreSummary {
  totalSubmissions: number;
  completedGrades: number;
  unfinishedReviews: number;
  pendingArbitrations: number;
  ocrFailures: number;
  canPublish: boolean;
}

const statusLabels: Record<string, string> = {
  calculating: "计算中",
  pending_confirmation: "待确认",
  confirmed: "已确认",
  pending_publish: "待发布",
  published: "已发布",
  locked: "已锁定"
};

const sourceLabels: Record<string, string> = {
  single_review: "人工单评",
  rule_auto: "规则自动",
  arbitration: "仲裁",
  average: "双评平均",
  first: "第一评",
  second: "第二评",
  higher: "高分优先",
  lower: "低分优先"
};

const qualityLabels: Record<string, string> = {
  unfinished_review_tasks: "未完成阅卷",
  unfinished_arbitration_tasks: "未完成仲裁",
  ocr_failed_unhandled: "OCR 失败",
  missing_final_grades: "缺失最终题目分",
  grades_not_confirmed: "成绩未确认",
  no_submission_grades: "无成绩可发布"
};

const auditActionLabels: Record<string, string> = {
  "score.finalized": "成绩汇总",
  "score.confirmed": "成绩确认",
  "score.published": "成绩发布",
  "score.exported": "成绩导出"
};

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    return `${error.status} ${error.code}: ${error.message}`;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "未知错误";
}

function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "-";
  }
  return Number(value.toFixed(1)).toString();
}

function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function statusTone(status: string): StatusTone {
  if (status === "published" || status === "locked") {
    return "success";
  }
  if (status === "confirmed" || status === "pending_publish") {
    return "processing";
  }
  if (status === "pending_confirmation") {
    return "warning";
  }
  return "neutral";
}

function issueCount(quality: QualityCheckResult | null, code: string) {
  return quality?.quality.issues.find((issue) => issue.code === code)?.count ?? 0;
}

function createSummary(submissions: Submission[], grades: SubmissionGrade[], quality: QualityCheckResult | null): ScoreSummary {
  return {
    totalSubmissions: submissions.length,
    completedGrades: grades.length,
    unfinishedReviews: issueCount(quality, "unfinished_review_tasks"),
    pendingArbitrations: issueCount(quality, "unfinished_arbitration_tasks"),
    ocrFailures: issueCount(quality, "ocr_failed_unhandled"),
    canPublish: Boolean(quality?.can_publish)
  };
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

async function loadIdentities(canReadStudentNames: boolean): Promise<IdentityMaps> {
  if (!canReadStudentNames) {
    return { students: {}, classes: {} };
  }
  const [studentsResult, classesResult] = await Promise.allSettled([listStudents(), listClasses()]);
  const maps: IdentityMaps = { students: {}, classes: {} };
  if (studentsResult.status === "fulfilled") {
    for (const student of studentsResult.value.students) {
      maps.students[student.id] = student;
    }
  } else {
    maps.error = formatError(studentsResult.reason);
  }
  if (classesResult.status === "fulfilled") {
    for (const item of classesResult.value.classes) {
      maps.classes[item.id] = item;
    }
  } else {
    maps.error = [maps.error, formatError(classesResult.reason)].filter(Boolean).join("；");
  }
  return maps;
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
  const hasSession = true;
  const canWrite = canManage && hasSession;
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState(initialExamId);
  const [submissions, setSubmissions] = useState<Submission[]>([]);
  const [grades, setGrades] = useState<SubmissionGrade[]>([]);
  const [quality, setQuality] = useState<QualityCheckResult | null>(null);
  const [identities, setIdentities] = useState<IdentityMaps>({ students: {}, classes: {} });
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [lastWatermark, setLastWatermark] = useState("");
  const [keyword, setKeyword] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [confirmReason, setConfirmReason] = useState("checked by subject lead");
  const [publishReason, setPublishReason] = useState("approved for release");
  const [loadingExams, setLoadingExams] = useState(true);
  const [loadingScores, setLoadingScores] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [actioning, setActioning] = useState<string | null>(null);

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const summary = useMemo(() => createSummary(submissions, grades, quality), [grades, quality, submissions]);
  const scoreAuditLogs = useMemo(() => auditLogs.filter((item) => item.action.startsWith("score.")), [auditLogs]);
  const publishedOrLocked = selectedExam?.status === "published" || (grades.length > 0 && grades.every((grade) => grade.locked || grade.status === "published" || grade.status === "locked"));

  const filteredGrades = useMemo(() => {
    const text = keyword.trim().toLowerCase();
    return grades.filter((grade) => {
      const student = grade.student_id ? identities.students[grade.student_id] : undefined;
      const className = student ? identities.classes[student.class_id]?.name : "";
      const statusMatched = statusFilter === "all" || grade.status === statusFilter;
      const keywordMatched =
        !text ||
        grade.anonymous_code.toLowerCase().includes(text) ||
        grade.submission_id.toLowerCase().includes(text) ||
        (student?.name ?? "").toLowerCase().includes(text) ||
        (student?.student_no ?? "").toLowerCase().includes(text) ||
        className.toLowerCase().includes(text);
      return statusMatched && keywordMatched;
    });
  }, [grades, identities.classes, identities.students, keyword, statusFilter]);

  const loadExamList = useCallback(async () => {
    setLoadingExams(true);
    setError(null);
    if (!hasSession) {
      setExams([]);
      setSelectedExamId("");
      setError("当前没有有效登录会话，无法调用真实后端 API。");
      setLoadingExams(false);
      return;
    }
    try {
      const result = await listExams();
      setExams(result.exams);
      setSelectedExamId((current) => current || result.exams[0]?.id || "");
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoadingExams(false);
    }
  }, [hasSession]);

  const loadScores = useCallback(
    async (examId: string) => {
      if (!examId || !hasSession) {
        setSubmissions([]);
        setGrades([]);
        setQuality(null);
        setAuditLogs([]);
        return;
      }
      setLoadingScores(true);
      setError(null);
      try {
        const [submissionResult, gradeResult, qualityResult, identityResult, auditResult] = await Promise.allSettled([
          listSubmissions(examId),
          listExamGrades(examId),
          checkExamGradeQuality(examId, "publish"),
          loadIdentities(canReadStudentNames),
          canReadAudit ? listAuditLogs({ target_type: "exam", target_id: examId, limit: 20 }) : Promise.resolve({ audit_logs: [] })
        ]);
        if (submissionResult.status === "fulfilled") {
          setSubmissions(submissionResult.value.submissions);
        } else {
          throw submissionResult.reason;
        }
        if (gradeResult.status === "fulfilled") {
          setGrades(gradeResult.value.grades);
        } else {
          throw gradeResult.reason;
        }
        if (qualityResult.status === "fulfilled") {
          setQuality(qualityResult.value);
        } else {
          throw qualityResult.reason;
        }
        if (identityResult.status === "fulfilled") {
          setIdentities(identityResult.value);
        }
        if (auditResult.status === "fulfilled") {
          setAuditLogs(auditResult.value.audit_logs);
        }
      } catch (currentError) {
        setError(formatError(currentError));
      } finally {
        setLoadingScores(false);
      }
    },
    [canReadAudit, canReadStudentNames, hasSession]
  );

  useEffect(() => {
    void loadExamList();
  }, [loadExamList]);

  useEffect(() => {
    void loadScores(selectedExamId);
  }, [loadScores, selectedExamId]);

  const refresh = async () => {
    await loadExamList();
    if (selectedExamId) {
      await loadScores(selectedExamId);
    }
  };

  const runAction = async (key: string, action: () => Promise<void>, successText: string) => {
    setActioning(key);
    try {
      await action();
      message.success(successText);
      if (selectedExamId) {
        await loadScores(selectedExamId);
      }
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
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
      content: "发布后成绩会锁定，后续修改必须走申诉或改分留痕流程。",
      okText: "发布",
      cancelText: "取消",
      onOk: () =>
        runAction(
          "publish",
          async () => {
            await publishExamGrades(selectedExamId, publishReason.trim());
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
      title: "导出成绩 CSV",
      content: "导出动作会写入审计日志，并在 CSV 响应头中附带水印信息。",
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
          "成绩 CSV 已导出"
        )
    });
  };

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
              <span key={item.id} title={`${sourceLabels[item.source] ?? item.source} · ${item.status}`}>
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
      render: (value: string) => <StatusTag tone={statusTone(value)}>{statusLabels[value] ?? value}</StatusTag>
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
    return [{ label: "全部状态", value: "all" }, ...statuses.map((status) => ({ label: statusLabels[status] ?? status, value: status }))];
  }, [grades]);

  const renderQuality = () => {
    if (!quality) {
      return <EmptyState title="暂无质量检查" description="选择考试后会读取发布前质量检查。" />;
    }
    if (quality.quality.passed) {
      return (
        <div className="score-quality-pass">
          <ShieldCheck size={22} />
          <div>
            <strong>{publishedOrLocked ? "成绩已发布，质量校验通过" : "发布前质量检查通过"}</strong>
            <span>{publishedOrLocked ? "成绩已发布并锁定，发布前阻断项均已处理。" : "当前成绩已满足发布闸门。"}</span>
          </div>
        </div>
      );
    }
    return (
      <List
        size="small"
        dataSource={quality.quality.issues}
        locale={{ emptyText: <Empty description="暂无质量问题" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
        renderItem={(issue: QualityIssue) => (
          <List.Item>
            <div className="score-quality-issue">
              <StatusTag tone={issue.blocking ? "danger" : "warning"}>{issue.blocking ? "阻断" : "提示"}</StatusTag>
              <strong>{qualityLabels[issue.code] ?? issue.code}</strong>
              <span>{issue.count} 项</span>
              <small>{issue.message}</small>
            </div>
          </List.Item>
        )}
      />
    );
  };

  const renderAudit = () => {
    if (!canReadAudit) {
      return <EmptyState title="无审计读取权限" description="导出、确认和发布仍会写审计；当前用户不能读取审计日志。" />;
    }
    if (scoreAuditLogs.length === 0) {
      return <EmptyState title="暂无审计记录" description="当前考试尚未返回匹配的 score 审计动作。" />;
    }
    return (
      <List
        size="small"
        dataSource={scoreAuditLogs.slice(0, 8)}
        renderItem={(item) => (
          <List.Item>
            <div className="score-audit-row">
              <strong>{auditActionLabels[item.action] ?? item.action}</strong>
              <span>{item.reason || item.target_type}</span>
              <small>{formatTime(item.created_at)}</small>
            </div>
          </List.Item>
        )}
      />
    );
  };

  return (
    <div className={mode === "teacher" ? "score-shell read-only" : "score-shell"}>
      <section className="score-topbar">
        <div>
          <Space>
            <h1>{mode === "teacher" ? "班级成绩" : "成绩发布"}</h1>
          </Space>
          <p>{mode === "teacher" ? "查看当前授权考试的班级成绩与阅卷完成情况。" : "先处理阻断问题，检查无误后确认并发布成绩。"}</p>
        </div>
        <Space wrap>
          <Select
            className="score-exam-select"
            loading={loadingExams}
            value={selectedExamId || undefined}
            placeholder="选择考试"
            options={exams.map((exam) => ({ label: `${exam.name} · ${exam.subject}`, value: exam.id }))}
            onChange={setSelectedExamId}
          />
          <Button icon={<RefreshCw size={16} />} onClick={() => void refresh()} loading={loadingExams || loadingScores}>
            刷新
          </Button>
        </Space>
      </section>

      {!hasSession ? (
        <Alert
          type="warning"
          showIcon
          message="未检测到真实后端访问令牌"
            description="完成成绩汇总、质量检查、确认、发布和留痕导出。"
        />
      ) : null}

      {identities.error ? <Alert type="warning" showIcon message="学生身份信息读取不完整" description={identities.error} /> : null}

      {error ? <ErrorState message={error} onRetry={() => void refresh()} /> : null}

      <section className="score-summary-strip">
        <div>
          <span>参考人数</span>
          <strong>{summary.totalSubmissions}</strong>
        </div>
        <div>
          <span>已完成阅卷</span>
          <strong>{summary.completedGrades}</strong>
        </div>
        <div>
          <span>阅卷未完成</span>
          <strong>{summary.unfinishedReviews}</strong>
        </div>
        <div>
          <span>等待仲裁</span>
          <strong>{summary.pendingArbitrations}</strong>
        </div>
        <div>
          <span>识别失败</span>
          <strong>{summary.ocrFailures}</strong>
        </div>
        <div>
          <span>{mode === "teacher" ? "当前状态" : publishedOrLocked ? "发布状态" : "是否可发布"}</span>
          <StatusTag tone={mode === "teacher" ? "neutral" : publishedOrLocked || summary.canPublish ? "success" : "danger"}>{mode === "teacher" ? (selectedExam ? statusLabels[selectedExam.status] ?? selectedExam.status : "未选择") : publishedOrLocked ? "已发布" : summary.canPublish ? "可发布" : "不可发布"}</StatusTag>
        </div>
      </section>

      <section className={mode === "teacher" ? "score-workspace read-only" : "score-workspace"}>
        <main className="score-main">
          <section className="score-quality-panel">
            <div className="panel-head">
              <div>
                <h2>发布前质量检查</h2>
                <p>{selectedExam ? selectedExam.name : "未选择考试"}</p>
              </div>
              <Progress type="circle" size={58} percent={quality ? (quality.quality.passed ? 100 : Math.max(0, Math.round((1 - quality.quality.issues.length / 6) * 100))) : 0} status={quality?.quality.passed ? "success" : "exception"} format={(percent) => `${percent ?? 0}%`} />
            </div>
            {loadingScores ? <LoadingState label="正在读取质量检查" /> : renderQuality()}
          </section>

          <section className="score-table-panel">
            <div className="panel-head">
              <div>
                <h2>成绩列表</h2>
                <p>{filteredGrades.length} / {grades.length} 条成绩</p>
              </div>
              <Space wrap>
                <Input prefix={<Search size={16} />} placeholder="搜索匿名码、学生、班级" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
                <Select className="toolbar-select" value={statusFilter} options={statusOptions} onChange={setStatusFilter} />
              </Space>
            </div>
            {loadingScores ? (
              <LoadingState label="正在读取成绩" />
            ) : (
              <ResponsiveTable
                rowKey="id"
                size="small"
                columns={columns}
                dataSource={filteredGrades}
                pagination={{ pageSize: 8, showSizeChanger: false }}
                locale={{ emptyText: <Empty description="当前考试没有后端返回的成绩" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
              />
            )}
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
            <Alert type="success" showIcon message="成绩已发布并锁定" description="发布后核心分数不可直接修改，后续调整必须通过申诉或改分留痕流程。" />
          ) : null}

          <Button block icon={<Calculator size={16} />} disabled={!canWrite || !selectedExamId || publishedOrLocked} loading={actioning === "finalize"} onClick={() => void finalize()}>
            汇总最终成绩
          </Button>

          <Input.TextArea rows={3} value={confirmReason} placeholder="确认原因" onChange={(event) => setConfirmReason(event.target.value)} />
          <Button block icon={<CheckCircle2 size={16} />} disabled={!canWrite || grades.length === 0 || publishedOrLocked} loading={actioning === "confirm"} onClick={() => void confirmGrades()}>
            确认成绩
          </Button>

          <Input.TextArea rows={3} value={publishReason} placeholder="发布原因" onChange={(event) => setPublishReason(event.target.value)} />
          <Button block type="primary" icon={<Send size={16} />} disabled={!canWrite || !quality?.can_publish || publishedOrLocked} loading={actioning === "publish"} onClick={publish}>
            发布成绩
          </Button>

          <Button block icon={<Download size={16} />} disabled={!canWrite || grades.length === 0} loading={actioning === "export"} onClick={exportGrades}>
            导出 CSV
          </Button>

          <details className="score-advanced-details">
            <summary>更多与审计</summary>
          <Alert
            type="info"
            showIcon
            icon={<FileWarning size={18} />}
            message="导出审计与水印"
            description={lastWatermark ? `最近导出水印：${lastWatermark}` : "导出成绩会写 score.exported 审计，并在响应头返回 X-EduGrade-Watermark。"}
          />

          <Tabs
            size="small"
            items={[
              { key: "audit", label: "审计", children: renderAudit() },
              {
                key: "items",
                label: "字段",
                children: (
                  <div className="score-field-note">
                    <span>学生姓名和班级只在具备 `org:manage` 权限时读取。</span>
                    <span>CSV 当前为后端真实导出能力，未伪装 Excel。</span>
                  </div>
                )
              }
            ]}
          />
          </details>
        </aside> : null}
      </section>
    </div>
  );
}
