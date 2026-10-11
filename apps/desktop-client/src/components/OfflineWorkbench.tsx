import { useCallback, useEffect, useMemo, useRef, useState, type FC, type ReactNode } from "react";
import { Alert, Button, Empty, Form, Input, InputNumber, List, Modal, Space, Table, Tag, type TableColumnsType } from "antd";
import { BookOpenCheck, Download, KeyRound, RefreshCw, Save, Send, Trash2 } from "lucide-react";
import { AnswerDocumentViewer, type AnswerDocument } from "./AnswerDocumentViewer";
import { getUserErrorMessage } from "../api/userError";
import { listExams } from "../api/exams";
import { downloadReviewTaskImage, getReviewTask, getReviewTaskContext, listReviewTasks } from "../api/review";
import type { DesktopApiClient } from "../api/client";
import type { ReviewTaskContext } from "@edugrade/sdk";
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
import { assertOfflineGradeCommandIdentity, syncOfflineGradeCommand } from "../lib/offlineReviewCommand";
import { reviewTaskState, offlineSyncStatusLabels } from "../statusLabels";
import type {
  AuthUser,
  AiGrade,
  LocalLogEntry,
  OfflineDraftRecord,
  OfflineGradeDraft,
  OfflineGradeCommand,
  OfflineTaskPackage,
  Question,
  ReviewTask,
  RubricSelection
} from "../types";

type OfflineTaskPackageWithAnswer = OfflineTaskPackage & {
  /** 任务上下文提供的原始作答或识别文字。 */
  answerText?: string;
  /** 任务上下文按阅卷权限生成的同源图片地址，不是文件管理接口的 asset id。 */
  answerImageUrl?: string;
  /** 识别候选只供核对，不能把历史候选或候选载荷当成人工总分。 */
  aiCandidates?: ReviewTaskContext["ai_candidates"];
  aiSecondOpinion?: ReviewTaskContext["ai_second_opinion"];
};

type OfflineAnswerPreview = AnswerDocument & { taskId: string; requestId: number };

interface OfflineWorkbenchProps {
  durableScopeKey: string;
  client: DesktopApiClient;
  token: string | null;
  user: AuthUser | null;
  isOnline: boolean;
  onLog: (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
  registerLeaveGuard?: (guard: OfflineLeaveGuard | null) => void;
  initialTaskId?: string;
}

export interface OfflineLeaveGuard {
  requestLeave: () => Promise<boolean>;
  shouldBlockClose: () => boolean;
}

// 页脚只提供按钮；点击时由本次回调读取操作锁，再决定是否允许放弃输入。
function renderOfflineLeaveFooter(onDiscard: () => void, _originNode: ReactNode, { OkBtn, CancelBtn }: { OkBtn: FC; CancelBtn: FC }) {
  return <Space><CancelBtn /><Button danger onClick={onDiscard}>放弃未保存输入并离开</Button><OkBtn /></Space>;
}

function selectedOfflineReviewTask(tasks: readonly ReviewTask[], selectedTaskId: string, pkg: OfflineTaskPackage | null): ReviewTask | undefined {
  // 当前列表优先；离线恢复后列表尚未读取时，仅回退到同一已选任务的保存材料。
  return tasks.find((item) => item.id === selectedTaskId) ?? (pkg?.task.id === selectedTaskId ? pkg.task : undefined);
}

export function OfflineWorkbench({ durableScopeKey, client, token, user, isOnline, onLog, registerLeaveGuard, initialTaskId }: Readonly<OfflineWorkbenchProps>) {
  const [offlineKey, setOfflineKey] = useState("");
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [examNames, setExamNames] = useState<Record<string, string>>({});
  const [selectedTaskId, setSelectedTaskId] = useState("");
  const [taskError, setTaskError] = useState<string | null>(null);
  const [loadingTasks, setLoadingTasks] = useState(false);
  const [pkg, setPkg] = useState<OfflineTaskPackageWithAnswer | null>(null);
  const [packageError, setPackageError] = useState<string | null>(null);
  const [packageLoading, setPackageLoading] = useState(false);
  const [imagePreview, setImagePreview] = useState<OfflineAnswerPreview | null>(null);
  const [readablePreview, setReadablePreview] = useState<{ preview: OfflineAnswerPreview; readable: boolean } | null>(null);
  const imagePreviewRef = useRef(imagePreview);
  imagePreviewRef.current = imagePreview;
  const onImageReadableChange = useCallback((preview: OfflineAnswerPreview, readable: boolean) => {
    // 换图或换任务后的旧图片事件不能放行当前任务，旧查看器卸载也不能撤销新图片状态。
    if (preview !== imagePreviewRef.current) return;
    setReadablePreview((current) => current?.preview === preview && current.readable === readable ? current : { preview, readable });
  }, []);
  const [draft, setDraft] = useState<OfflineGradeDraft>(() => emptyDraft(""));
  const [envelopes, setEnvelopes] = useState<OfflineDraftEnvelope[]>(() => readOfflineDraftEnvelopes());
  const [syncStatus, setSyncStatus] = useState<OfflineDraftEnvelope["syncStatus"]>("draft");
  const [syncMessage, setSyncMessage] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [savingDraft, setSavingDraft] = useState(false);
  const [pendingCommand, setPendingCommand] = useState(false);
  const [savedSnapshot, setSavedSnapshot] = useState<{ package: string; draft: string } | null>(null);
  const pendingCommands = useRef(new Map<string, OfflineGradeCommand>());
  const packageRequestRef = useRef(0);
  // ref 同步阻止连续点击；state 负责按钮显示，不能替代尚未重新渲染时的互斥检查。
  const packageBusy = useRef(false);
  const syncInFlight = useRef(false);
  const draftSaveInFlight = useRef(false);
  // 即使本地回执写入失败，也要保留服务端已确认的结果。
  const confirmedTaskIds = useRef(new Set<string>());

  const latestAiGrade = useMemo(() => latestGrade(pkg?.aiGrades ?? []), [pkg?.aiGrades]);
  const maxScore = pkg?.question?.rubric?.max_score ?? pkg?.question?.score ?? latestAiGrade?.max_score ?? 0;
  const hasKey = hasDurableDesktopStore() || offlineKey.trim().length >= 8;
  const currentEnvelope = envelopes.find((item) => item.taskId === (pkg?.task.id ?? selectedTaskId));
  const selectedTask = selectedOfflineReviewTask(tasks, selectedTaskId, pkg);
  const alreadySynced = syncStatus === "synced" || currentEnvelope?.syncStatus === "synced" || confirmedTaskIds.current.has(pkg?.task.id ?? "");
  const hasReadableImage = Boolean(imagePreview?.taskId === pkg?.task.id && readablePreview?.preview === imagePreview && readablePreview?.readable);
  // 地址和 Blob 只说明材料可下载；实际显示失败或重启丢图后，均不能把图片算作可核对材料。
  const hasAnswerMaterial = Boolean(pkg && (packageAnswerText(pkg) || hasReadableImage));
  const packageSaved = Boolean(pkg && savedSnapshot?.package === JSON.stringify(pkg));
  const draftSaved = Boolean(pkg && savedSnapshot?.draft === JSON.stringify(draft));
  const nextTask = tasks.find((item) => item.id !== pkg?.task.id && reviewTaskState(item.status).canGrade
    && !confirmedTaskIds.current.has(item.id) && !envelopes.some((envelope) => envelope.taskId === item.id && envelope.syncStatus === "synced"));
  const editorRef = useRef({ pkg, draft, draftSaved });
  editorRef.current = { pkg, draft, draftSaved };
  // 图片报错后可能还有此前捕获的提交回调；材料检查必须读取最新任务和显示结果。
  const answerMaterialRef = useRef({ pkg, available: hasAnswerMaterial });
  answerMaterialRef.current = { pkg, available: hasAnswerMaterial };

  const refreshEnvelopes = useCallback(async () => setEnvelopes(await listOfflineDraftEnvelopes(durableScopeKey)), [durableScopeKey]);

  useEffect(() => {
    if (durableScopeKey || !hasDurableDesktopStore()) {
      void refreshEnvelopes().catch((error) => setSyncMessage(`本地列表读取失败：${formatError(error)}`));
    }
  }, [durableScopeKey, refreshEnvelopes]);

  useEffect(() => () => {
    if (imagePreview?.url) URL.revokeObjectURL(imagePreview.url);
  }, [imagePreview?.url]);

  const requestScope = useMemo(() => ({ durableScopeKey, client, actorId: user?.id, tenantId: user?.tenant_id, token }), [durableScopeKey, client, user?.id, user?.tenant_id, token]);
  const activeRequestScope = useRef<typeof requestScope | null>(null);
  useEffect(() => {
    // 新会话接管锁和加载状态；旧流程通过代次失效，不能在 finally 解开新操作。
    activeRequestScope.current = requestScope;
    packageBusy.current = false; syncInFlight.current = false; draftSaveInFlight.current = false;
    setSyncing(false); setSavingDraft(false); setPackageLoading(false);
    return () => { activeRequestScope.current = null; packageRequestRef.current += 1; };
  }, [requestScope]);

  const loadMyTasks = async () => {
    const owner = activeRequestScope.current;
    setTaskError(null);
    setLoadingTasks(true);
    if (!token || !user) {
      setTasks([]);
      setTaskError("未登录：不会读取离线阅卷任务，也不会显示假任务。");
      setLoadingTasks(false);
      return;
    }
    try {
      const [taskResult, examResult] = await Promise.allSettled([listReviewTasks(client, { assigned_to: user.id }), listExams(client)]);
      if (owner !== activeRequestScope.current) return;
      if (taskResult.status === "rejected") throw taskResult.reason;
      const result = taskResult.value;
      setExamNames(examResult.status === "fulfilled" ? Object.fromEntries(examResult.value.exams.map((exam) => [exam.id, exam.name])) : {});
      setTasks(result.tasks);
      setSelectedTaskId((current) => current || result.tasks[0]?.id || "");
      await onLog("info", "offline review tasks loaded", `${result.tasks.length} tasks`);
    } catch (error) {
      if (owner !== activeRequestScope.current) return;
      const message = formatError(error);
      setTaskError(message);
      setTasks([]);
      try { await onLog("warning", "offline task load failed", message); } catch {
        setTaskError(`${message} 本地日志保存失败。`);
      }
    } finally {
      if (owner === activeRequestScope.current) setLoadingTasks(false);
    }
  };

  const downloadPackageImage = async (imagePath: string, nextPackage: OfflineTaskPackageWithAnswer, requestId: number) => {
    try {
      const blob = await downloadReviewTaskImage(client, imagePath, nextPackage.task.id);
      if (requestId !== packageRequestRef.current) return;
      setImagePreview({ url: URL.createObjectURL(blob.blob), contentType: blob.contentType, filename: blob.filename, taskId: nextPackage.task.id, requestId });
    } catch (error) {
      if (requestId !== packageRequestRef.current) return;
      nextPackage.warnings.push(`答案图片下载失败：${formatError(error)}`);
    }
  };

  const confirmPackageReplacement = (description: string) => new Promise<boolean>((resolve) => {
    Modal.confirm({ title: "核对当前评分后继续？", content: description, okText: "确认继续", cancelText: "保留当前内容", onOk: () => resolve(true), onCancel: () => resolve(false) });
  });

  const preparePackageReplacement = async (taskId: string, previous: typeof editorRef.current, requestId: number) => {
    // 换任务只影响编辑器，不删除已保存记录；未保存输入须由教师明确决定是否放弃。
    if (previous.pkg && previous.pkg.task.id !== taskId && !previous.draftSaved && !await confirmPackageReplacement("当前评分尚未保存。继续下载另一任务会清除当前编辑器的输入，已保存的本地草稿仍可重新加载。")) return null;
    if (requestId !== packageRequestRef.current) return null;
    const detail = await getReviewTask(client, taskId);
    if (requestId !== packageRequestRef.current) return null;
    const nextPackage = await buildTaskPackage(client, detail.task, user?.tenant_id ?? "");
    if (requestId !== packageRequestRef.current) return null;
    const sameTask = previous.pkg?.task.id === taskId;
    if (sameTask && nextPackage.assessmentSource !== "task_context") throw new Error("新版冻结材料读取失败，原任务包和人工评分已保留；可单独恢复答卷图片或稍后重试。");
    const changedAssessment = sameTask && packageAssessmentIdentity(previous.pkg!) !== packageAssessmentIdentity(nextPackage);
    if (pendingCommands.current.has(taskId) && changedAssessment) {
      setPackageError("原评分结果尚待确认，当前保留原任务包与评分。请先继续同步核对回执，再处理新版材料。");
      return null;
    }
    if (changedAssessment) {
      const accepted = await confirmPackageReplacement("任务版本或冻结评分依据已变化。确认后保留人工总分与评语，按新版细则重新填写评分点；请先核对新材料再提交。取消会保留原任务包及所有输入。");
      if (!accepted || requestId !== packageRequestRef.current) return null;
    }
    return { nextPackage, sameTask, changedAssessment };
  };

  const downloadPackage = async (taskId = selectedTaskId) => {
    if (syncInFlight.current || draftSaveInFlight.current || packageBusy.current) return;
    const requestId = ++packageRequestRef.current;
    if (!taskId || !token) {
      setPackageError("请先登录并选择真实阅卷任务。");
      return;
    }
    setPackageError(null);
    packageBusy.current = true;
    setPackageLoading(true);
    try {
      const previous = editorRef.current;
      const replacement = await preparePackageReplacement(taskId, previous, requestId);
      // 候选包交回时也可能已切换会话；旧代次不能替换当前编辑器。
      if (!replacement || requestId !== packageRequestRef.current) return;
      const { nextPackage, sameTask, changedAssessment } = replacement;
      setPkg(nextPackage);
      // 重下同一份材料用于恢复图像时保留人工输入；新材料只使材料保存标记失效。
      setSavedSnapshot((current) => sameTask ? current : null);
      const command = pendingCommands.current.get(taskId);
      setDraft(draftForDownloadedPackage(command, previous.draft, nextPackage, sameTask, changedAssessment));
      setPendingCommand(Boolean(command));
      const knownEnvelope = envelopes.find((item) => item.taskId === taskId);
      const confirmed = confirmedTaskIds.current.has(taskId) || knownEnvelope?.syncStatus === "synced";
      setSyncStatus(confirmed ? "synced" : "draft");
      setSyncMessage(packageDownloadMessage(confirmed, changedAssessment, sameTask));
      setImagePreview(null);
      if (nextPackage.answerImageUrl) {
        await downloadPackageImage(nextPackage.answerImageUrl, nextPackage, requestId);
      }
      if (requestId !== packageRequestRef.current) return;
      await onLog("info", "offline task package downloaded", taskId);
    } catch (error) {
      if (requestId !== packageRequestRef.current) return;
      const message = formatError(error);
      setPackageError(message);
      try { await onLog("error", "offline task package download failed", message); } catch {
        setPackageError(`${message} 本地日志保存失败。`);
      }
    } finally {
      if (requestId === packageRequestRef.current) {
        packageBusy.current = false;
        setPackageLoading(false);
      }
    }
  };

  const restorePackageImage = async () => {
    if (syncInFlight.current || draftSaveInFlight.current || packageBusy.current || !pkg || !token || !isOnline) return;
    const requestId = ++packageRequestRef.current;
    packageBusy.current = true; setPackageLoading(true); setPackageError(null);
    try {
      // 恢复图片只读取冻结任务上下文给出的受控地址；不能回查提交或文件管理接口。
      let imagePath = pkg.answerImageUrl;
      if (!imagePath) {
        // 旧加密包只保存过页面文件 ID；补取同任务上下文寻找受控图片，不替换旧评分依据或人工输入。
        const { context } = await getReviewTaskContext(client, pkg.task.id);
        if (requestId !== packageRequestRef.current) return;
        assertTaskContext(context, pkg.task);
        imagePath = context.answer_artifact?.segment_image_url?.trim();
      }
      if (!imagePath) throw new Error("当前任务没有可用答卷图片入口，请重新读取任务材料或联系管理员。");
      const image = await downloadReviewTaskImage(client, imagePath, pkg.task.id);
      if (requestId !== packageRequestRef.current) return;
      setImagePreview({ url: URL.createObjectURL(image.blob), contentType: image.contentType, filename: image.filename, taskId: pkg.task.id, requestId });
    } catch (error) {
      if (requestId === packageRequestRef.current) setPackageError(`答卷图片恢复失败，原评分未改变：${formatError(error)}`);
    } finally {
      if (requestId === packageRequestRef.current) { packageBusy.current = false; setPackageLoading(false); }
    }
  };

  const finishDraftSave = async (taskId: string, requestId: number) => {
    try {
      await refreshEnvelopes();
      if (requestId !== packageRequestRef.current) return;
      await onLog("info", "offline draft encrypted and saved", taskId);
    } catch (error) {
      if (requestId === packageRequestRef.current) setSyncMessage(`草稿已加密保存，但本地列表或日志更新失败：${formatError(error)}`);
    } finally {
      // 会话变化后锁由新流程接管，旧收尾不能释放它。
      if (requestId === packageRequestRef.current) { draftSaveInFlight.current = false; setSavingDraft(false); }
    }
  };

  const saveDraft = async (): Promise<boolean> => {
    if (syncInFlight.current || draftSaveInFlight.current || packageBusy.current || alreadySynced || confirmedTaskIds.current.has(pkg?.task.id ?? "")) return false;
    if (!pkg) {
      setSyncMessage("请先下载任务包。");
      return false;
    }
    if (!hasKey) {
      setSyncMessage("本地离线密钥至少 8 个字符；未配置密钥时禁止保存草稿。");
      return false;
    }
    draftSaveInFlight.current = true;
    setSavingDraft(true);
    const requestId = packageRequestRef.current;
    // 未确认的原命令不能被手动保存的新分数覆盖，包括刚重启后尚未加载草稿的情况。
    try {
      const stored = await loadOfflineDraft(pkg.task.id, offlineKey, durableScopeKey);
      if (requestId !== packageRequestRef.current) return false;
      if (stored?.submissionCommand || pendingCommands.current.has(pkg.task.id)) {
        setPendingCommand(true);
        setSyncMessage("原评分结果尚待确认，请先继续同步确认；草稿编辑不会替换原操作。");
        draftSaveInFlight.current = false;
        setSavingDraft(false);
        return false;
      }
    } catch (error) {
      if (requestId !== packageRequestRef.current) return false;
      setSyncMessage(`本地草稿读取失败，尚未覆盖：${formatError(error)}`);
      draftSaveInFlight.current = false;
      setSavingDraft(false);
      return false;
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
    try {
      await saveOfflineDraft(record, offlineKey, durableScopeKey);
      if (requestId !== packageRequestRef.current) return false;
      setSyncStatus("draft");
      setSavedSnapshot({ package: JSON.stringify(record.packageSnapshot), draft: JSON.stringify(record.draft) });
      setSyncMessage("草稿已加密保存到本地。");
    } catch (error) {
      if (requestId !== packageRequestRef.current) return false;
      setSyncMessage(`草稿保存失败：${formatError(error)}`);
      draftSaveInFlight.current = false;
      setSavingDraft(false);
      return false;
    }
    await finishDraftSave(pkg.task.id, requestId);
    return requestId === packageRequestRef.current;
  };

  const leaveState = useRef({ saveDraft, packageSaved, draftSaved, alreadySynced });
  leaveState.current = { saveDraft, packageSaved, draftSaved, alreadySynced };
  useEffect(() => {
    const busy = () => syncInFlight.current || draftSaveInFlight.current || packageBusy.current;
    const dirty = () => Boolean(editorRef.current.pkg && !leaveState.current.alreadySynced && (!leaveState.current.packageSaved || !leaveState.current.draftSaved));
    registerLeaveGuard?.({
      shouldBlockClose: () => busy() || dirty(),
      requestLeave: async () => {
        // 原生写盘和提交尚未返回时不能卸载；保存成功以写盘结果为准，不能只看按钮或提示文字。
        if (busy()) { setSyncMessage("正在保存、加载或提交，请等待完成后再离开。"); return false; }
        if (!dirty()) return true;
        return new Promise<boolean>((resolve) => {
          const discardAndLeave = () => {
            if (busy()) return;
            dialog.destroy(); resolve(true);
          };
          const dialog = Modal.confirm({
            title: "当前评分尚未保存",
            content: "保存到本机后离开，或明确放弃当前未保存输入。取消可继续阅卷。",
            okText: "保存后离开", cancelText: "取消",
            onOk: async () => {
              dialog.update({ cancelButtonProps: { disabled: true }, footer: undefined });
              if (!await leaveState.current.saveDraft()) {
                dialog.update({ cancelButtonProps: { disabled: false }, content: "本机保存未成功，当前评分仍保留。请检查工作台提示并重试，或取消继续编辑。" });
                throw new Error("本机保存未成功，不能离开");
              }
              resolve(true);
            },
            onCancel: () => resolve(false),
            footer: renderOfflineLeaveFooter.bind(null, discardAndLeave)
          });
        });
      }
    });
    return () => registerLeaveGuard?.(null);
  }, [registerLeaveGuard]);

  const applyLoadedDraft = (taskId: string, record: OfflineDraftRecord, latestEnvelopes: OfflineDraftEnvelope[]) => {
    setEnvelopes(latestEnvelopes);
    const metadata = latestEnvelopes.find((item) => item.taskId === taskId);
    const confirmed = confirmedTaskIds.current.has(taskId);
    // Blob 地址只属于本次 WebView 会话；解密草稿不包含图片，不能沿用上一任务的图像。
    setImagePreview(null);
    setPkg(record.packageSnapshot);
    setDraft(record.draft);
    setSavedSnapshot({ package: JSON.stringify(record.packageSnapshot), draft: JSON.stringify(record.draft) });
    if (record.submissionCommand) pendingCommands.current.set(taskId, record.submissionCommand);
    else pendingCommands.current.delete(taskId);
    setPendingCommand(Boolean(record.submissionCommand));
    setSelectedTaskId(taskId);
    setSyncStatus(confirmed ? "synced" : metadata?.syncStatus ?? record.syncStatus);
    setSyncMessage(confirmed ? "同步成功，服务端已接收人工评分。" : metadata?.syncMessage ?? record.syncMessage ?? "本地加密草稿已加载。");
  };

  const loadDraft = async (taskId: string) => {
    if (syncInFlight.current || draftSaveInFlight.current || packageBusy.current) return;
    if (!hasKey) {
      setSyncMessage("请输入本地离线密钥后再加载草稿。");
      return;
    }
    const requestId = ++packageRequestRef.current;
    packageBusy.current = true;
    setPackageLoading(true);
    try {
      if (editorRef.current.pkg && !editorRef.current.draftSaved && !await confirmPackageReplacement("当前评分输入尚未保存。加载本地草稿会替换编辑器中的输入；取消可继续保存当前评分。")) return;
      if (requestId !== packageRequestRef.current) return;
      // 原生和浏览器存储只更新状态元数据，不重写加密正文；正文内的状态可能早于服务端回执。
      const [record, latestEnvelopes] = await Promise.all([
        loadOfflineDraft(taskId, offlineKey, durableScopeKey),
        listOfflineDraftEnvelopes(durableScopeKey)
      ]);
      if (requestId !== packageRequestRef.current) return;
      if (!record) {
        setSyncMessage("未找到本地草稿。");
        return;
      }
      applyLoadedDraft(taskId, record, latestEnvelopes);
      await onLog("info", "offline draft decrypted and loaded", taskId);
    } catch (error) {
      if (requestId !== packageRequestRef.current) return;
      const message = `草稿解密失败：${formatError(error)}`;
      setSyncMessage(message);
      try { await onLog("warning", "offline draft decrypt failed", message); } catch {
        setSyncMessage(`${message} 本地日志保存失败。`);
      }
    } finally {
      if (requestId === packageRequestRef.current) {
        packageBusy.current = false;
        setPackageLoading(false);
      }
    }
  };

  // 初次打开可随可用租约重试；输入变化不触发重载，编辑器函数使用本次入口的会话快照。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 自动打开只随任务入口与租约触发，非稳定编辑器函数不能使已打开草稿反复重载。
  useEffect(() => {
    if (!initialTaskId) return;
    // 同一账号续期只更换读写租约，不重读已打开的任务；盘中旧分数不能覆盖尚未保存的输入。
    if (editorRef.current.pkg?.task.id === initialTaskId) return;
    let current = true;
    // 从任务列表或同步队列进入时优先恢复原评分；不能用重新下载的空草稿覆盖已保存输入。
    void loadOfflineDraft(initialTaskId, offlineKey, durableScopeKey).then(async (record) => {
      if (!current) return;
      setSelectedTaskId(initialTaskId);
      if (record) await loadDraft(initialTaskId);
      else if (token) await downloadPackage(initialTaskId);
      else setPackageError("该任务尚无本机草稿，请在线登录后下载任务材料。");
    }).catch((error) => { if (current) setPackageError(`任务打开失败：${formatError(error)}`); });
    return () => { current = false; };
  }, [initialTaskId, durableScopeKey]);

  const getSyncPackageSnapshot = (previous: OfflineGradeCommand | undefined, existing: OfflineDraftRecord | null, currentPackage: OfflineTaskPackage) => {
    // 待确认命令已有本地记录时，继续使用当时的任务包，避免换成刚下载的材料。
    return previous && existing ? existing.packageSnapshot : currentPackage;
  };

  const getSyncInputMessage = (recover = false) => {
    if (!hasKey) {
      return "本地离线密钥至少 8 个字符；未配置密钥时禁止同步草稿。";
    }
    if (alreadySynced || confirmedTaskIds.current.has(pkg?.task.id ?? "")) {
      return "该草稿已同步成功，已阻止重复提交。";
    }
    // 待确认操作使用已加密的原载荷，只核对回执时不要求重启后再次下载图片或重填评分。
    if (recover) return null;
    if (draft?.score == null) {
      return "请先填写最终分。";
    }
    if (answerMaterialRef.current.pkg !== pkg || !answerMaterialRef.current.available) {
      return "当前任务没有可核对的答卷文字或图片，请重新读取任务材料或联系管理员；已禁止提交。";
    }
    if (!Number.isFinite(draft.score) || draft.score < 0 || (maxScore > 0 && draft.score > maxScore)) {
      return "最终分必须在 0 到题目满分之间。";
    }
    const points = pkg?.question?.rubric?.points ?? [];
    const pointSum = points.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0);
    if (points.length && Math.abs(draft.score - pointSum) > 0.001 && !draft.reason.trim()) return "最终分与评分点合计不同，请填写人工调整原因后提交。";
    return null;
  };

  const buildDraftSyncCommand = (taskPackage: OfflineTaskPackage, previous: OfflineGradeCommand | undefined) => {
    const selections: RubricSelection[] = Object.entries(draft.rubricSelections)
      .filter(([, score]) => Number(score) > 0)
      .map(([point_id, score]) => ({ point_id, score: Number(score) }));
    const command: OfflineGradeCommand = previous ?? {
      id: crypto.randomUUID(), taskId: taskPackage.task.id, server: client.url("/"), tenantId: user?.tenant_id ?? "", actorId: user?.id ?? "",
      payload: { expected_revision: taskPackage.task.revision, score: Number(draft.score), rubric_selections: selections,
        comments: draft.comments, private_note: draft.privateNote, student_feedback: draft.studentFeedback, reason: draft.reason || "离线阅卷提交" }
    };
    // 恢复原载荷也须校验当前身份；不能把其他服务器或账号的命令重新加密到本次作用域。
    assertOfflineGradeCommandIdentity(command, taskPackage.task.id, client, user);
    return command;
  };

  const persistDraftSyncCommand = async (taskPackage: OfflineTaskPackage, assertCurrent: () => void) => {
    // 本地阶段完成前不能发出网络操作；读取、校验或加密失败均沿用尚未提交的提示。
    try {
      const existing = await loadOfflineDraft(taskPackage.task.id, offlineKey, durableScopeKey);
      assertCurrent();
      const previous = pendingCommands.current.get(taskPackage.task.id) ?? existing?.submissionCommand;
      const currentInputMessage = getSyncInputMessage(Boolean(previous));
      if (currentInputMessage) throw new Error(currentInputMessage);
      const command = buildDraftSyncCommand(taskPackage, previous);
      const now = new Date();
      const snapshot = getSyncPackageSnapshot(previous, existing, taskPackage);
      // 网络前将稳定命令 ID 与完整输入一起加密；失败或重启后不重新生成键或换输入。
      await saveOfflineDraft({
        taskId: taskPackage.task.id,
        anonymousCode: taskPackage.task.anonymous_code,
        savedAt: now.toISOString(),
        expiresAt: new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString(),
        syncStatus: "syncing",
        packageSnapshot: snapshot,
        draft: previous ? draftFromCommand(command) : draft,
        submissionCommand: command
      }, offlineKey, durableScopeKey);
      return { existing, previous, command, now, snapshot };
    } catch (error) {
      throw new Error(`草稿加密保存失败，尚未提交：${formatError(error)}`);
    }
  };

  const finishDraftSync = async (taskId: string, stored: boolean, outcome: OfflineDraftEnvelope["syncStatus"], message: string, packageRequestId: number) => {
    // 本地写入失败不能把服务端已确认的结果改成 failed，也不能因再次写状态失败而冒出未处理异常。
    const localWarnings: string[] = [];
    if (stored) {
      try {
        await updateOfflineDraftStatus(taskId, { syncStatus: outcome, syncMessage: message }, durableScopeKey);
      } catch (error) {
        localWarnings.push(`本地状态保存失败：${formatError(error)}`);
      }
    }
    try { await refreshEnvelopes(); } catch (error) {
      localWarnings.push(`本地列表刷新失败：${formatError(error)}`);
    }
    try {
      await onLog(outcome === "synced" ? "info" : "warning", `offline draft sync ${outcome}`, message);
    } catch (error) {
      localWarnings.push(`本地日志保存失败：${formatError(error)}`);
    }
    if (packageRequestId === packageRequestRef.current) setSyncMessage([message, ...localWarnings].join(" "));
  };

  const syncDraft = async () => {
    if (syncInFlight.current || draftSaveInFlight.current || packageBusy.current) return;
    if (!pkg) {
      setSyncMessage("请先下载或加载任务包。");
      return;
    }
    const inputMessage = getSyncInputMessage(pendingCommand || pendingCommands.current.has(pkg.task.id));
    if (inputMessage) {
      setSyncMessage(inputMessage);
      return;
    }
    syncInFlight.current = true;
    setSyncing(true);
    setSyncStatus("syncing");
    const packageRequestId = packageRequestRef.current;
    let stored = false;
    let outcome: OfflineDraftEnvelope["syncStatus"] = "failed";
    let message = "";
    const assertCurrent = () => {
      if (activeRequestScope.current !== requestScope || packageRequestId !== packageRequestRef.current) throw new Error("阅卷会话已变化，请在原账号下重新确认结果。");
    };
    try {
      try {
        const { existing, previous, command, now, snapshot } = await persistDraftSyncCommand(pkg, assertCurrent);
        stored = true;
        pendingCommands.current.set(pkg.task.id, command);
        assertCurrent();
        setSavedSnapshot({ package: JSON.stringify(snapshot), draft: JSON.stringify(previous ? draftFromCommand(command) : draft) });
        setPendingCommand(true);
        if (!token || !isOnline) throw new Error("当前未登录或离线，无法同步；加密草稿可稍后重试。");
        const result = await syncOfflineGradeCommand(client, command, Boolean(previous), assertCurrent, async () => {
          // 明确未接受的命令仍需重新发起评分；没有已加载材料时只完成回执核对，不再发送 POST。
          if (answerMaterialRef.current.pkg !== pkg || !answerMaterialRef.current.available) throw new Error("原操作未被服务器接受，当前没有可核对的答卷材料；请恢复图片或重新读取材料后继续提交。");
          return detectConflict(client, snapshot, user, assertCurrent);
        });
        assertCurrent();
        outcome = result.status;
        message = result.message;
        if (outcome === "synced") {
          confirmedTaskIds.current.add(pkg.task.id);
        } else if (result.resolved) {
          // 明确未接受或拒绝后才能解除冻结；若写盘失败，保留原命令，下次仍先核对回执。
          await saveOfflineDraft({ ...existing, taskId: pkg.task.id, anonymousCode: pkg.task.anonymous_code,
            savedAt: now.toISOString(), expiresAt: new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString(),
            syncStatus: outcome, syncMessage: message, packageSnapshot: snapshot, draft: draftFromCommand(command), submissionCommand: undefined
          }, offlineKey, durableScopeKey);
          assertCurrent();
          pendingCommands.current.delete(pkg.task.id);
          setPendingCommand(false);
        }
      } catch (error) {
        if (packageRequestId !== packageRequestRef.current) return;
        message = formatError(error);
      }
      if (packageRequestId !== packageRequestRef.current) return;
      setSyncStatus(outcome);
      await finishDraftSync(pkg.task.id, stored, outcome, message, packageRequestId);
    } finally {
      if (packageRequestId === packageRequestRef.current) { syncInFlight.current = false; setSyncing(false); }
    }
  };

  const purgeExpired = async () => {
    if (syncInFlight.current || draftSaveInFlight.current) return;
    const requestId = packageRequestRef.current;
    const expired = envelopes.filter((item) => new Date(item.expiresAt).getTime() <= Date.now());
    const removable = expired.filter((item) => item.syncStatus === "synced");
    if (!removable.length) { setSyncMessage(`没有可清理的过期已同步草稿；${expired.length} 份未同步草稿继续保留。`); return; }
    const confirmed = await new Promise<boolean>((resolve) => Modal.confirm({ title: `清理 ${removable.length} 份过期已同步草稿？`,
      content: <div><p>将永久删除这些本机评分与任务材料，服务端已接收的评分保持不变。过期未同步草稿和待确认提交均保留。</p>{removable.map((item) => <p key={item.taskId}>{item.anonymousCode} / 保存 {formatDate(item.savedAt)} / 到期 {formatDate(item.expiresAt)}</p>)}</div>,
      okText: "确认删除已同步本机草稿", cancelText: "保留草稿", onOk: () => resolve(true), onCancel: () => resolve(false) }));
    if (!confirmed || requestId !== packageRequestRef.current || syncInFlight.current || draftSaveInFlight.current) return;
    let count: number;
    try {
      count = await purgeExpiredOfflineDrafts(new Date(), durableScopeKey, offlineKey);
    } catch (error) {
      setSyncMessage(`本地缓存清理失败：${formatError(error)}`);
      return;
    }
    try {
      await refreshEnvelopes();
      await onLog("info", "expired offline drafts purged", `${count} drafts`);
      setSyncMessage(`已清理 ${count} 条过期本地缓存。`);
    } catch (error) {
      setSyncMessage(`已清理 ${count} 条过期本地缓存，但本地列表或日志更新失败：${formatError(error)}`);
    }
  };

  return (
    <div className="offline-workbench">
      <section className="panel full">
        <div className="offline-head">
          <div>
            <p className="eyebrow">离线阅卷</p>
            <h3>离线阅卷基础工作台</h3>
            <p>任务包读取任务绑定的题目与评分细则，点击保存草稿后写入本地加密存储。</p>
          </div>
          <OfflineWorkbenchStatusTags isOnline={isOnline} hasKey={hasKey} />
        </div>
        <Alert
          type="info"
          showIcon
          message={
            hasDurableDesktopStore()
              ? "点击保存后，任务数据、冻结细则与评分草稿加密写入本机，密钥由 Windows 凭据库保护。答卷图片仅缓存于本次会话，重启或加载草稿后可联网恢复图片，人工评分保持原文。"
              : "浏览器开发模式仅使用显式加密草稿回退，不能作为生产扫描站或上传恢复验证。"
          }
        />
        {pkg ? <MaterialSaveAlert pkg={pkg} packageSaved={packageSaved} draftSaved={draftSaved} hasImage={hasReadableImage} /> : null}
        <div className="offline-toolbar">
          {!hasDurableDesktopStore() && <Input.Password prefix={<KeyRound size={14} />} value={offlineKey} onChange={(event) => setOfflineKey(event.target.value)} placeholder="浏览器开发密钥，至少 8 位" />}
          <Button icon={<RefreshCw size={16} />} loading={loadingTasks} disabled={!token || !user} onClick={loadMyTasks}>
            获取我的任务
          </Button>
          <Button icon={<Trash2 size={16} />} disabled={syncing || savingDraft} onClick={purgeExpired}>
            清理过期已同步草稿
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
          <Table rowKey="id" size="small" columns={taskColumns(setSelectedTaskId, (id) => { setSelectedTaskId(id); void downloadPackage(id); }, !token || syncing || savingDraft || packageLoading)} dataSource={tasks} rowClassName={(task) => task.id === selectedTaskId ? "selected-review-task" : ""} pagination={{ pageSize: 6, showSizeChanger: false }} locale={{ emptyText: <Empty description="暂无任务" /> }} />
          {selectedTask ? <Alert className="section-alert" type="info" showIcon message={`当前选择：${selectedTask.anonymous_code} · 第 ${selectedTask.question_no ?? "待核对"} 题`} description={`${examNames[selectedTask.exam_id] ?? "考试名称尚未加载，请联网刷新任务核对"}。${reviewTaskState(selectedTask.status).detail}。打开其他任务前会核对当前草稿是否保存。`} /> : null}
          <Button className="section-button" type="primary" icon={<Download size={16} />} loading={packageLoading} disabled={!selectedTaskId || !token || syncing || savingDraft} onClick={() => void downloadPackage()}>
            {selectedTask ? `下载任务包：${selectedTask.anonymous_code} · 第 ${selectedTask.question_no ?? "待核对"} 题` : "下载任务包"}
          </Button>
          <Button className="section-button" icon={<RefreshCw size={16} />} loading={packageLoading} disabled={!pkg || !token || !isOnline || syncing || savingDraft || packageLoading} onClick={() => void restorePackageImage()}>
            恢复答卷图片
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
            renderItem={(item) => {
              const messageSuffix = item.syncMessage ? " / " + item.syncMessage : "";
              return (
                <List.Item actions={[<Button key="load" size="small" disabled={!hasKey || syncing || savingDraft} onClick={() => void loadDraft(item.taskId)}>加载</Button>]}>
                  <List.Item.Meta
                    title={
                      <Space wrap>
                        <span>{item.anonymousCode}</span>
                        <SyncTag status={item.syncStatus} />
                      </Space>
                    }
                    description={`${formatDate(item.savedAt)} / 过期 ${formatDate(item.expiresAt)}${messageSuffix}`}
                  />
                </List.Item>
              );
            }}
          />
        </section>
      </div>

      <div className="offline-grid workbench">
        <section className="panel">
          <PackageView pkg={pkg} imagePreview={imagePreview} onImageReadableChange={onImageReadableChange} />
        </section>
        <section className="panel">
          {pendingCommand && !alreadySynced ? <Alert type="warning" showIcon message="原评分结果尚待确认，分数和评语已冻结。继续同步会先核对原操作回执。" /> : null}
          <DraftEditor pkg={pkg} draft={draft} setDraft={setDraft} maxScore={maxScore} latestAiGrade={latestAiGrade} disabled={syncing || savingDraft || packageLoading || alreadySynced || pendingCommand} />
          <Space className="offline-actions" wrap>
            <Button icon={<Save size={16} />} loading={savingDraft} disabled={!pkg || !hasKey || syncing || savingDraft || packageLoading || alreadySynced || pendingCommand} onClick={() => void saveDraft()}>
              加密保存草稿
            </Button>
            <Button type="primary" icon={<Send size={16} />} loading={syncing} disabled={!pkg || (!hasAnswerMaterial && !pendingCommand) || !hasKey || syncing || savingDraft || packageLoading || alreadySynced} onClick={() => void syncDraft()}>
              同步提交
            </Button>
            <SyncTag status={syncStatus} />
            {alreadySynced && nextTask ? <Button disabled={!token || syncing || savingDraft || packageLoading} onClick={() => { setSelectedTaskId(nextTask.id); void downloadPackage(nextTask.id); }}>{`下一份：${nextTask.anonymous_code}`}</Button> : null}
          </Space>
          {syncMessage && <Alert className="section-alert" type={syncStatus === "conflict" || syncStatus === "failed" ? "warning" : "info"} message={syncMessage} showIcon />}
        </section>
      </div>
    </div>
  );
}

function OfflineWorkbenchStatusTags({ isOnline, hasKey }: Readonly<{ isOnline: boolean; hasKey: boolean }>) {
  return <Space wrap>
    <Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线"}</Tag>
    <Tag color={hasKey ? "success" : "warning"}>{hasKey ? "离线密钥已输入" : "未配置离线密钥"}</Tag>
  </Space>;
}

function MaterialSaveAlert({ pkg, packageSaved, draftSaved, hasImage }: Readonly<{
  pkg: OfflineTaskPackageWithAnswer;
  packageSaved: boolean;
  draftSaved: boolean;
  hasImage: boolean;
}>) {
  let materialDescription = "题目与评分细则来自任务冻结版本。";
  if (!pkg.question) materialDescription = "冻结题目与评分细则未获取。";
  else if (pkg.question.rubric?.status === "missing") materialDescription = "冻结评分细则缺失，任务包仅记录缺失状态。";
  const draftDescription = draftSaved ? "当前评分草稿已保存。" : "当前评分输入尚未保存。";
  const imageDescription = hasImage ? "仅在本次会话可用" : "尚未加载";
  const answerDescription = packageAnswerText(pkg) || hasImage ? "已有可核对答卷材料。" : "没有可核对答卷材料，不能提交。";
  return <Alert
    type={packageSaved && draftSaved ? "success" : "warning"}
    showIcon
    message={packageSaved ? "当前任务材料已加密保存" : "当前任务材料尚未保存"}
    description={`${materialDescription}${answerDescription}${draftDescription}答卷图片${imageDescription}，不随加密草稿恢复。`}
  />;
}

function taskColumns(selectTask: (id: string) => void, openTask: (id: string) => void, busy: boolean): TableColumnsType<ReviewTask> {
  return [
    { title: "匿名号", dataIndex: "anonymous_code" },
    { title: "题号", dataIndex: "question_no", width: 80 },
    { title: "状态", dataIndex: "status", width: 100, render: (value: string) => <Tag title={reviewTaskState(value).detail}>{reviewTaskState(value).label}</Tag> },
    {
      title: "操作",
      key: "action",
      width: 84,
      render: (_value, task) => (
        <Space><Button size="small" disabled={busy} onClick={() => selectTask(task.id)}>
          选择
        </Button><Button size="small" disabled={busy || !reviewTaskState(task.status).canGrade} onClick={() => openTask(task.id)}>打开阅卷</Button></Space>
      )
    }
  ];
}

function PackageView({ pkg, imagePreview, onImageReadableChange }: Readonly<{
  pkg: OfflineTaskPackageWithAnswer | null;
  imagePreview: OfflineAnswerPreview | null;
  onImageReadableChange: (preview: OfflineAnswerPreview, readable: boolean) => void;
}>) {
  const [revealedSecondOpinion, setRevealedSecondOpinion] = useState(false);
  useEffect(() => { setRevealedSecondOpinion(false); }, [pkg?.task.id, pkg?.downloadedAt]);
  const reportImageReadability = useCallback((readable: boolean) => {
    if (imagePreview) onImageReadableChange(imagePreview, readable);
  }, [imagePreview, onImageReadableChange]);
  if (!pkg) {
    return <Empty description="尚未下载任务包" />;
  }
  return (
    <div className="package-view">
      <div className="package-summary">
        <Tag color="blue">{pkg.task.anonymous_code}</Tag>
        <Tag title={reviewTaskState(pkg.task.status).detail}>{reviewTaskState(pkg.task.status).label}</Tag>
        <Tag>评分标准 {pkg.rubricVersion ?? "未记录"}</Tag>
        <Tag>{pkg.assessmentSource === "task_context" ? "任务冻结题目与细则" : "旧版细则来源未记录，请重新下载"}</Tag>
      </div>
      {pkg.warnings.length > 0 && <Alert type="warning" showIcon message="任务包不完整" description={pkg.warnings.join("；")} />}
      <div className="answer-preview">
        {imagePreview ? <AnswerDocumentViewer key={`${imagePreview.taskId}:${imagePreview.requestId}`} document={imagePreview} onReadableChange={reportImageReadability} /> : <Empty description={pkg.answerImageUrl ? "答案图片待下载/下载失败" : "任务未提供答卷图片入口"} />}
      </div>
      <h4>答题内容</h4>
      <pre>{packageAnswerText(pkg) || "未记录答卷文字"}</pre>
      <h4>任务题目</h4>
      <p>{pkg.question?.stem || "未记录题干"}</p>
      <h4>评分细则</h4>
      {pkg.question?.rubric?.status === "missing" ? <Alert type="warning" showIcon message="本任务缺少冻结评分细则，请联系管理员补核任务配置。" /> : null}
      <List
        size="small"
        dataSource={pkg.question?.rubric?.points ?? []}
        locale={{ emptyText: <Empty description="未获取评分标准" /> }}
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
      {pkg.aiSecondOpinion?.available && !pkg.aiSecondOpinion.score_prefill_allowed ? <>
        {/* 人工主导任务先独立核对答卷；辅助意见需明确展开，不能从其载荷恢复被禁止的建议分。 */}
        <Button onClick={() => setRevealedSecondOpinion((value) => !value)}>{revealedSecondOpinion ? "收起 AI 辅助意见" : "查看 AI 辅助意见"}</Button>
        {revealedSecondOpinion ? <OfflineSecondOpinionDetails metadata={pkg.aiSecondOpinion.metadata} /> : null}
      </> : null}
      {(pkg.aiCandidates?.length ?? 0) > 0 ? <>
        <h4>AI 识别候选</h4>
        <List size="small" dataSource={pkg.aiCandidates ?? []} renderItem={(candidate) => <List.Item>
          <div><Tag>{candidate.is_current ? "当前候选" : "历史候选"}</Tag><span>来源：{answerCandidateSource(candidate.source)}</span>
            {typeof candidate.confidence === "number" ? <span> · 置信度 {Math.round(candidate.confidence * 100)}%</span> : null}
            <pre>{candidate.display_text || "候选未提供可读文字"}</pre></div>
        </List.Item>} />
      </> : null}
    </div>
  );
}

function OfflineSecondOpinionDetails({ metadata }: Readonly<{ metadata: NonNullable<ReviewTaskContext["ai_second_opinion"]>["metadata"] }>) {
  // 这里只展示已展开的说明和置信度；建议分是否可采用仍由任务上下文的预填权限决定。
  return <div>
    {typeof metadata.teacher_note === "string" ? <p>{metadata.teacher_note}</p> : null}
    {typeof metadata.student_feedback === "string" ? <p>{metadata.student_feedback}</p> : null}
    {typeof metadata.confidence === "number" ? <p>辅助意见置信度 {Math.round(metadata.confidence * 100)}%</p> : null}
  </div>;
}

function DraftEditor({
  pkg,
  draft,
  setDraft,
  maxScore,
  latestAiGrade,
  disabled
}: Readonly<{
  pkg: OfflineTaskPackage | null;
  draft: OfflineGradeDraft;
  setDraft: React.Dispatch<React.SetStateAction<OfflineGradeDraft>>;
  maxScore: number;
  latestAiGrade?: AiGrade;
  disabled: boolean;
}>) {
  if (!pkg) {
    return <Empty description="下载任务包后填写草稿" />;
  }
  return (
    <Form layout="vertical" className="offline-draft-form" disabled={disabled}>
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
      {latestAiGrade && !latestAiGrade.mock && <Space wrap className="section-alert"><Button disabled={disabled} onClick={() => Modal.confirm({ title: "确认采用 AI 建议分？", content: `最终分将改为 ${latestAiGrade.suggested_score}。评分点与反馈保持原文，请人工核对评分依据。`, okText: "采用建议分", cancelText: "继续核对", onOk: () => setDraft((current) => current.taskId === pkg.task.id ? { ...current, score: latestAiGrade.suggested_score } : current) })}>采用 AI 建议分</Button>
        {latestAiGrade.student_feedback && <Button disabled={disabled} onClick={() => Modal.confirm({ title: "审读并采用 AI 学生反馈", content: <div><p>以下文字将替换学生可见反馈，分数和内部备注保持原文。发布后是否展示由学校发布策略决定。</p><p>{latestAiGrade.student_feedback}</p></div>, okText: "采用反馈并继续编辑", cancelText: "保留现有文字", onOk: () => setDraft((current) => current.taskId === pkg.task.id ? { ...current, studentFeedback: latestAiGrade.student_feedback ?? "" } : current) })}>审读 AI 反馈</Button>}</Space>}
      <Form.Item label="教师评语（阅卷记录，可选）">
        <Input.TextArea rows={3} value={draft.comments} onChange={(event) => setDraft((current) => ({ ...current, comments: event.target.value }))} />
      </Form.Item>
      <Form.Item label="学生可见反馈（可选，发布后按学校策略展示）">
        <Input.TextArea rows={2} value={draft.studentFeedback} onChange={(event) => setDraft((current) => ({ ...current, studentFeedback: event.target.value }))} />
      </Form.Item>
      <Form.Item label="内部备注（可选，阅卷教师及授权管理员可见）">
        <Input.TextArea rows={2} value={draft.privateNote} onChange={(event) => setDraft((current) => ({ ...current, privateNote: event.target.value }))} />
      </Form.Item>
      <Form.Item label="人工调整/提交原因（总分与评分点合计不同时必填）">
        <Input value={draft.reason} placeholder="说明人工调整评分的依据；其他情况可选" onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))} />
      </Form.Item>
    </Form>
  );
}

function packageAnswerText(pkg: OfflineTaskPackageWithAnswer) {
  // 旧加密包只有 ocrText；允许继续审读原文，但冻结细则来源仍由独立预检校验。
  return pkg.answerText?.trim() || pkg.ocrText?.trim() || "";
}

function assertTaskContext(context: ReviewTaskContext, task: ReviewTask) {
  if (context.task.id !== task.id || context.question.id !== task.question_id
    || context.task.answer_segment_id !== task.answer_segment_id
    || (context.answer_artifact && context.answer_artifact.answer_segment_id !== task.answer_segment_id)) {
    throw new Error("任务冻结上下文与所选任务不一致，请重新下载。");
  }
}

function taskContextAiGrades(context: ReviewTaskContext, tenantId: string): AiGrade[] {
  const opinion = context.ai_second_opinion;
  // 可采用的建议分仅来自任务上下文；人工主导策略没有授权预填时不能从其他证据字段补出分数。
  if (!opinion?.available || !opinion.score_prefill_allowed
    || (context.question_snapshot?.risk_tier === "R3" && context.question_snapshot.scoring_policy_snapshot?.mode === "HUMAN_PRIMARY")) return [];
  const metadata = opinion.metadata;
  const score = metadata.suggested_score;
  const maxScore = metadata.max_score ?? context.frozen_rubric.max_score;
  if (typeof score !== "number" || !Number.isFinite(score) || typeof maxScore !== "number" || !Number.isFinite(maxScore)
    || score < 0 || maxScore < 0 || score > maxScore) return [];
  return [{
    id: typeof metadata.id === "string" ? metadata.id : `task-ai-${context.task.id}`,
    tenant_id: tenantId, answer_segment_id: context.task.answer_segment_id, question_id: context.question.id,
    question_no: context.question.question_no, suggested_score: score, max_score: maxScore,
    confidence: typeof metadata.confidence === "number" ? metadata.confidence : 0,
    matched_points: [], missing_points: [], risk_flags: [], mock: metadata.mock === true,
    student_feedback: typeof metadata.student_feedback === "string" ? metadata.student_feedback : undefined,
    created_at: typeof metadata.created_at === "string" ? metadata.created_at : ""
  }];
}

function answerCandidateSource(source: string) {
  return ({ ocr: "文字识别", ocr_text: "识别文字", vlm: "图像识别", human: "人工核对", manual: "人工核对" } as Record<string, string>)[source] ?? source;
}

async function buildTaskPackage(client: DesktopApiClient, task: ReviewTask, tenantID: string): Promise<OfflineTaskPackageWithAnswer> {
  let context: Awaited<ReturnType<typeof getReviewTaskContext>>["context"] | undefined;
  const warnings: string[] = [];
  try {
    ({ context } = await getReviewTaskContext(client, task.id));
  } catch (error) {
    warnings.push(`任务材料读取失败：${formatError(error)}`);
  }
  if (context) assertTaskContext(context, task);

  // 只映射任务上下文里的冻结材料；考试题库、提交片段和文件管理接口都可能超出阅卷权限，不能回退读取。
  const frozenRubric = context?.frozen_rubric;
  const question: Question | undefined = context ? {
    ...context.question, tenant_id: tenantID, knowledge_points: context.question.knowledge_points ?? [],
    rubric: frozenRubric ? {
      ...frozenRubric,
      points: Array.isArray(frozenRubric.points) ? frozenRubric.points.map((point) => ({
        id: typeof point.id === "string" ? point.id : "", description: typeof point.description === "string" ? point.description : "", score: Number(point.score ?? 0), required: point.required === true
      })) : []
    } : undefined
  } : undefined;
  const artifact = context?.answer_artifact;
  const answerText = artifact?.raw_answer?.trim() || artifact?.ocr_text?.trim() || "";
  const answerImageUrl = artifact?.segment_image_url?.trim() || undefined;
  if (!context) warnings.push("未能获取任务冻结题目与评分细则。");
  else if (!question) warnings.push("未能获取任务冻结题目与评分细则。");
  else if (question.rubric?.status === "missing") warnings.push("本任务冻结评分细则缺失；未回退到当前题库。");
  if (!answerText && !answerImageUrl) warnings.push("未获取可核对的答卷文字或图片，请重新读取任务材料或联系管理员。");
  return {
    task: context?.task ?? task,
    ocrText: answerText,
    answerText,
    answerImageUrl,
    question,
    aiGrades: context ? taskContextAiGrades(context, tenantID) : [],
    aiCandidates: (context?.ai_candidates ?? []).filter((candidate) => candidate.answer_segment_id === task.answer_segment_id),
    aiSecondOpinion: context?.ai_second_opinion,
    warnings,
    downloadedAt: new Date().toISOString(),
    rubricVersion: question?.rubric?.version,
    ...(context ? { assessmentSource: "task_context" as const } : {})
  };
}

async function detectConflict(client: DesktopApiClient, pkg: OfflineTaskPackage, user: AuthUser | null, assertCurrent: () => void) {
  // 预检提供可读冲突原因；提交仍携带下载时的 revision，由服务端处理预检后的再次变更。
  if (!Number.isInteger(pkg.task.revision) || pkg.task.revision <= 0) {
    return "本地任务包缺少版本，请重新下载任务包后核对草稿。";
  }
  const latest = await getReviewTask(client, pkg.task.id);
  assertCurrent();
  if (latest.task.revision !== pkg.task.revision) {
    return "服务端任务版本已变化，请重新下载任务包后核对草稿。";
  }
  const status = latest.task.status;
  if (["submitted", "completed", "cancelled", "withdrawn", "revoked"].includes(status)) {
    const state = reviewTaskState(status);
    const nextStep = ["submitted", "completed"].includes(status)
      ? "请刷新任务列表核对服务端评分结果；如结果不符，请联系学校管理员。"
      : "请刷新任务列表，并联系学校管理员核对任务安排。";
    return `服务端任务${state.label}，本地草稿不能提交。${state.detail}。本地草稿仍保留。${nextStep}`;
  }
  if (latest.task.assigned_to && user?.id && latest.task.assigned_to !== user.id) {
    return "服务端任务已不再分配给当前用户。";
  }
  const questionResult = await getReviewTaskContext(client, pkg.task.id);
  assertCurrent();
  assertTaskContext(questionResult.context, pkg.task);
  if (pkg.assessmentSource !== "task_context") return "旧任务包未记录冻结细则来源，请重新下载任务包后核对草稿。";
  const latestRubricVersion = questionResult.context.frozen_rubric.version;
  if (pkg.rubricVersion && latestRubricVersion && latestRubricVersion !== pkg.rubricVersion) {
    return `评分标准版本变化：本地 ${pkg.rubricVersion}，服务端 ${latestRubricVersion}。`;
  }
  return null;
}

function draftFromCommand(command: OfflineGradeCommand): OfflineGradeDraft {
  const payload = command.payload;
  return { taskId: command.taskId, score: payload.score,
    rubricSelections: Object.fromEntries(payload.rubric_selections.map((selection) => [selection.point_id, selection.score])),
    comments: payload.comments, privateNote: payload.private_note, studentFeedback: payload.student_feedback, reason: payload.reason };
}

function packageAssessmentIdentity(pkg: OfflineTaskPackageWithAnswer) {
  return JSON.stringify({ task: { id: pkg.task.id, revision: pkg.task.revision, question_id: pkg.task.question_id, answer_segment_id: pkg.task.answer_segment_id, assigned_to: pkg.task.assigned_to }, question: pkg.question, answerText: pkg.answerText, answerImageUrl: pkg.answerImageUrl, source: pkg.assessmentSource });
}

function draftForDownloadedPackage(command: OfflineGradeCommand | undefined, previousDraft: OfflineGradeDraft, nextPackage: OfflineTaskPackage, sameTask: boolean, changedAssessment: boolean) {
  // 待确认命令优先恢复原输入；同一任务材料变化时只重置评分点，保留教师总分和评语。
  if (command) return draftFromCommand(command);
  if (!sameTask) return createInitialDraft(nextPackage);
  const nextDraft = { ...previousDraft };
  if (changedAssessment) {
    nextDraft.rubricSelections = Object.fromEntries((nextPackage.question?.rubric?.points ?? []).map((point) => [point.id, 0]));
  }
  return nextDraft;
}

function packageDownloadMessage(confirmed: boolean, changedAssessment: boolean, sameTask: boolean) {
  if (confirmed) return "该任务已同步成功，服务端已接收人工评分。";
  if (changedAssessment) return "已保留人工总分与评语，评分点已按新材料清空；请重新核对评分依据后保存和提交。";
  if (sameTask) return "任务材料已重新下载，人工评分与评语保持原文。";
  return null;
}

function createInitialDraft(pkg: OfflineTaskPackage): OfflineGradeDraft {
  const rubricSelections: Record<string, number> = {};
  for (const point of pkg.question?.rubric?.points ?? []) {
    rubricSelections[point.id] = 0;
  }
  return {
    taskId: pkg.task.id,
    // AI 只作为证据展示；教师显式采用前，最终分和学生反馈保持未填写状态。
    score: null,
    rubricSelections,
    comments: "",
    studentFeedback: "",
    privateNote: "",
    reason: ""
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
    reason: ""
  };
}

function latestGrade(grades: AiGrade[]) {
  return grades.filter((grade) => !grade.mock).sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0] ?? grades[0];
}

function SyncTag({ status }: Readonly<{ status: OfflineDraftEnvelope["syncStatus"] }>) {
  let color = "default";
  if (status === "synced") color = "success";
  else if (status === "conflict") color = "warning";
  else if (status === "failed") color = "error";
  else if (status === "syncing") color = "processing";
  return <Tag color={color}>{offlineSyncStatusLabels[status]}</Tag>;
}

function formatError(error: unknown) {
  return getUserErrorMessage(error);
}

function formatDate(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}
