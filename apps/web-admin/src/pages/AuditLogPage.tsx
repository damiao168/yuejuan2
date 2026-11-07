import { Alert, App, Button, DatePicker, Input, Select, Space, Tooltip, type TableColumnsType } from "antd";
import { Download, LockKeyhole, RefreshCw, Search, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { exportAuditLogs, listAuditLogs, type AuditLog, type AuditLogFilter } from "../api/audit";
import { listExams, type Exam } from "../api/exams";
import { listManagedUsers, type ManagedUser } from "../api/users";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { examSubjectLabel } from "../constants/examStatus";
import { actionLabel, actionLabels, actionTone, AuditLogDetailDrawer, auditReasonText, exportWatermarkText, formatError, formatTime, saveBlob, targetTypeLabel } from "./auditLogPresentation";

interface AuditLogPageProps {
  canRead: boolean;
  canExport: boolean;
  tenantName: string;
}

export function AuditLogPage({ canRead, canExport, tenantName }: Readonly<AuditLogPageProps>) {
  const { message, modal } = App.useApp();
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [exams, setExams] = useState<Exam[]>([]);
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [usersFailed, setUsersFailed] = useState(false);
  const [usersRetry, setUsersRetry] = useState(0);
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [actionFilter, setActionFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [examFilter, setExamFilter] = useState("");
  const [createdFrom, setCreatedFrom] = useState("");
  const [createdTo, setCreatedTo] = useState("");
  const [keyword, setKeyword] = useState("");
  const [lastWatermark, setLastWatermark] = useState("");
  const [lastLoadedAt, setLastLoadedAt] = useState("");
  const detailTriggerRef = useRef<HTMLElement | null>(null);
  const queryRevisionRef = useRef(0);
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
      q: keyword.trim() || undefined,
      actor_id: actorFilter.trim() || undefined,
      exam_id: examFilter || undefined,
      created_from: createdFrom || undefined,
      created_to: createdTo || undefined
    }),
    [actionFilter, actorFilter, createdFrom, createdTo, examFilter, keyword]
  );

  const actionOptions = useMemo(
    () => Object.entries(actionLabels).map(([value, label]) => ({ value, label: `${label} · ${value}` })),
    []
  );
  const filtersChanged = JSON.stringify(pendingFilter) !== JSON.stringify(filter);

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
      { title: "说明", dataIndex: "reason", ellipsis: true, render: (value: string | undefined, record: AuditLog) => auditReasonText(record.action, value) },
      { title: "结果", dataIndex: "action", width: 90, render: (value: string) => <StatusTag tone={actionTone(value)}>{value.includes("failed") || value.includes("rejected") ? "失败" : "完成"}</StatusTag> }
    ],
    [actorName]
  );

  useEffect(() => {
    let active = true;
    setUsersFailed(false);
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
  }, [usersRetry]);

  const loadLogs = useCallback(async () => {
    // 新筛选或刷新开始后，旧查询和旧分页只能结束自己的请求，不能再写入当前列表。
    const revision = ++queryRevisionRef.current;
    setLoading(true);
    setLoadingMore(false);
    setError(null);
    try {
      const [auditResult, examResult] = await Promise.allSettled([listAuditLogs({ ...filter, limit: 50 }), listExams()]);
      if (revision !== queryRevisionRef.current) return;
      if (auditResult.status === "fulfilled") {
        setLogs(auditResult.value.audit_logs);
        setNextCursor(auditResult.value.next_cursor ?? "");
        setHasMore(Boolean(auditResult.value.has_more));
        setLastLoadedAt(new Date().toISOString());
      } else {
        throw auditResult.reason;
      }
      if (examResult.status === "fulfilled") {
        setExams(examResult.value.exams);
      }
    } catch (currentError) {
      if (revision !== queryRevisionRef.current) return;
      // 刷新失败时保留上次查询结果；时间标记让用户知道这不是刚取到的数据。
      setError(formatError(currentError));
    } finally {
      if (revision === queryRevisionRef.current) setLoading(false);
    }
  }, [filter]);

  const loadMoreLogs = useCallback(async () => {
    if (!hasMore || !nextCursor || loadingMore || loading || error) return;
    const revision = queryRevisionRef.current;
    setLoadingMore(true);
    try {
      const result = await listAuditLogs({ ...filter, limit: 50, cursor: nextCursor });
      if (revision !== queryRevisionRef.current) return;
      setLogs((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        result.audit_logs.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setNextCursor(result.next_cursor ?? "");
      setHasMore(Boolean(result.has_more));
    } catch (currentError) {
      if (revision === queryRevisionRef.current) message.error(formatError(currentError));
    } finally {
      if (revision === queryRevisionRef.current) setLoadingMore(false);
    }
  }, [error, filter, hasMore, loading, loadingMore, message, nextCursor]);

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  const openDetail = (record: AuditLog, trigger: HTMLElement) => {
    detailTriggerRef.current = trigger;
    setSelectedLog(record);
    setDrawerOpen(true);
  };

  const closeDetail = () => {
    setDrawerOpen(false);
    requestAnimationFrame(() => detailTriggerRef.current?.focus());
  };

  const exportLogs = () => {
    modal.confirm({
      title: "导出操作记录",
      content: "将导出已应用筛选条件（含关键词）下最新的最多 200 条操作记录为 CSV。此次导出会被记录在案，文件带有可追溯水印，请妥善保管。",
      okText: "确认导出",
      cancelText: "取消",
      onOk: async () => {
        setExporting(true);
        try {
          // 列表、后续游标和导出使用同一份已提交筛选；未应用的输入不能改变这次导出。
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
              <Button type="primary" icon={<Download size={16} />} disabled={!canExport || logs.length === 0 || loading || Boolean(error)} loading={exporting} onClick={exportLogs}>
                导出 CSV
              </Button>
            </span>
          </Tooltip>
        </Space>
      </section>

      {!canRead ? <Alert type="error" showIcon message="没有查看权限" description="当前账号没有「查看操作记录」的权限，请联系系统管理员开通。" /> : null}
      {error ? <ErrorState message={error} onRetry={loadLogs} /> : null}
      {error && logs.length > 0 ? <Alert type="warning" showIcon message="当前查询未成功，列表保留上次成功读取的记录；请重试后继续分页或导出。" /> : null}

      <section className="audit-scope-panel">
        <LockKeyhole size={18} />
        <span>数据范围：仅显示 {tenantName} 的操作记录；记录不可修改、不可删除。</span>
      </section>

      <section className="audit-filter-panel">
        <label className="audit-filter-field"><span>关键词</span><Input prefix={<Search size={16} />} placeholder="搜索账号、操作说明或对象编号" aria-label="搜索操作记录" maxLength={128} value={keyword} onChange={event => setKeyword(event.target.value)} onPressEnter={() => setFilter(pendingFilter)} allowClear /></label>
        <label className="audit-filter-field audit-filter-dates">
          <span>时间范围</span>
          <DatePicker.RangePicker
            showTime
            onChange={(dates) => {
              setCreatedFrom(dates?.[0]?.toISOString() ?? "");
              setCreatedTo(dates?.[1]?.toISOString() ?? "");
            }}
          />
        </label>
        <label className="audit-filter-field">
          <span>操作人</span>
          <Select allowClear showSearch disabled={usersFailed} placeholder={usersFailed ? "人员列表暂不可用" : "全部人员"} optionFilterProp="label" value={actorFilter || undefined} options={users.map((item) => ({ value: item.id, label: item.display_name }))} onChange={(value) => setActorFilter(value ?? "")} />
          {usersFailed ? <Button size="small" onClick={() => setUsersRetry(value => value + 1)}>重试人员列表</Button> : null}
        </label>
        <label className="audit-filter-field">
          <span>操作类型</span>
          <Select allowClear showSearch optionFilterProp="label" placeholder="全部类型" value={actionFilter || undefined} options={actionOptions} onChange={(value) => setActionFilter(value ?? "")} />
        </label>
        <label className="audit-filter-field">
          <span>考试</span>
          <Select allowClear showSearch optionFilterProp="label" placeholder="全部考试" value={examFilter || undefined} options={exams.map((exam) => ({ value: exam.id, label: `${exam.name} · ${examSubjectLabel(exam.subject)}` }))} onChange={(value) => setExamFilter(value ?? "")} />
        </label>
        <Button className="audit-apply-filter" type="primary" icon={<Search size={16} />} onClick={() => setFilter(pendingFilter)} loading={loading}>应用筛选</Button>
        {filtersChanged ? <p className="audit-filter-pending">筛选条件尚未应用，列表与导出仍按上次查询。</p> : null}
      </section>

      <section className="audit-summary-strip">
        <div>
          <span>已加载记录</span>
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
            <p>当前已加载 {logs.length} 条{hasMore ? "，还有更多可加载" : ""}。{lastLoadedAt ? `最近成功读取：${formatTime(lastLoadedAt)}` : ""}</p>
          </div>
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
              dataSource={logs}
              pagination={false}
              onRow={(record) => ({
                tabIndex: 0,
                onClick: (event) => openDetail(record, event.currentTarget),
                onKeyDown: (event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    openDetail(record, event.currentTarget);
                  }
                }
              })}
              locale={{ emptyText: <EmptyState title="暂无操作记录" description="当前筛选条件下没有操作记录，可放宽时间范围后重试。" /> }}
            />
            {hasMore && !error ? (
              <Button block loading={loadingMore} onClick={() => void loadMoreLogs()}>
                加载更多操作记录
              </Button>
            ) : null}
          </>
        )}
      </section>

      <section className="audit-export-panel">
        <ShieldCheck size={18} />
        <span>{exportWatermarkText(lastWatermark)}</span>
      </section>

      <AuditLogDetailDrawer selectedLog={selectedLog} open={drawerOpen} onClose={closeDetail} actorName={actorName} />
    </div>
  );
}

export { auditReasonText } from "./auditLogPresentation";
