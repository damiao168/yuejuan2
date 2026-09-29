import { useCallback, useEffect, useState } from "react";
import { Alert, App, Button, Descriptions, Input, Modal, Select, Space, type TableColumnsType } from "antd";
import { RefreshCw } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import {
  assignProcessingException,
  getProcessingSummary,
  listProcessingExceptions,
  resolveProcessingException,
  retryProcessingException,
  type ProcessingException,
  type ProcessingExceptionSeverity,
  type ProcessingExceptionStatus,
  type ProcessingSummary
} from "../api/processing";
import { EmptyState } from "./PageState";
import { ResponsiveTable } from "./ResponsiveTable";
import { StatusTag } from "./StatusTag";
import { hashQueryParam } from "../router/query";
import type { StatusTone } from "../types";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn(`API 请求失败 ${error.status} ${error.code}`, error);
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}
function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

const processingStageLabels: Record<string, string> = {
  RECEIVED: "已接收",
  VALIDATED: "已校验",
  QUALITY_CHECKED: "质量检查",
  REGISTERED: "版面校正",
  IDENTIFIED: "身份识别",
  PARSED: "内容识别",
  SEGMENTED: "题目切分",
  READY: "可阅卷"
};

const processingIssueLabels: Record<string, string> = {
  BLOCKED_MISSING_IDENTITY: "未识别到考生信息",
  BLOCKED_MISSING_PAGE: "答题卡页数不完整",
  BLOCKED_BAD_ALIGNMENT: "版面校正失败",
  BLOCKED_LOW_IMAGE_QUALITY: "图像质量不达标",
  OCR_LOW_CONFIDENCE: "文字识别置信度偏低",
  MATH_PARSE_FAILED: "数学内容解析失败",
  CHEMISTRY_PARSE_FAILED: "化学内容解析失败",
  TABLE_PARSE_FAILED: "表格解析失败",
  DIAGRAM_PARSE_FAILED: "图形解析失败",
  SEGMENTATION_FAILED: "题目切分失败"
};

function processingStageLabel(stage: string) {
  return processingStageLabels[stage] ?? stage;
}

function processingIssueLabel(code: string) {
  return processingIssueLabels[code] ?? code;
}

function processingExceptionTone(exception: ProcessingException): StatusTone {
  if (exception.severity === "P0" || exception.severity === "P1") return "danger";
  if (exception.severity === "P2") return "warning";
  return "neutral";
}

function processingExceptionStatusTone(status: string): StatusTone {
  if (status === "resolved") return "success";
  if (status === "assigned") return "processing";
  return "warning";
}

function processingExceptionStatusLabel(status: string) {
  if (status === "resolved") return "已解决";
  if (status === "assigned") return "已指派";
  return "待处理";
}

export function ProcessingOperationsPanel({ examId, canManage }: { examId: string; canManage: boolean }) {
  const { message } = App.useApp();
  const focusedExceptionID = hashQueryParam("exception");
  const [summary, setSummary] = useState<ProcessingSummary | null>(null);
  const [exceptions, setExceptions] = useState<ProcessingException[]>([]);
  const [severity, setSeverity] = useState<ProcessingExceptionSeverity | undefined>();
  const [status, setStatus] = useState<ProcessingExceptionStatus | undefined>("open");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actioning, setActioning] = useState<string | null>(null);
  const [assigning, setAssigning] = useState<ProcessingException | null>(null);
  const [assigneeID, setAssigneeID] = useState("");
  const [resolving, setResolving] = useState<ProcessingException | null>(null);
  const [resolution, setResolution] = useState("");

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    // 汇总和异常列表允许各自降级，单侧失败时保留另一侧并明确提示加载不完整。
    const [summaryResult, exceptionResult] = await Promise.allSettled([
      getProcessingSummary(examId, signal),
      listProcessingExceptions({ examId, severity, status, limit: 25 }, signal)
    ]);
    if (signal?.aborted) return;
    if (summaryResult.status === "fulfilled") {
      setSummary(summaryResult.value.summary);
    } else {
      setSummary(null);
    }
    if (exceptionResult.status === "fulfilled") {
      setExceptions(exceptionResult.value.exceptions);
    } else {
      setExceptions([]);
    }
    if (summaryResult.status === "rejected" && exceptionResult.status === "rejected") {
      setError(formatError(summaryResult.reason));
    } else if (summaryResult.status === "rejected" || exceptionResult.status === "rejected") {
      setError("部分处理状态暂时无法加载，可稍后刷新。");
    }
    setLoading(false);
  }, [examId, severity, status]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const retry = useCallback(async (exception: ProcessingException) => {
    setActioning(`retry:${exception.id}`);
    try {
      await retryProcessingException(exception.id);
      message.success("已重新进入处理队列");
      await load();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
  }, [load, message]);

  const assign = useCallback(async () => {
    if (!assigning) return;
    const normalizedAssigneeID = assigneeID.trim();
    if (!normalizedAssigneeID) {
      message.warning("请输入处理人账号 ID");
      return;
    }
    setActioning(`assign:${assigning.id}`);
    try {
      await assignProcessingException(assigning.id, normalizedAssigneeID);
      message.success("已指派处理人");
      setAssigning(null);
      setAssigneeID("");
      await load();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
  }, [assigneeID, assigning, load, message]);

  const resolve = useCallback(async () => {
    if (!resolving) return;
    const normalizedResolution = resolution.trim();
    if (!normalizedResolution) {
      message.warning("请填写处理说明");
      return;
    }
    setActioning(`resolve:${resolving.id}`);
    try {
      await resolveProcessingException(resolving.id, normalizedResolution);
      message.success("异常已标记为解决");
      setResolving(null);
      setResolution("");
      await load();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
  }, [load, message, resolution, resolving]);

  const exceptionColumns: TableColumnsType<ProcessingException> = [
    {
      title: "问题",
      key: "issue",
      render: (_, exception) => (
        <div className="capture-identity-cell">
          <strong>{processingIssueLabel(exception.code)}</strong>
          <span className="muted-text">页面 {exception.page_id.slice(0, 8)}</span>
        </div>
      )
    },
    {
      title: "级别",
      dataIndex: "severity",
      width: 96,
      render: (_value, exception) => <StatusTag tone={processingExceptionTone(exception)}>{exception.severity}</StatusTag>
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 100,
      render: (value: string) => <StatusTag tone={processingExceptionStatusTone(value)}>{processingExceptionStatusLabel(value)}</StatusTag>
    },
    {
      title: "处理人",
      dataIndex: "assigned_to",
      width: 132,
      render: (value?: string) => value || "未指派"
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      width: 168,
      render: formatTime
    },
    {
      title: "操作",
      key: "actions",
      width: 220,
      render: (_, exception) => (
        <Space className="table-actions" size={4} wrap>
          {canManage && exception.status !== "resolved" && exception.retry_source_id ? (
            <Button
              size="small"
              loading={actioning === `retry:${exception.id}`}
              onClick={() => void retry(exception)}
            >
              重试
            </Button>
          ) : null}
          {canManage && exception.status !== "resolved" ? (
            <Button
              size="small"
              loading={actioning === `assign:${exception.id}`}
              onClick={() => {
                setAssigneeID(exception.assigned_to ?? "");
                setAssigning(exception);
              }}
            >
              指派
            </Button>
          ) : null}
          {canManage && exception.status !== "resolved" ? (
            <Button
              size="small"
              loading={actioning === `resolve:${exception.id}`}
              onClick={() => {
                setResolution("");
                setResolving(exception);
              }}
            >
              解决
            </Button>
          ) : null}
          {!canManage || exception.status === "resolved" ? "-" : null}
        </Space>
      )
    }
  ];

  const stageItems = (summary?.by_stage ?? []).filter((item) => item.count > 0);

  return (
    <section className="workspace-section">
      <div className="section-head">
        <div>
          <h2>处理状态</h2>
          <p>按页面汇总导入、质量检查、识别和切题状态。</p>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>
          刷新
        </Button>
      </div>

      {error ? (
        <Alert
          type="warning"
          showIcon
          message="处理状态未完全加载"
          description={error}
          action={<Button size="small" onClick={() => void load()}>重试</Button>}
        />
      ) : null}

      {focusedExceptionID ? (
        <Alert
          style={{ marginTop: 16 }}
          type="info"
          showIcon
          message="已从考试工作区定位到处理异常"
          description="请在下方异常列表中处理该页面；处理完成后，工作区的阻断提示会在刷新后同步更新。"
        />
      ) : null}

      {summary ? (
        <>
          <Descriptions size="small" column={{ xs: 2, sm: 4 }} style={{ marginTop: 16 }}>
            <Descriptions.Item label="总页数">{summary.total_pages}</Descriptions.Item>
            <Descriptions.Item label="可阅卷">{summary.ready_pages}</Descriptions.Item>
            <Descriptions.Item label="待处理">{summary.pending_pages}</Descriptions.Item>
            <Descriptions.Item label="需处理">{summary.blocked_pages}</Descriptions.Item>
          </Descriptions>
          {stageItems.length > 0 ? (
            <Space wrap size={[6, 6]} style={{ marginTop: 12 }}>
              {stageItems.map((item) => <StatusTag key={item.stage} tone={item.stage === "READY" ? "success" : "processing"}>{`${processingStageLabel(item.stage)} ${item.count}`}</StatusTag>)}
            </Space>
          ) : null}
          {summary.blocked_pages > 0 ? (
            <Alert
              style={{ marginTop: 16 }}
              type="warning"
              showIcon
              message={`${summary.blocked_pages} 页需要处理`}
              description="请在下方异常列表中重试、指派或确认解决。"
            />
          ) : null}
        </>
      ) : null}

      <div className="section-head" style={{ marginTop: 20 }}>
        <div>
          <h2>处理异常</h2>
          <p>仅显示当前考试的页面级异常。</p>
        </div>
        <Space wrap>
          <Select<ProcessingExceptionSeverity>
            className="toolbar-select"
            allowClear
            placeholder="全部级别"
            value={severity}
            options={["P0", "P1", "P2", "P3"].map((value) => ({ label: value, value }))}
            onChange={(value) => setSeverity(value)}
          />
          <Select<ProcessingExceptionStatus>
            className="toolbar-select"
            allowClear
            placeholder="全部状态"
            value={status}
            options={[
              { label: "待处理", value: "open" },
              { label: "已指派", value: "assigned" },
              { label: "已解决", value: "resolved" }
            ]}
            onChange={(value) => setStatus(value)}
          />
        </Space>
      </div>
      <ResponsiveTable<ProcessingException>
        className="dense-data-table"
        rowKey="id"
        columns={exceptionColumns}
        dataSource={exceptions}
        loading={loading}
        pagination={false}
        size="small"
        mobilePrimaryCount={2}
        rowClassName={(exception) => exception.id === focusedExceptionID ? "capture-focused-processing-exception" : ""}
        locale={{ emptyText: <EmptyState title="暂无处理异常" description="当前筛选条件下没有需要处理的页面。" /> }}
      />

      <Modal
        title="指派处理人"
        open={Boolean(assigning)}
        okText="确认指派"
        cancelText="取消"
        confirmLoading={Boolean(assigning && actioning === `assign:${assigning.id}`)}
        onCancel={() => setAssigning(null)}
        onOk={() => void assign()}
      >
        <p className="muted-text">输入学校内处理人的账号 ID。</p>
        <Input autoFocus value={assigneeID} onChange={(event) => setAssigneeID(event.target.value)} placeholder="处理人账号 ID" />
      </Modal>
      <Modal
        title="确认解决异常"
        open={Boolean(resolving)}
        okText="确认解决"
        cancelText="取消"
        confirmLoading={Boolean(resolving && actioning === `resolve:${resolving.id}`)}
        onCancel={() => setResolving(null)}
        onOk={() => void resolve()}
      >
        <p className="muted-text">请记录已采取的处理措施，便于后续追溯。</p>
        <Input.TextArea autoFocus rows={3} value={resolution} onChange={(event) => setResolution(event.target.value)} placeholder="例如：已人工确认页面完整，允许继续处理" />
      </Modal>
    </section>
  );
}
