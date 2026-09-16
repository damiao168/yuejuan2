import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Empty, Form, Input, InputNumber, List, Space, Table, Tag, type TableColumnsType } from "antd";
import { BookOpenCheck, Download, KeyRound, RefreshCw, Save, Send, Trash2 } from "lucide-react";
import { downloadFileBlob } from "../api/files";
import { getUserErrorMessage } from "../api/userError";
import { listQuestions } from "../api/papers";
import { getReviewTask, listAiGrades, listReviewTasks, submitHumanGrade } from "../api/review";
import { listAnswerSegments, listOcrTasks, listSubmissionPages } from "../api/submissions";
import type { DesktopApiClient } from "../api/client";
import {
  loadOfflineDraft,
  listOfflineDraftEnvelopes,
  purgeExpiredOfflineDrafts,
  readOfflineDraftEnvelopes,
  saveOfflineDraft,
  updateOfflineDraftStatus,
  type OfflineDraftEnvelope
} from "../lib/offlineStore";
import { hasDurableDesktopStore } from "../lib/durableStore";
import { genericStatusLabel, offlineSyncStatusLabels } from "../statusLabels";
import type {
  AuthUser,
  AiGrade,
  LocalLogEntry,
  OfflineDraftRecord,
  OfflineGradeDraft,
  OfflineTaskPackage,
  Question,
  ReviewTask,
  RubricSelection
} from "../types";

interface OfflineWorkbenchProps {
  client: DesktopApiClient;
  token: string | null;
  user: AuthUser | null;
  isOnline: boolean;
  onLog: (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
}

export function OfflineWorkbench({ client, token, user, isOnline, onLog }: OfflineWorkbenchProps) {
  const [offlineKey, setOfflineKey] = useState("");
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [selectedTaskId, setSelectedTaskId] = useState("");
  const [taskError, setTaskError] = useState<string | null>(null);
  const [loadingTasks, setLoadingTasks] = useState(false);
  const [pkg, setPkg] = useState<OfflineTaskPackage | null>(null);
  const [packageError, setPackageError] = useState<string | null>(null);
  const [packageLoading, setPackageLoading] = useState(false);
  const [imagePreview, setImagePreview] = useState<{ url: string; contentType: string; filename?: string } | null>(null);
  const [draft, setDraft] = useState<OfflineGradeDraft>(() => emptyDraft(""));
  const [envelopes, setEnvelopes] = useState<OfflineDraftEnvelope[]>(() => readOfflineDraftEnvelopes());
  const [syncStatus, setSyncStatus] = useState<OfflineDraftEnvelope["syncStatus"]>("draft");
  const [syncMessage, setSyncMessage] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const packageRequestRef = useRef(0);

  const latestAiGrade = useMemo(() => latestGrade(pkg?.aiGrades ?? []), [pkg?.aiGrades]);
  const maxScore = pkg?.question?.rubric?.max_score ?? pkg?.question?.score ?? latestAiGrade?.max_score ?? 0;
  const hasKey = hasDurableDesktopStore() || offlineKey.trim().length >= 8;
  const currentEnvelope = envelopes.find((item) => item.taskId === (pkg?.task.id ?? selectedTaskId));

  const refreshEnvelopes = async () => setEnvelopes(await listOfflineDraftEnvelopes());

  useEffect(() => {
    void refreshEnvelopes();
  }, []);

  useEffect(() => () => {
    if (imagePreview?.url) URL.revokeObjectURL(imagePreview.url);
  }, [imagePreview?.url]);

  useEffect(() => () => {
    packageRequestRef.current += 1;
  }, []);

  const loadMyTasks = async () => {
    setTaskError(null);
    setLoadingTasks(true);
    if (!token || !user) {
      setTasks([]);
      setTaskError("未登录：不会读取离线阅卷任务，也不会显示假任务。");
      setLoadingTasks(false);
      return;
    }
    try {
      const result = await listReviewTasks(client, { assigned_to: user.id });
      setTasks(result.tasks);
      setSelectedTaskId((current) => current || result.tasks[0]?.id || "");
      await onLog("info", "offline review tasks loaded", `${result.tasks.length} tasks`);
    } catch (error) {
      const message = formatError(error);
      setTaskError(message);
      setTasks([]);
      await onLog("warning", "offline task load failed", message);
    } finally {
      setLoadingTasks(false);
    }
  };

  const downloadPackage = async (taskId = selectedTaskId) => {
    const requestId = ++packageRequestRef.current;
    if (!taskId || !token) {
      setPackageError("请先登录并选择真实阅卷任务。");
      return;
    }
    setPackageError(null);
    setPackageLoading(true);
    setImagePreview((current) => {
      if (current?.url) {
        URL.revokeObjectURL(current.url);
      }
      return null;
    });
    try {
      const detail = await getReviewTask(client, taskId);
      if (requestId !== packageRequestRef.current) return;
      const nextPackage = await buildTaskPackage(client, detail.task);
      if (requestId !== packageRequestRef.current) return;
      setPkg(nextPackage);
      setDraft(createInitialDraft(nextPackage));
      setSyncStatus("draft");
      setSyncMessage(null);
      if (nextPackage.page?.file_asset_id) {
        try {
          const blob = await downloadFileBlob(client, nextPackage.page.file_asset_id);
          if (requestId !== packageRequestRef.current) return;
          setImagePreview({ url: URL.createObjectURL(blob.blob), contentType: blob.contentType, filename: blob.filename });
        } catch (error) {
          if (requestId !== packageRequestRef.current) return;
          nextPackage.warnings.push(`答案图片下载失败：${formatError(error)}`);
        }
      }
      await onLog("info", "offline task package downloaded", taskId);
    } catch (error) {
      if (requestId !== packageRequestRef.current) return;
      const message = formatError(error);
      setPackageError(message);
      await onLog("error", "offline task package download failed", message);
    } finally {
      if (requestId === packageRequestRef.current) setPackageLoading(false);
    }
  };

  const saveDraft = async () => {
    if (!pkg) {
      setSyncMessage("请先下载任务包。");
      return;
    }
    if (!hasKey) {
      setSyncMessage("本地离线密钥至少 8 个字符；未配置密钥时禁止保存草稿。");
      return;
    }
    const now = new Date();
    const record: OfflineDraftRecord = {
      taskId: pkg.task.id,
      anonymousCode: pkg.task.anonymous_code,
      savedAt: now.toISOString(),
      expiresAt: new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString(),
      syncStatus: "draft",
      packageSnapshot: pkg,
      draft
    };
    await saveOfflineDraft(record, offlineKey);
    await refreshEnvelopes();
    setSyncStatus("draft");
    setSyncMessage("草稿已加密保存到本地。");
    await onLog("info", "offline draft encrypted and saved", pkg.task.id);
  };

  const loadDraft = async (taskId: string) => {
    if (!hasKey) {
      setSyncMessage("请输入本地离线密钥后再加载草稿。");
      return;
    }
    try {
      const record = await loadOfflineDraft(taskId, offlineKey);
      if (!record) {
        setSyncMessage("未找到本地草稿。");
        return;
      }
      setPkg(record.packageSnapshot);
      setDraft(record.draft);
      setSelectedTaskId(taskId);
      setImagePreview((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return null;
      });
      setSyncStatus(record.syncStatus);
      setSyncMessage(record.syncMessage ?? "本地加密草稿已加载。");
      await onLog("info", "offline draft decrypted and loaded", taskId);
    } catch (error) {
      const message = `草稿解密失败：${formatError(error)}`;
      setSyncMessage(message);
      await onLog("warning", "offline draft decrypt failed", message);
    }
  };

  const syncDraft = async () => {
    if (!pkg) {
      setSyncMessage("请先下载或加载任务包。");
      return;
    }
    if (!draft || draft.score === null) {
      setSyncMessage("请先填写最终分。");
      return;
    }
    if (currentEnvelope?.syncStatus === "synced" || syncStatus === "synced") {
      setSyncMessage("该草稿已同步成功，已阻止重复提交。");
      return;
    }
    if (!token || !isOnline) {
      setSyncMessage("当前未登录或离线，无法同步；草稿可稍后重试。");
      await updateOfflineDraftStatus(pkg.task.id, { syncStatus: "failed", syncMessage: "未登录或离线" });
      await refreshEnvelopes();
      return;
    }
    setSyncing(true);
    setSyncStatus("syncing");
    try {
      const conflict = await detectConflict(client, pkg, user);
      if (conflict) {
        setSyncStatus("conflict");
        setSyncMessage(conflict);
        await updateOfflineDraftStatus(pkg.task.id, { syncStatus: "conflict", syncMessage: conflict });
        await refreshEnvelopes();
        await onLog("warning", "offline draft sync conflict", conflict);
        return;
      }
      const selections: RubricSelection[] = Object.entries(draft.rubricSelections)
        .filter(([, score]) => Number(score) > 0)
        .map(([point_id, score]) => ({ point_id, score: Number(score) }));
      await submitHumanGrade(client, pkg.task.id, {
        expected_revision: pkg.task.revision,
        score: Number(draft.score),
        rubric_selections: selections,
        comments: draft.comments,
        private_note: draft.privateNote,
        student_feedback: draft.studentFeedback,
        reason: draft.reason || "offline review synced"
      });
      setSyncStatus("synced");
      setSyncMessage("同步成功，服务端已接收人工评分。");
      await updateOfflineDraftStatus(pkg.task.id, { syncStatus: "synced", syncMessage: "同步成功" });
      await refreshEnvelopes();
      await onLog("info", "offline draft synced", pkg.task.id);
    } catch (error) {
      const message = formatError(error);
      setSyncStatus("failed");
      setSyncMessage(message);
      await updateOfflineDraftStatus(pkg.task.id, { syncStatus: "failed", syncMessage: message });
      await refreshEnvelopes();
      await onLog("error", "offline draft sync failed", message);
    } finally {
      setSyncing(false);
    }
  };

  const purgeExpired = async () => {
    const count = await purgeExpiredOfflineDrafts();
    await refreshEnvelopes();
    await onLog("info", "expired offline drafts purged", `${count} drafts`);
    setSyncMessage(`已清理 ${count} 条过期本地缓存。`);
  };

  return (
    <div className="offline-workbench">
      <section className="panel full">
        <div className="offline-head">
          <div>
            <p className="eyebrow">离线阅卷</p>
            <h3>离线阅卷基础工作台</h3>
            <p>任务包聚合真实后端 API；草稿使用本地离线密钥加密保存。</p>
          </div>
          <Space wrap>
            <Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线"}</Tag>
            <Tag color={hasKey ? "success" : "warning"}>{hasKey ? "离线密钥已输入" : "未配置离线密钥"}</Tag>
          </Space>
        </div>
        <Alert
          type="info"
          showIcon
          message={
            hasDurableDesktopStore()
              ? "草稿与任务包已加密写入本机 SQLite，主密钥仅保存在 Windows 凭据库；本地答题图片只保留受控加密 spool，服务端确认与保留期结束前不会删除。"
              : "浏览器开发模式仅使用显式加密草稿回退，不能作为生产扫描站或上传恢复验证。"
          }
        />
        <div className="offline-toolbar">
          {!hasDurableDesktopStore() && <Input.Password prefix={<KeyRound size={14} />} value={offlineKey} onChange={(event) => setOfflineKey(event.target.value)} placeholder="浏览器开发密钥，至少 8 位" />}
          <Button icon={<RefreshCw size={16} />} loading={loadingTasks} disabled={!token || !user} onClick={loadMyTasks}>
            获取我的任务
          </Button>
          <Button icon={<Trash2 size={16} />} onClick={purgeExpired}>
            清理过期缓存
          </Button>
        </div>
        {taskError && <Alert className="section-alert" type="error" message={taskError} showIcon />}
      </section>

      <div className="offline-grid">
        <section className="panel">
          <div className="section-head compact">
            <div className="section-title">
              <span>
                <BookOpenCheck size={20} />
              </span>
              <div>
                <h3>我的阅卷任务</h3>
                <p>只读取分配给当前用户的真实任务。</p>
              </div>
            </div>
          </div>
          <Table rowKey="id" size="small" columns={taskColumns(setSelectedTaskId)} dataSource={tasks} pagination={{ pageSize: 6, showSizeChanger: false }} locale={{ emptyText: <Empty description="暂无任务" /> }} />
          <Button className="section-button" type="primary" icon={<Download size={16} />} loading={packageLoading} disabled={!selectedTaskId || !token} onClick={() => void downloadPackage()}>
            下载任务包
          </Button>
          {packageError && <Alert className="section-alert" type="error" message={packageError} showIcon />}
        </section>

        <section className="panel">
          <div className="section-head compact">
            <div className="section-title">
              <span>
                <Save size={20} />
              </span>
              <div>
                <h3>本地加密草稿</h3>
                <p>同步结果和冲突状态在这里可见。</p>
              </div>
            </div>
          </div>
          <List
            size="small"
            dataSource={envelopes}
            locale={{ emptyText: <Empty description="暂无本地草稿" /> }}
            renderItem={(item) => (
              <List.Item actions={[<Button key="load" size="small" disabled={!hasKey} onClick={() => void loadDraft(item.taskId)}>加载</Button>]}>
                <List.Item.Meta
                  title={
                    <Space wrap>
                      <span>{item.anonymousCode}</span>
                      <SyncTag status={item.syncStatus} />
                    </Space>
                  }
                  description={`${formatDate(item.savedAt)} / 过期 ${formatDate(item.expiresAt)}${item.syncMessage ? ` / ${item.syncMessage}` : ""}`}
                />
              </List.Item>
            )}
          />
        </section>
      </div>

      <div className="offline-grid workbench">
        <section className="panel">
          <PackageView pkg={pkg} imagePreview={imagePreview} />
        </section>
        <section className="panel">
          <DraftEditor pkg={pkg} draft={draft} setDraft={setDraft} maxScore={maxScore} latestAiGrade={latestAiGrade} />
          <Space className="offline-actions" wrap>
            <Button icon={<Save size={16} />} disabled={!pkg || !hasKey} onClick={() => void saveDraft()}>
              加密保存草稿
            </Button>
            <Button type="primary" icon={<Send size={16} />} loading={syncing} disabled={!pkg || syncStatus === "synced"} onClick={() => void syncDraft()}>
              同步提交
            </Button>
            <SyncTag status={syncStatus} />
          </Space>
          {syncMessage && <Alert className="section-alert" type={syncStatus === "conflict" || syncStatus === "failed" ? "warning" : "info"} message={syncMessage} showIcon />}
        </section>
      </div>
    </div>
  );
}

function taskColumns(selectTask: (id: string) => void): TableColumnsType<ReviewTask> {
  return [
    { title: "匿名号", dataIndex: "anonymous_code" },
    { title: "题号", dataIndex: "question_no", width: 80 },
    { title: "状态", dataIndex: "status", width: 100, render: (value: string) => <Tag>{value}</Tag> },
    {
      title: "操作",
      key: "action",
      width: 84,
      render: (_value, task) => (
        <Button size="small" onClick={() => selectTask(task.id)}>
          选择
        </Button>
      )
    }
  ];
}

function PackageView({ pkg, imagePreview }: { pkg: OfflineTaskPackage | null; imagePreview: { url: string; contentType: string; filename?: string } | null }) {
  if (!pkg) {
    return <Empty description="尚未下载任务包" />;
  }
  return (
    <div className="package-view">
      <div className="package-summary">
        <Tag color="blue">{pkg.task.anonymous_code}</Tag>
        <Tag>{genericStatusLabel(pkg.task.status)}</Tag>
        <Tag>Rubric {pkg.rubricVersion ?? "未记录"}</Tag>
      </div>
      {pkg.warnings.length > 0 && <Alert type="warning" showIcon message="任务包不完整" description={pkg.warnings.join("；")} />}
      <div className="answer-preview">
        {imagePreview ? <img src={imagePreview.url} alt={imagePreview.filename ?? "answer page"} /> : <Empty description={pkg.page ? "答案图片待下载/下载失败" : "未定位答案页面"} />}
      </div>
      <h4>OCR 文本</h4>
      <pre>{pkg.ocrText || "未记录 OCR 文本"}</pre>
      <h4>评分细则</h4>
      <List
        size="small"
        dataSource={pkg.question?.rubric?.points ?? []}
        locale={{ emptyText: <Empty description="未获取 Rubric" /> }}
        renderItem={(point) => (
          <List.Item>
            <span>{point.description}</span>
            <Tag>{point.score} 分</Tag>
          </List.Item>
        )}
      />
      <h4>AI 建议</h4>
      {latestGrade(pkg.aiGrades) ? (
        <p>
          建议分 {latestGrade(pkg.aiGrades)?.suggested_score}/{latestGrade(pkg.aiGrades)?.max_score}，置信度 {Math.round((latestGrade(pkg.aiGrades)?.confidence ?? 0) * 100)}%
        </p>
      ) : (
        <p className="muted">未获取 AI 建议分。</p>
      )}
    </div>
  );
}

function DraftEditor({
  pkg,
  draft,
  setDraft,
  maxScore,
  latestAiGrade
}: {
  pkg: OfflineTaskPackage | null;
  draft: OfflineGradeDraft;
  setDraft: React.Dispatch<React.SetStateAction<OfflineGradeDraft>>;
  maxScore: number;
  latestAiGrade?: AiGrade;
}) {
  if (!pkg) {
    return <Empty description="下载任务包后填写草稿" />;
  }
  return (
    <Form layout="vertical" className="offline-draft-form">
      <Form.Item label={`最终分 / ${maxScore || "未记录"} 分`}>
        <InputNumber min={0} max={maxScore || undefined} value={draft.score} onChange={(value) => setDraft((current) => ({ ...current, score: value === null ? null : Number(value) }))} />
        {latestAiGrade && <span className="muted"> AI 建议：{latestAiGrade.suggested_score}</span>}
      </Form.Item>
      <div className="rubric-edit-list">
        {(pkg.question?.rubric?.points ?? []).map((point) => (
          <label key={point.id}>
            <span>{point.description}</span>
            <InputNumber
              min={0}
              max={point.score}
              value={draft.rubricSelections[point.id] ?? 0}
              onChange={(value) =>
                setDraft((current) => ({
                  ...current,
                  rubricSelections: { ...current.rubricSelections, [point.id]: value === null ? 0 : Number(value) }
                }))
              }
            />
          </label>
        ))}
      </div>
      <Form.Item label="教师评语">
        <Input.TextArea rows={3} value={draft.comments} onChange={(event) => setDraft((current) => ({ ...current, comments: event.target.value }))} />
      </Form.Item>
      <Form.Item label="学生反馈">
        <Input.TextArea rows={2} value={draft.studentFeedback} onChange={(event) => setDraft((current) => ({ ...current, studentFeedback: event.target.value }))} />
      </Form.Item>
      <Form.Item label="私密备注">
        <Input.TextArea rows={2} value={draft.privateNote} onChange={(event) => setDraft((current) => ({ ...current, privateNote: event.target.value }))} />
      </Form.Item>
      <Form.Item label="提交原因">
        <Input value={draft.reason} onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))} />
      </Form.Item>
    </Form>
  );
}

async function buildTaskPackage(client: DesktopApiClient, task: ReviewTask): Promise<OfflineTaskPackage> {
  const [segmentsResult, pagesResult, ocrTaskResult, questionResult, gradeResult] = await Promise.allSettled([
    listAnswerSegments(client, task.submission_id),
    listSubmissionPages(client, task.submission_id),
    listOcrTasks(client, task.submission_id),
    listQuestions(client, task.exam_id),
    listAiGrades(client, task.answer_segment_id)
  ]);
  const warnings = [segmentsResult, pagesResult, ocrTaskResult, questionResult, gradeResult]
    .filter((item): item is PromiseRejectedResult => item.status === "rejected")
    .map((item) => formatError(item.reason));
  const segments = segmentsResult.status === "fulfilled" ? segmentsResult.value.segments : [];
  const pages = pagesResult.status === "fulfilled" ? pagesResult.value.pages : [];
  const ocrTasks = ocrTaskResult.status === "fulfilled" ? ocrTaskResult.value.tasks : [];
  const questions = questionResult.status === "fulfilled" ? questionResult.value.questions : [];
  const aiGrades = gradeResult.status === "fulfilled" ? gradeResult.value.grades : [];
  const segment = segments.find((item) => item.id === task.answer_segment_id);
  const question = questions.find((item) => item.id === task.question_id);
  const page = segment ? pages.find((item) => item.id === segment.submission_page_id) : undefined;
  const ocrText = ocrTasks
    .flatMap((ocrTask) => ocrTask.results ?? [])
    .filter((result) => !segment || result.submission_page_id === segment.submission_page_id)
    .map((result) => result.text)
    .filter(Boolean)
    .join("\n");
  if (!segment) {
    warnings.push("未能找到当前 answer_segment。");
  }
  if (!question) {
    warnings.push("未能获取题目与 Rubric。");
  }
  if (!page) {
    warnings.push("未能定位答案图片页面。");
  }
  return {
    task,
    segment,
    page,
    ocrText,
    question,
    aiGrades,
    warnings,
    downloadedAt: new Date().toISOString(),
    rubricVersion: question?.rubric?.version
  };
}

async function detectConflict(client: DesktopApiClient, pkg: OfflineTaskPackage, user: AuthUser | null) {
  if (!Number.isInteger(pkg.task.revision) || pkg.task.revision <= 0) {
    return "本地任务包缺少版本，请重新下载任务包后核对草稿。";
  }
  const latest = await getReviewTask(client, pkg.task.id);
  if (latest.task.revision !== pkg.task.revision) {
    return "服务端任务版本已变化，请重新下载任务包后核对草稿。";
  }
  const status = latest.task.status;
  if (["submitted", "completed", "cancelled", "withdrawn", "revoked"].includes(status)) {
    return `服务端任务状态为 ${status}，本地草稿不能提交。`;
  }
  if (latest.task.assigned_to && user?.id && latest.task.assigned_to !== user.id) {
    return "服务端任务已不再分配给当前用户。";
  }
  const questionResult = await listQuestions(client, pkg.task.exam_id);
  const latestQuestion = questionResult.questions.find((question: Question) => question.id === pkg.task.question_id);
  const latestRubricVersion = latestQuestion?.rubric?.version;
  if (pkg.rubricVersion && latestRubricVersion && latestRubricVersion !== pkg.rubricVersion) {
    return `Rubric 版本变化：本地 ${pkg.rubricVersion}，服务端 ${latestRubricVersion}。`;
  }
  return null;
}

function createInitialDraft(pkg: OfflineTaskPackage): OfflineGradeDraft {
  const grade = latestGrade(pkg.aiGrades);
  const rubricSelections: Record<string, number> = {};
  for (const point of pkg.question?.rubric?.points ?? []) {
    rubricSelections[point.id] = 0;
  }
  return {
    taskId: pkg.task.id,
    score: grade && !grade.mock ? grade.suggested_score : null,
    rubricSelections,
    comments: "",
    studentFeedback: grade?.student_feedback ?? "",
    privateNote: "",
    reason: "offline review synced"
  };
}

function emptyDraft(taskId: string): OfflineGradeDraft {
  return {
    taskId,
    score: null,
    rubricSelections: {},
    comments: "",
    studentFeedback: "",
    privateNote: "",
    reason: "offline review synced"
  };
}

function latestGrade(grades: AiGrade[]) {
  return grades.filter((grade) => !grade.mock).sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0] ?? grades[0];
}

function SyncTag({ status }: { status: OfflineDraftEnvelope["syncStatus"] }) {
  const color = status === "synced" ? "success" : status === "conflict" ? "warning" : status === "failed" ? "error" : status === "syncing" ? "processing" : "default";
  return <Tag color={color}>{offlineSyncStatusLabels[status]}</Tag>;
}

function formatError(error: unknown) {
  return getUserErrorMessage(error);
}

function formatDate(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}
