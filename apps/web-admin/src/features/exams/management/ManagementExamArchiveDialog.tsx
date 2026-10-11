import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Modal, Space } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { archiveManagementExam, checkManagementExamArchive, getManagementExam, managementExamVersion, recoverManagementExamCommand, restoreManagementExam, type ExamArchiveBlocker, type ManagementExam } from "../../../api/exams";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { examSubjectLabel } from "../../../constants/examStatus";
import { isManagementCommandUncertain, loadManagementCommand, managementCommandKey, type ManagementArchiveCommand } from "./managementCommand";

function ArchiveBlockerNotice({ blockers, allowed }: Readonly<{ blockers: ExamArchiveBlocker[]; allowed: boolean }>) {
  if (blockers.length) {
    return <Alert type="warning" showIcon message="暂时不能归档" description={<ul>{blockers.map((blocker, index) => <li key={`${blocker.code}:${blocker.exam_id ?? index}`}>{blocker.subject ? `${examSubjectLabel(blocker.subject)}：` : ""}{blocker.message}</li>)}</ul>} />;
  }
  if (!allowed) return <Alert type="warning" showIcon message="当前考试或权限不满足操作条件，请刷新后检查。" />;
  return null;
}

function ArchiveExamDetails({ exam, archive, blockers, allowed }: Readonly<{ exam: ManagementExam; archive: boolean; blockers: ExamArchiveBlocker[]; allowed: boolean }>) {
  return <div className="detail-stack">
    <p>“{exam.name}”包含{exam.exams.map((subject) => examSubjectLabel(subject.subject)).join("、")}，共 {exam.exams.length} 科。</p>
    <p>{archive ? "本次归档整场考试。归档后考试及各学科业务内容只读，已有试卷、答卷、成绩和日志保留，学生仍可按原权限查看已发布成绩。可在“已归档”中恢复。" : "恢复后考试返回未归档列表，各学科保留归档前的业务阶段。已发布成绩和原有配置锁定规则继续生效。"}</p>
    <ArchiveBlockerNotice blockers={blockers} allowed={allowed} />
  </div>;
}

export function ManagementExamArchiveDialog({ selection, actorScope, onClose, onSaved }: Readonly<{
  selection: { exam: ManagementExam; action: "archive" | "restore" } | null;
  actorScope: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}>) {
  const { message } = App.useApp();
  const [exam, setExam] = useState<ManagementExam | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const [blockers, setBlockers] = useState<ExamArchiveBlocker[]>([]);
  const [allowed, setAllowed] = useState(false);
  const command = useRef<ManagementArchiveCommand | null>(null);
  const commandStorageKey = useRef("");
  const submitting = useRef(false);
  // 操作目标决定恢复读取；回调单独取最新值，避免父页面刷新时重开同一命令查询。
  const callbacks = useRef({ onClose, onSaved, success: message.success });
  callbacks.current = { onClose, onSaved, success: message.success };
  const key = selection ? managementCommandKey(actorScope, selection.exam.kind, selection.exam.id, selection.action) : "";

  useEffect(() => {
    let active = true;
    setExam(null);
    setError(null);
    setAllowed(false);
    setUncertain(false);
    setBlockers([]);
    command.current = null;
    if (!selection) return;
    setLoading(true);
    // 历史成功回执只触发刷新；同动作关闭弹窗，反向动作仍重新读取当前考试。
    const recoverPending = async (pending: ManagementArchiveCommand, pendingKey: string) => {
      const recovered = await recoverManagementExamCommand(pending.commandId);
      if (!active) return false;
      if (recovered.command.status === "succeeded") {
        try { localStorage.removeItem(pendingKey); } catch { /* 服务器成功事实不因本地清理失败变成未知。 */ }
        command.current = null;
        callbacks.current.success(`已确认上次${pending.action === "archive" ? "归档" : "恢复"}操作完成`);
        // 命令回执是历史结果；刷新当前考试，不能用旧归档结果覆盖随后执行的恢复。
        await callbacks.current.onSaved();
        if (pending.action === selection.action) {
          if (active) callbacks.current.onClose();
          return false;
        }
        return true;
      }
      setUncertain(true);
      if (recovered.command.status === "not_accepted") return true;
      setExam(selection.exam);
      setAllowed(true);
      setError("上次操作结果尚未确定，请稍后点击“检查并重试”。");
      return false;
    };
    const showLoadError = (currentError: unknown) => {
      setError(getUserErrorMessage(currentError, "无法读取考试归档条件"));
      if (command.current) { setExam(selection.exam); setUncertain(true); setAllowed(true); }
    };
    void (async () => {
      try {
        const oppositeKey = managementCommandKey(actorScope, selection.exam.kind, selection.exam.id, selection.action === "archive" ? "restore" : "archive");
        let pending = loadManagementCommand(localStorage, key);
        let pendingKey = key;
        if (!pending) { pending = loadManagementCommand(localStorage, oppositeKey); pendingKey = oppositeKey; }
        command.current = pending;
        commandStorageKey.current = pending ? pendingKey : key;
        if (pending && !await recoverPending(pending, pendingKey)) return;
        const result = await getManagementExam(selection.exam.kind, selection.exam.id);
        const check = selection.action === "archive" ? await checkManagementExamArchive(result.exam) : null;
        // 切换考试或关闭弹窗后，旧请求不能替新对象设置归档条件。
        if (!active) return;
        setExam(result.exam);
        setBlockers(check?.check.blockers ?? []);
        setAllowed(selection.action === "archive" ? result.exam.can_archive && Boolean(check?.check.allowed) : result.exam.can_restore);
      } catch (currentError) {
        if (active) showLoadError(currentError);
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => { active = false; };
  }, [selection, actorScope, key]);

  const complete = async () => {
    const completedAction = command.current?.action ?? selection?.action;
    try { localStorage.removeItem(commandStorageKey.current || key); } catch { /* 已完成回执留存时，下次只需查询恢复。 */ }
    command.current = null;
    message.success(completedAction === "archive" ? "整场考试已归档" : "整场考试已恢复");
    onClose();
    await onSaved();
  };

  const completeRecoveredSubmission = async (pending: ManagementArchiveCommand) => {
    const recovered = await recoverManagementExamCommand(pending.commandId);
    if (recovered.command.status === "succeeded") { await complete(); return true; }
    if (recovered.command.status !== "not_accepted") throw new Error("考试操作结果尚未确定，请稍后检查原操作");
    return false;
  };

  const createSubmission = (currentExam: ManagementExam, action: ManagementArchiveCommand["action"]) => {
    const created: ManagementArchiveCommand = { version: 1, commandId: crypto.randomUUID(), kind: currentExam.kind, examId: currentExam.id, action, input: managementExamVersion(currentExam), createdAt: new Date().toISOString() };
    // 本地记录先于提交；存储失败时不发起操作，否则刷新后会失去恢复原输入的依据。
    localStorage.setItem(key, JSON.stringify(created));
    command.current = created;
    commandStorageKey.current = key;
    return created;
  };

  const handleSubmitFailure = (currentError: unknown, mutationAttempted: boolean) => {
    // 恢复查询被登录或权限拒绝，不代表之前的提交失败；只有本次明确写拒绝才能清除旧命令。
    const ambiguous = !mutationAttempted || isManagementCommandUncertain(currentError);
    setUncertain(ambiguous && Boolean(command.current));
    if (!ambiguous) {
      try { localStorage.removeItem(commandStorageKey.current || key); } catch { /* 保留记录只会再次查询已知命令，不重新生成输入。 */ }
      command.current = null;
      setAllowed(false);
    }
    const recoveryHint = ambiguous && command.current ? "。原操作已保存，可关闭后重新打开以检查结果。" : "。请关闭弹窗并刷新后重新检查。";
    setError(getUserErrorMessage(currentError, "考试操作失败，请稍后重试") + recoveryHint);
  };

  const submit = async () => {
    if (!selection || !exam || !allowed || submitting.current) return;
    submitting.current = true;
    setSaving(true);
    setError(null);
    let mutationAttempted = false;
    try {
      let pending = command.current;
      if (pending && await completeRecoveredSubmission(pending)) return;
      pending ??= createSubmission(exam, selection.action);
      // 恢复查询确认没有接收后，仍复用原版本和原命令键；后续变化由服务端并发校验拒绝。
      const target = { kind: pending.kind, id: pending.examId };
      mutationAttempted = true;
      if (pending.action === "archive") await archiveManagementExam(target, pending.input, pending.commandId);
      else await restoreManagementExam(target, pending.input, pending.commandId);
      await complete();
    } catch (currentError) {
      handleSubmitFailure(currentError, mutationAttempted);
    } finally {
      submitting.current = false;
      setSaving(false);
    }
  };

  const archive = selection?.action === "archive";
  let submitLabel = archive ? "归档考试" : "恢复考试";
  if (uncertain) submitLabel = "检查并重试";
  let content = null;
  if (loading) content = <LoadingState label="正在检查整场考试" />;
  else if (exam) content = <ArchiveExamDetails exam={exam} archive={archive} blockers={blockers} allowed={allowed} />;
  return <Modal title={`${archive ? "归档" : "恢复"}整场考试`} open={Boolean(selection)} onCancel={() => { if (!saving) onClose(); }} closable={!saving} maskClosable={!saving}
    footer={<Space><Button disabled={saving} onClick={onClose}>取消</Button><Button type="primary" danger={archive} loading={saving} disabled={loading || !allowed} onClick={() => void submit()}>{submitLabel}</Button></Space>}>
    {content}
    {error ? <ErrorState message={error} /> : null}
  </Modal>;
}
