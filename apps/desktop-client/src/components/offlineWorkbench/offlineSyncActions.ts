import type { RefObject } from 'react';
import { assertOfflineGradeCommandIdentity, syncOfflineGradeCommand } from '../../lib/offlineReviewCommand';
import { loadOfflineDraft, saveOfflineDraft, updateOfflineDraftStatus, type OfflineDraftEnvelope } from '../../lib/offlineStore';
import type { OfflineDraftRecord, OfflineGradeCommand, OfflineGradeDraft, OfflineTaskPackage, RubricSelection } from '../../types';
import { draftFromCommand } from './draftState';
import { detectConflict, formatError } from './taskPackage';
import type { OfflineTaskPackageWithAnswer, OfflineWorkbenchProps } from './types';

type RequestScope = Pick<OfflineWorkbenchProps, 'durableScopeKey' | 'client' | 'token'> & { actorId: string | undefined; tenantId: string | undefined };
type SyncContext = Pick<OfflineWorkbenchProps, 'durableScopeKey' | 'client' | 'token' | 'user' | 'isOnline' | 'onLog'> & {
  offlineKey: string;
  pkg: OfflineTaskPackageWithAnswer | null;
  draft: OfflineGradeDraft;
  setSyncStatus: (value: OfflineDraftEnvelope['syncStatus']) => void;
  setSyncMessage: (value: string | null) => void;
  setSyncing: (value: boolean) => void;
  pendingCommand: boolean;
  setPendingCommand: (value: boolean) => void;
  setSavedSnapshot: (value: { package: string; draft: string } | null) => void;
  pendingCommands: RefObject<Map<string, OfflineGradeCommand>>;
  packageRequestRef: RefObject<number>;
  packageBusy: RefObject<boolean>;
  syncInFlight: RefObject<boolean>;
  draftSaveInFlight: RefObject<boolean>;
  confirmedTaskIds: RefObject<Set<string>>;
  maxScore: number;
  hasKey: boolean;
  alreadySynced: boolean;
  answerMaterialRef: RefObject<{ pkg: OfflineTaskPackageWithAnswer | null; available: boolean }>;
  refreshEnvelopes: () => Promise<void>;
  requestScope: RequestScope;
  activeRequestScope: RefObject<RequestScope | null>;
};

// 这里只组织一次渲染的同步动作；租约、互斥锁和服务端确认集仍由原工作台 Hook 唯一持有。
// 网络操作前先加密固定命令，回执写盘失败也不撤销服务端已确认的事实。
export function createOfflineSyncActions({ offlineKey, pkg, draft, setSyncStatus, setSyncMessage, setSyncing, pendingCommand, setPendingCommand, setSavedSnapshot, pendingCommands, packageRequestRef, packageBusy, syncInFlight, draftSaveInFlight, confirmedTaskIds, maxScore, hasKey, alreadySynced, answerMaterialRef, refreshEnvelopes, requestScope, activeRequestScope, durableScopeKey, client, token, user, isOnline, onLog }: SyncContext) {


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
  return { syncDraft };
}
