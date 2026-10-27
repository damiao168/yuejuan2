// 请求代次、会话租约和互斥 ref 在同一个控制器中拥有，避免分散 hooks 后由旧 finally 释放新操作。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Modal } from 'antd';
import { listExams } from '../../api/exams';
import { downloadReviewTaskImage, getReviewTask, getReviewTaskContext, listReviewTasks } from '../../api/review';
import { loadOfflineDraft, listOfflineDraftEnvelopes, purgeExpiredOfflineDrafts, readOfflineDraftEnvelopes, saveOfflineDraft, updateOfflineDraftStatus, type OfflineDraftEnvelope } from '../../lib/offlineStore';
import { hasDurableDesktopStore } from '../../lib/durableStore';
import { assertOfflineGradeCommandIdentity, syncOfflineGradeCommand } from '../../lib/offlineReviewCommand';
import { reviewTaskState } from '../../statusLabels';
import type { OfflineDraftRecord, OfflineGradeDraft, OfflineGradeCommand, OfflineTaskPackage, ReviewTask, RubricSelection } from '../../types';
import type { OfflineAnswerPreview, OfflineTaskPackageWithAnswer, OfflineWorkbenchProps } from './types';
import { selectedOfflineReviewTask, packageAnswerText, assertTaskContext, buildTaskPackage, detectConflict, packageAssessmentIdentity, latestGrade, formatError } from './taskPackage';
import { draftFromCommand, draftForDownloadedPackage, packageDownloadMessage, emptyDraft } from './draftState';
import { formatDate, renderOfflineLeaveFooter } from './OfflineWorkbenchView';

export function useOfflineWorkbench({ durableScopeKey, client, token, user, isOnline, onLog, registerLeaveGuard, initialTaskId }: Readonly<OfflineWorkbenchProps>) {
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

  return {
    isOnline,
    hasKey,
    pkg,
    packageSaved,
    draftSaved,
    hasReadableImage,
    offlineKey,
    setOfflineKey,
    loadingTasks,
    token,
    user,
    loadMyTasks,
    syncing,
    savingDraft,
    purgeExpired,
    taskError,
    setSelectedTaskId,
    downloadPackage,
    packageLoading,
    tasks,
    selectedTaskId,
    selectedTask,
    examNames,
    restorePackageImage,
    packageError,
    envelopes,
    loadDraft,
    imagePreview,
    onImageReadableChange,
    pendingCommand,
    alreadySynced,
    draft,
    setDraft,
    maxScore,
    latestAiGrade,
    hasAnswerMaterial,
    saveDraft,
    syncDraft,
    syncStatus,
    nextTask,
    syncMessage
  };
}

export type OfflineWorkbenchModel = ReturnType<typeof useOfflineWorkbench>;
