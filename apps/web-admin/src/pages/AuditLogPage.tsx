import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, DatePicker, Descriptions, Drawer, Input, Select, Space, Tooltip, type TableColumnsType } from "antd";
import { Download, FileWarning, LockKeyhole, RefreshCw, Search, ShieldCheck } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { exportAuditLogs, listAuditLogs, type AuditLog, type AuditLogFilter } from "../api/audit";
import { listExams, type Exam } from "../api/exams";
import { listManagedUsers, type ManagedUser } from "../api/users";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { examSubjectLabel } from "../constants/examStatus";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";

interface AuditLogPageProps {
  canRead: boolean;
  canExport: boolean;
  tenantName: string;
}

const sensitiveKeys = ["password", "token", "authorization", "secret", "credential", "access_token", "refresh_token", "password_hash"];

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn("操作记录请求失败", error.status, error.code);
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

function isSensitiveKey(key: string) {
  const normalized = key.toLowerCase();
  return sensitiveKeys.some((item) => normalized.includes(item));
}

// 展示前递归遮盖已知敏感键和常见凭据格式；这是界面兜底，不能替代服务端日志脱敏。
function maskSensitive(value: unknown, key = ""): unknown {
  if (isSensitiveKey(key)) {
    return "***";
  }
  if (Array.isArray(value)) {
    return value.map((item) => maskSensitive(item));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([entryKey, entryValue]) => [entryKey, maskSensitive(entryValue, entryKey)]));
  }
  if (typeof value === "string" && /(bearer\s+|password=|token=)/i.test(value)) {
    return value.replace(/(bearer\s+)[^\s]+/i, "$1***").replace(/(password|token)=([^&\s]+)/gi, "$1=***");
  }
  return value;
}

function maskedJSON(value?: Record<string, unknown>) {
  if (!value || Object.keys(value).length === 0) {
    return "未记录";
  }
  return JSON.stringify(maskSensitive(value), null, 2);
}

function maskText(value?: string) {
  if (!value) {
    return "-";
  }
  return String(maskSensitive(value));
}

function actionTone(action: string) {
  if (action.includes("failed") || action.includes("rejected")) {
    return "danger" as const;
  }
  if (action.includes("export") || action.includes("publish") || action.includes("adjusted")) {
    return "warning" as const;
  }
  if (action.includes("created") || action.includes("succeeded") || action.includes("confirmed")) {
    return "success" as const;
  }
  return "processing" as const;
}

const targetTypeLabels: Record<string, string> = {
  exam: "考试",
  submission: "答卷",
  submission_page: "答卷页",
  answer_segment: "作答区域",
  question: "题目",
  paper: "试卷",
  paper_template: "答题卡模板",
  rubric: "评分细则",
  review_task: "阅卷任务",
  arbitration_task: "仲裁任务",
  double_mark_session: "双评",
  appeal: "申诉",
  scoring_run: "评分批次",
  ocr_task: "识别任务",
  agent_worker_task: "自动处理任务",
  ai_grade: "智能评分",
  final_grade: "最终成绩",
  deduction_point: "扣分点",
  score: "成绩",
  report: "学情报告",
  file: "文件",
  audit_log: "操作记录",
  user: "用户",
  student: "学生",
  school: "机构",
  grade: "年级",
  class: "班级",
  tenant: "机构"
};

function targetTypeLabel(targetType?: string) {
  if (!targetType) {
    return "系统";
  }
  return targetTypeLabels[targetType] ?? "其他对象";
}

function describeTarget(record: AuditLog) {
  const typeLabel = targetTypeLabel(record.target_type);
  return record.target_id ? `${typeLabel}（编号 ${record.target_id.slice(0, 8)}）` : typeLabel;
}

const actionLabels: Record<string, string> = {
  "auth.login_succeeded": "登录成功",
  "auth.login_failed": "登录失败",
  "auth.login_rate_limited": "登录尝试过于频繁被拦截",
  "auth.logout": "退出登录",
  "auth.user_created": "创建用户",
  "user.updated": "更新用户信息",
  "org.tenant_created": "开通机构",
  "org.tenant_updated": "更新机构信息",
  "org.school_created": "创建机构",
  "org.grade_created": "创建年级",
  "org.class_created": "创建班级",
  "org.student_created": "添加学生",
  "org.student_updated": "更新学生信息",
  "org.students_imported": "导入学生",
  "org.teacher_bound_to_class": "设置教师任教班级",
  "exam.created": "创建考试",
  "exam.updated": "更新考试",
  "exam.status_changed": "变更考试阶段",
  "exam.readiness_confirmed": "确认考试准备就绪",
  "exam.collection_started": "开始采集答卷",
  "exam.archived": "归档考试",
	"exam.answer_sheet_template_bound": "绑定本场答题卡模板",
	"exam.answer_sheet_template_unbound": "解除本场答题卡模板",
	"page.template_matched": "自动识别答题卡模板",
	"page.template_match_failed": "答题卡模板识别失败",
  "paper.created": "创建试卷",
  "paper.config_validated": "校验试卷配置",
  "paper.template_created": "创建答题卡模板",
  "paper.template_updated": "更新答题卡模板",
  "paper.template_cloned": "复制答题卡模板",
  "paper.template_locked": "锁定答题卡模板",
  "paper.question_created": "创建题目",
  "paper.question_updated": "更新题目",
  "paper.question_deleted": "删除题目",
  "paper.rubric_created": "创建评分细则",
  "submission.created": "登记答卷",
  "submission.status_changed": "答卷状态变更",
  "submission.page_added": "补充答卷页",
  "submission.page_replaced": "替换答卷页",
  "submission.quality_checked": "答卷质量检查",
  "submission.pages_processing_started": "开始处理答卷页",
  "ocr.task_created": "创建识别任务",
  "ocr.task_started": "开始文字识别",
  "ocr.task_completed": "文字识别完成",
  "ocr.task_failed": "文字识别失败",
  "grading.scoring_run_started": "开始自动评分",
  "grading.scoring_run_cancelled": "取消自动评分",
  "grading.scoring_run_retry_failed": "重试评分失败项",
  "grading.segment_score_reprocessed": "重新评分",
  "grading.scoring_rule_created": "创建评分规则",
  "grading.scoring_rule_updated": "更新评分规则",
  "grading.scoring_rule_published": "发布评分规则",
  "grading.rule_grade_created": "生成规则评分",
  "grading.answer_recorded": "登记作答内容",
  "grading.omr_completed": "填涂识别完成",
  "grading.omr_failed": "填涂识别失败",
  "grading.omr_calibration_created": "创建填涂识别校准",
  "grading.omr_calibration_case_labeled": "标注填涂校准样例",
  "grading.omr_calibration_approved": "通过填涂识别校准",
  "grading.omr_calibration_revoked": "撤销填涂识别校准",
  "grading.omr_calibration_discarded": "废弃填涂识别校准",
  "review.draft_saved": "保存阅卷草稿",
  "review.human_grade_submitted": "提交人工评分",
  "review.task_created": "创建阅卷任务",
  "review.task_assigned": "分配阅卷任务",
  "review.tasks_batch_assigned": "批量分配阅卷任务",
  "review.task_claimed": "领取阅卷任务",
  "review.task_returned": "退回阅卷任务",
  "review.task_released": "释放阅卷任务",
  "review.double_mark_policy_set": "设置双评策略",
  "review.double_mark_session_created": "创建双评",
  "review.double_mark_auto_finalized": "双评自动定分",
  "arbitration.task_created": "创建仲裁任务",
  "arbitration.task_assigned": "分配仲裁任务",
  "arbitration.submitted": "提交仲裁结果",
  "appeal.created": "提交申诉",
  "appeal.assigned": "分配申诉处理人",
  "appeal.teacher_recommendation_submitted": "提交申诉处理建议",
  "appeal.reviewed": "复核申诉",
  "appeal.score_adjusted": "申诉调分",
  "appeal.closed": "关闭申诉",
  "final_grade.created": "生成最终成绩",
  "score.confirmed": "确认成绩",
  "score.finalized": "成绩定稿",
  "score.published": "发布考试成绩",
  "score.exported": "导出成绩",
  "report.generated": "生成学情报告",
  "report.exported": "导出学情报告",
  "file.uploaded": "上传文件",
  "file.downloaded": "下载答卷文件",
  "file.deleted": "删除文件",
  "audit.exported": "导出操作记录"
};

function actionLabel(action: string) {
  return actionLabels[action] ?? "其他操作";
}

export function AuditLogPage({ canRead, canExport, tenantName }: AuditLogPageProps) {
  const { message, modal } = App.useApp();
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [exams, setExams] = useState<Exam[]>([]);
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [usersFailed, setUsersFailed] = useState(false);
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [actionFilter, setActionFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [examFilter, setExamFilter] = useState("");
  const [createdFrom, setCreatedFrom] = useState("");
  const [createdTo, setCreatedTo] = useState("");
  const [keyword, setKeyword] = useState("");
  const [lastWatermark, setLastWatermark] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextCursor, setNextCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<AuditLogFilter>({});

  const pendingFilter = useMemo<AuditLogFilter>(
    () => ({
      action: actionFilter.trim() || undefined,
      actor_id: actorFilter.trim() || undefined,
      exam_id: examFilter || undefined,
      created_from: createdFrom || undefined,
      created_to: createdTo || undefined
    }),
    [actionFilter, actorFilter, createdFrom, createdTo, examFilter]
  );

  const actionOptions = useMemo(
    () =>
      Array.from(new Set(logs.map((item) => item.action)))
        .sort()
        .map((action) => ({ value: action, label: actionLabel(action) })),
    [logs]
  );

  const userNameById = useMemo(() => new Map(users.map((item) => [item.id, item.display_name])), [users]);

  const actorName = useCallback(
    (actor?: string) => {
      if (!actor) {
        return "系统自动处理";
      }
      return userNameById.get(actor) ?? `操作人员（ID ${actor.slice(0, 8)}）`;
    },
    [userNameById]
  );

  const filteredLogs = useMemo(() => {
    const text = keyword.trim().toLowerCase();
    if (!text) {
      return logs;
    }
    return logs.filter((item) => {
      const haystack = [item.actor_id, item.action, item.target_type, item.target_id, item.reason, item.ip_address, item.user_agent, item.request_id].join(" ").toLowerCase();
      return haystack.includes(text);
    });
  }, [keyword, logs]);

  const summary = useMemo(
    () => ({
      total: logs.length,
      actors: new Set(logs.map((item) => item.actor_id).filter(Boolean)).size,
      failures: logs.filter((item) => item.action.includes("failed") || item.action.includes("rejected")).length,
      sensitive: logs.filter((item) => item.action.includes("publish") || item.action.includes("export") || item.action.includes("adjust")).length
    }),
    [logs]
  );

  const columns = useMemo<TableColumnsType<AuditLog>>(
    () => [
      { title: "时间", dataIndex: "created_at", width: 170, render: (value: string) => formatTime(value) },
      { title: "操作人员", dataIndex: "actor_id", width: 170, render: (value?: string) => (value ? <span title={value}>{actorName(value)}</span> : actorName(value)) },
      { title: "操作内容", dataIndex: "action", width: 230, render: (value: string) => <span title={value}>{actionLabel(value)}</span> },
      { title: "业务对象", width: 170, render: (_, record) => <span title={record.target_type || undefined}>{targetTypeLabel(record.target_type)}</span> },
      { title: "说明", dataIndex: "reason", ellipsis: true, render: (value?: string) => maskText(value) },
      { title: "结果", dataIndex: "action", width: 90, render: (value: string) => <StatusTag tone={actionTone(value)}>{value.includes("failed") || value.includes("rejected") ? "失败" : "完成"}</StatusTag> }
    ],
    [actorName]
  );

  useEffect(() => {
    let active = true;
    listManagedUsers({ limit: 200 })
      .then((result) => {
        if (active) {
          setUsers(result.users);
        }
      })
      .catch((usersError) => {
        console.warn("操作人列表加载失败", usersError);
        if (active) {
          setUsersFailed(true);
        }
      });
    return () => {
      active = false;
    };
  }, []);

  const loadLogs = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [auditResult, examResult] = await Promise.allSettled([listAuditLogs({ ...filter, limit: 50 }), listExams()]);
      if (auditResult.status === "fulfilled") {
        setLogs(auditResult.value.audit_logs);
        setNextCursor(auditResult.value.next_cursor ?? "");
        setHasMore(Boolean(auditResult.value.has_more));
      } else {
        throw auditResult.reason;
      }
      if (examResult.status === "fulfilled") {
        setExams(examResult.value.exams);
      }
    } catch (currentError) {
      setLogs([]);
      setNextCursor("");
      setHasMore(false);
      setError(formatError(currentError));
    } finally {
      setLoading(false);
    }
  }, [filter]);

  const loadMoreLogs = useCallback(async () => {
    if (!hasMore || !nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const result = await listAuditLogs({ ...filter, limit: 50, cursor: nextCursor });
      setLogs((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        result.audit_logs.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setNextCursor(result.next_cursor ?? "");
      setHasMore(Boolean(result.has_more));
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setLoadingMore(false);
    }
  }, [filter, hasMore, loadingMore, message, nextCursor]);

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  const openDetail = (record: AuditLog) => {
    setSelectedLog(record);
    setDrawerOpen(true);
  };

  const exportLogs = () => {
    modal.confirm({
      title: "导出操作记录",
      content: "将导出当前筛选条件下的操作记录为 CSV 文件。此次导出会被记录在案，文件带有可追溯水印，请妥善保管。",
      okText: "确认导出",
      cancelText: "取消",
      onOk: async () => {
        setExporting(true);
        try {
          // 导出使用已提交的服务端筛选；下方关键字只过滤已加载列表，不改变导出范围。
          const result = await exportAuditLogs({ ...filter, limit: 200 });
          setLastWatermark(result.watermark ?? "");
          saveBlob(result.blob, result.filename ?? "audit-logs.csv");
          message.success("操作记录已导出");
          await loadLogs();
        } catch (currentError) {
          message.error(formatError(currentError));
        } finally {
          setExporting(false);
        }
      }
    });
  };

  return (
    <div className="audit-shell">
      <section className="audit-topbar">
        <div>
          <Space align="center" wrap>
          <h1>操作记录</h1>
          </Space>
          <p>查看谁在什么时间执行了什么操作，异常时再展开技术详情。</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} onClick={loadLogs} loading={loading}>
            刷新
          </Button>
          <Tooltip title={!canExport ? "需要「导出操作记录」权限" : ""}>
            <span>
              <Button type="primary" icon={<Download size={16} />} disabled={!canExport || logs.length === 0} loading={exporting} onClick={exportLogs}>
                导出 CSV
              </Button>
            </span>
          </Tooltip>
        </Space>
      </section>

      {!canRead ? <Alert type="error" showIcon message="没有查看权限" description="当前账号没有「查看操作记录」的权限，请联系系统管理员开通。" /> : null}
      {error ? <ErrorState message={error} onRetry={loadLogs} /> : null}

      <section className="audit-scope-panel">
        <LockKeyhole size={18} />
        <span>数据范围：仅显示 {tenantName} 的操作记录；记录不可修改、不可删除。</span>
      </section>

      <section className="audit-filter-panel">
        <DatePicker.RangePicker
          showTime
          onChange={(dates) => {
            setCreatedFrom(dates?.[0]?.toISOString() ?? "");
            setCreatedTo(dates?.[1]?.toISOString() ?? "");
          }}
        />
        {usersFailed ? (
          <Input placeholder="操作人 ID" value={actorFilter} onChange={(event) => setActorFilter(event.target.value)} allowClear />
        ) : (
          <Select
            allowClear
            showSearch
            placeholder="按操作人筛选"
            optionFilterProp="label"
            value={actorFilter || undefined}
            options={users.map((item) => ({ value: item.id, label: item.display_name }))}
            onChange={(value) => setActorFilter(value ?? "")}
          />
        )}
        <Select allowClear placeholder="操作类型" value={actionFilter || undefined} options={actionOptions} onChange={(value) => setActionFilter(value ?? "")} />
        <Select
          allowClear
          placeholder="考试"
          value={examFilter || undefined}
          options={exams.map((exam) => ({ value: exam.id, label: `${exam.name} · ${examSubjectLabel(exam.subject)}` }))}
          onChange={(value) => setExamFilter(value ?? "")}
        />
        <Button icon={<Search size={16} />} onClick={() => setFilter(pendingFilter)} loading={loading}>
          查询
        </Button>
      </section>

      <section className="audit-summary-strip">
        <div>
          <span>本次查询</span>
          <strong>{summary.total}</strong>
        </div>
        <div>
          <span>涉及人员</span>
          <strong>{summary.actors}</strong>
        </div>
        <div>
          <span>异常操作</span>
          <strong>{summary.failures}</strong>
        </div>
        <div>
          <span>敏感操作</span>
          <strong>{summary.sensitive}</strong>
        </div>
      </section>

      <section className="audit-table-panel">
        <div className="panel-head">
          <div>
            <h2>操作记录</h2>
            <p>{filteredLogs.length} / {logs.length} 条记录，点击行查看详情。</p>
          </div>
          <Input className="audit-keyword" prefix={<Search size={16} />} placeholder="搜索操作内容或说明" value={keyword} onChange={(event) => setKeyword(event.target.value)} allowClear />
        </div>
        {loading ? (
          <LoadingState label="正在读取操作记录" />
        ) : (
          <>
            <ResponsiveTable
              className="dense-data-table"
              rowKey="id"
              size="small"
              columns={columns}
              dataSource={filteredLogs}
              pagination={{ pageSize: 12 }}
              onRow={(record) => ({ onClick: () => openDetail(record) })}
              locale={{ emptyText: <EmptyState title="暂无操作记录" description="当前筛选条件下没有操作记录，可放宽时间范围后重试。" /> }}
            />
            {hasMore ? (
              <Button block loading={loadingMore} onClick={() => void loadMoreLogs()}>
                加载更多操作记录
              </Button>
            ) : null}
          </>
        )}
      </section>

      <section className="audit-export-panel">
        <ShieldCheck size={18} />
        <span>{lastWatermark ? `最近一次导出已记录（水印编号 ${lastWatermark.length > 12 ? `${lastWatermark.slice(0, 12)}…` : lastWatermark}）` : "每次导出都会自动记录在操作日志中"}</span>
      </section>

      <Drawer title="记录详情" width={520} open={drawerOpen} onClose={() => setDrawerOpen(false)}>
        {selectedLog ? (
          <div className="audit-detail-stack">
            <Descriptions size="small" column={1}>
              <Descriptions.Item label="操作人">{selectedLog.actor_id ? <span title={selectedLog.actor_id}>{actorName(selectedLog.actor_id)}</span> : actorName(selectedLog.actor_id)}</Descriptions.Item>
              <Descriptions.Item label="操作内容"><span title={selectedLog.action}>{actionLabel(selectedLog.action)}</span></Descriptions.Item>
              <Descriptions.Item label="操作对象"><span title={selectedLog.target_id ? `${selectedLog.target_type}:${selectedLog.target_id}` : selectedLog.target_type}>{describeTarget(selectedLog)}</span></Descriptions.Item>
              <Descriptions.Item label="说明">{maskText(selectedLog.reason)}</Descriptions.Item>
              <Descriptions.Item label="IP 地址">{selectedLog.ip_address || "-"}</Descriptions.Item>
              <Descriptions.Item label="浏览器">{maskText(selectedLog.user_agent)}</Descriptions.Item>
              <Descriptions.Item label="时间">{formatTime(selectedLog.created_at)}</Descriptions.Item>
              <Descriptions.Item label="请求编号">{selectedLog.request_id || "-"}</Descriptions.Item>
            </Descriptions>
            <details className="audit-json-block">
              <summary><strong>变更前数据</strong></summary>
              <pre>{maskedJSON(selectedLog.before_value)}</pre>
            </details>
            <details className="audit-json-block">
              <summary><strong>变更后数据</strong></summary>
              <pre>{maskedJSON(selectedLog.after_value)}</pre>
            </details>
            <Alert
              type="info"
              showIcon
              icon={<FileWarning size={18} />}
              message="只读记录"
              description="操作记录不可修改、不可删除；密码等敏感内容显示时已自动隐藏。"
            />
          </div>
        ) : (
          <EmptyState title="未选择记录" description="请选择一条操作记录查看详情。" />
        )}
      </Drawer>
    </div>
  );
}
