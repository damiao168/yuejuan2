import { useEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";
import type { BusinessCommandReceipt } from "@edugrade/sdk";
import { apiClient, getUserErrorMessage } from "../api/client";
import { captureBusinessCommandSession, inspectPersistedBusinessCommand, isBusinessCommandSessionCurrent, resumePersistedBusinessCommand, type BusinessCommandSession } from "../api/businessCommand";

const operations: Record<string, { label: string; path: (id: string) => string }> = {
  "review.submit": { label: "人工评分", path: id => `/api/v1/review-tasks/${id}/submit` },
  "review.arbitrate": { label: "仲裁评分", path: id => `/api/v1/arbitration-tasks/${id}/submit` },
  "score.confirm": { label: "成绩确认", path: id => `/api/v1/exams/${id}/confirm-grades` },
  "score.publish": { label: "成绩发布", path: id => `/api/v1/exams/${id}/publish` },
  "report.export": { label: "报表导出", path: id => `/api/v1/exams/${id}/reports/export` }
};

function objectReadPath(operation: string, target: string): string {
  if (operation.startsWith("score.") || operation.startsWith("report.")) return `/api/v1/exams/${encodeURIComponent(target)}`;
  if (operation === "review.arbitrate") return `/api/v1/arbitration-tasks/${encodeURIComponent(target)}`;
  return `/api/v1/review-tasks/${encodeURIComponent(target)}`;
}

function businessPagePath(operation: string, target: string): string {
  if (operation.startsWith("score.")) return `/exams/${encodeURIComponent(target)}/scores`;
  if (operation.startsWith("report.")) return `/exams/${encodeURIComponent(target)}/reports`;
  if (operation === "review.arbitrate") return `/arbitration?task=${encodeURIComponent(target)}`;
  return `/grading?task=${encodeURIComponent(target)}`;
}

function completionText(label: string, target: string, score?: number): string {
  const scoreText = typeof score === "number" ? ` · 得分 ${score}` : "";
  return `${label}已完成 · 对象 ${target}${scoreText}`;
}

type ReadableCommandObject = {
  exam?: { name?: string };
  task?: { exam_name?: string; question_no?: string; anonymous_code?: string };
  arbitration_task?: { question_no?: string; anonymous_code?: string };
};

function readableObjectLabel(object: ReadableCommandObject, fallback: string): string {
  if (object.exam?.name) return object.exam.name;
  const task = object.task ?? object.arbitration_task;
  return [object.task?.exam_name, task?.question_no ? `第 ${task.question_no} 题` : "", task?.anonymous_code ? `答卷 ${task.anonymous_code}` : ""].filter(Boolean).join(" · ") || fallback;
}

type CompletedCommand = { key: string; text: string; path: string; commandId?: string };

function pendingCommandKeys(prefix: string) {
  // 存储被禁用时主界面仍可挂载；没有读到记录不代表服务端原命令已经完成。
  try { return Object.keys(localStorage).filter(key => key.startsWith(prefix)); }
  catch { return []; }
}

function decodeReportBlob(content: string, contentType: string): Blob {
  // atob 返回 0-255 的单字节字符，每项都有索引 0，不会出现 Unicode 代理对。
  return new Blob([Uint8Array.from(atob(content), char => char.codePointAt(0)!)], { type: contentType });
}

export function PendingBusinessCommands({ tenant, actor, onNavigate }: Readonly<{ tenant: string; actor: string; onNavigate?: (path: string) => void }>) {
  const prefix = `business-command:${tenant}:${actor}:`;
  const [keys, setKeys] = useState<string[]>([]);
  const [busy, setBusy] = useState("");
  const busyRef = useRef(false);
  const mountedRef = useRef(false);
  const [error, setError] = useState("");
  const [receipts, setReceipts] = useState<Record<string, BusinessCommandReceipt>>({});
  const [outcomes, setOutcomes] = useState<CompletedCommand[]>([]);
  const [labels, setLabels] = useState<Record<string, string>>({});
  const inspected = useRef(new Set<string>());
  const automaticAttempts = useRef(new Map<string, number>());
  const [sessionRevision, setSessionRevision] = useState(0);
  useEffect(() => {
    mountedRef.current = true;
    const refresh = () => setKeys(pendingCommandKeys(prefix));
    const sessionChanged = () => {
      busyRef.current = false;
      setBusy("");
      setError("");
      setReceipts({});
      setOutcomes([]);
      setLabels({});
      inspected.current.clear();
      automaticAttempts.current.clear();
      setSessionRevision(current => current + 1);
      refresh();
    };
    refresh();
    window.addEventListener("business-command-changed", refresh);
    window.addEventListener("storage", refresh);
    window.addEventListener("business-command-scope-changed", sessionChanged);
    return () => {
      mountedRef.current = false;
      window.removeEventListener("business-command-changed", refresh);
      window.removeEventListener("storage", refresh);
      window.removeEventListener("business-command-scope-changed", sessionChanged);
    };
  }, [prefix]);

  // 自动恢复只读回执，逐条核对以免同时请求过多；续发冻结内容仍须用户明确点击。
  // 同一键若存入新命令 ID，必须重新核对；锁屏或换账号后的旧 Promise 由会话代次隔离。
  // biome-ignore lint/correctness/useExhaustiveDependencies: resume 使用当前会话；只在队列、忙碌状态或会话代次变化时自动核对一次。
  useEffect(() => {
    if (busyRef.current) return;
    for (const key of keys) {
      if (!key.startsWith(prefix)) continue;
      let identity: string;
      try { identity = `${key}:${localStorage.getItem(key) ?? ""}`; }
      catch { return; }
      if (inspected.current.has(identity)) continue;
      inspected.current.add(identity);
      automaticAttempts.current.set(identity, (automaticAttempts.current.get(identity) ?? 0) + 1);
      void resume(key);
      break;
    }
  }, [keys, busy, sessionRevision, prefix]);

  useEffect(() => {
    if (busy) return;
    const retry = keys.flatMap(key => {
      const receipt = receipts[key];
      if (!receipt || !["processing", "unknown"].includes(receipt.status)) return [];
      try {
        const identity = `${key}:${localStorage.getItem(key) ?? ""}`;
        return (automaticAttempts.current.get(identity) ?? 0) < 3 ? [identity] : [];
      } catch { return []; }
    });
    if (!retry.length) return;
    // 最多三轮、间隔十五秒，之后保留人工刷新；不因后台窗口恢复而无限轮询或重新提交。
    const timer = window.setTimeout(() => {
      retry.forEach(identity => inspected.current.delete(identity));
      setSessionRevision(current => current + 1);
    }, 15000);
    return () => window.clearTimeout(timer);
  }, [keys, receipts, busy]);

  async function inspectPendingCommand(key: string, operation: string, target: string, label: string, expectedSession: BusinessCommandSession, isCurrent: () => boolean) {
    const receipt = await inspectPersistedBusinessCommand(operation, target, expectedSession);
    if (!isCurrent()) return;
    setReceipts(current => ({ ...current, [key]: receipt }));
    const result = receipt.result as { task?: { score?: number }; human_grade?: { score?: number }; final_grade?: { score?: number }; status?: string } | undefined;
    const score = result?.human_grade?.score ?? result?.final_grade?.score;
    // 名称只是辅助信息，读取悬挂也不能锁住已取得回执的队列；仍沿用原对象权限和会话代次。
    void apiClient.request<ReadableCommandObject>(objectReadPath(operation, target)).then(object => {
      if (!isCurrent()) return;
      const name = readableObjectLabel(object, target);
      setLabels(current => ({ ...current, [key]: name }));
      setOutcomes(current => current.map(item => item.key === key && item.commandId === receipt.command_id ? { ...item, text: completionText(label, name, score) } : item));
    }).catch(() => { /* 名称不可读时保留精确对象号，回执事实和后续核对继续。 */ });
    if (!isCurrent()) return;
    if (receipt.status !== "succeeded") return;
    // 成功回执保留在本次界面供核对；本地仅移除同一个命令，不能清掉后来页面创建的新命令。
    try {
      const raw = localStorage.getItem(key);
      if (raw && (JSON.parse(raw) as { id: string }).id === receipt.command_id) localStorage.removeItem(key);
    } catch { /* 已确认成功事实仍展示，清理失败的记录留待恢复。 */ }
    setKeys(pendingCommandKeys(prefix));
    // 本地待确认键删除后，成功回执仍保留可读对象和业务入口，便于核对实际成绩或任务。
    setOutcomes(current => [...current.filter(item => item.key !== key), { key, commandId: receipt.command_id, text: completionText(label, target, score), path: businessPagePath(operation, target) }]);
  }

  async function resume(key: string, continueOriginal = false) {
    // ref 在本次点击内立即上锁，防止 React 更新按钮状态前重复恢复同一个命令。
    if (busyRef.current) return;
    let expectedSession: BusinessCommandSession;
    try { expectedSession = captureBusinessCommandSession(tenant, actor); } catch { return; }
    const isCurrent = () => mountedRef.current && isBusinessCommandSessionCurrent(expectedSession);
    busyRef.current = true;
    setBusy(key); setError("");
    try {
      const [operation, ...targetParts] = key.slice(prefix.length).split(":");
      const target = targetParts.join(":");
      const entry = operations[operation];
      if (!entry) throw new Error("无法识别待确认操作");
      if (!continueOriginal) {
        await inspectPendingCommand(key, operation, target, entry.label, expectedSession, isCurrent);
        return;
      }
      if (operation === "report.export") {
        const file = await resumePersistedBusinessCommand(operation, target,
          id => apiClient.requestBlob(entry.path(encodeURIComponent(target)), { method: "POST", headers: { "Idempotency-Key": id } }),
          resultValue => {
            const result = resultValue as { Content: string; ContentType: string; Filename: string; Watermark?: string };
            return {
              blob: decodeReportBlob(result.Content, result.ContentType),
              contentType: result.ContentType,
              filename: result.Filename,
              watermark: result.Watermark,
            };
          }, expectedSession);
        if (!isCurrent()) return;
        const url = URL.createObjectURL(file.blob);
        const link = document.createElement("a"); link.href = url; link.download = file.filename ?? "report.csv"; link.click();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      } else await resumePersistedBusinessCommand(operation, target,
        (id, original) => apiClient.request(entry.path(encodeURIComponent(target)), { method: "POST", headers: { "Idempotency-Key": id }, body: JSON.stringify(original) }),
        () => undefined, expectedSession);
      if (isCurrent()) {
        setKeys(pendingCommandKeys(prefix));
        setOutcomes(current => [...current.filter(item => item.key !== key), { key, text: completionText(entry.label, labels[key] || target), path: businessPagePath(operation, target) }]);
      }
    } catch (cause) {
      if (isCurrent()) setError(getUserErrorMessage(cause, "确认操作结果失败，请稍后继续"));
    } finally {
      // 换会话后已有新恢复操作时，旧 Promise 不能解开新操作的锁或清除它的等待状态。
      if (isCurrent()) { busyRef.current = false; setBusy(""); }
    }
  }
  if (!keys.length && !outcomes.length) return null;
  return <Alert type={keys.length ? "warning" : "success"} showIcon message={keys.length ? "提交结果待确认" : "操作结果已确认"} description={<Space direction="vertical">{keys.map(key => {
    const [operation, ...parts] = key.slice(prefix.length).split(":");
    const receipt = receipts[key];
    const payload = receipt?.payload as { score?: number; reason?: string; comment?: string; expected_revision?: number; expected_task_revision?: number } | undefined;
    const statusLabels: Record<string, string> = { processing: "正在处理，请稍后查看结果。", takeover_ready: "可以继续提交此前内容。", unknown: "暂无法确认结果", not_accepted: "此次提交未被接收，请返回原页面重新提交。", rejected: "原操作已拒绝，请返回业务页面核对", succeeded: "已完成" };
    return <div key={key}>
      <strong>{operations[operation]?.label ?? "操作"}</strong><span> · {operation.startsWith("score.") || operation.startsWith("report.") ? "考试" : "任务"} {labels[key] ?? parts.join(":")}</span>
      <Button disabled={Boolean(busy)} loading={busy === key} onClick={() => void resume(key)}>查看操作结果</Button>
      {onNavigate ? <Button disabled={Boolean(busy)} onClick={() => onNavigate(businessPagePath(operation, parts.join(":")))}>返回对应业务页面</Button> : null}
      {receipt ? <div><output aria-live="polite">{statusLabels[receipt.status] ?? "未知结果"}</output>
        {payload ? <div>原提交内容：{typeof payload.score === "number" ? `评分 ${payload.score}；` : ""}{payload.reason || payload.comment || "无文字说明"}{payload.expected_revision || payload.expected_task_revision ? `；版本 ${payload.expected_revision ?? payload.expected_task_revision}` : ""}</div> : null}
        {receipt.status === "takeover_ready" && "payload" in receipt ? <Button disabled={Boolean(busy)} onClick={() => void resume(key, true)}>按原内容继续提交</Button> : null}
      </div> : null}
    </div>;
  })}{Object.entries(receipts).filter(([key, receipt]) => key.includes(":report.export:") && receipt.status === "succeeded").map(([key, receipt]) => <Button key={key} onClick={() => {
    try {
      captureBusinessCommandSession(tenant, actor);
      const result = receipt.result as { Content: string; ContentType: string; Filename: string };
      const url = URL.createObjectURL(decodeReportBlob(result.Content, result.ContentType));
      const link = document.createElement("a"); link.href = url; link.download = result.Filename; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (cause) { setError(getUserErrorMessage(cause, "下载已完成的报表失败")); }
  }}>下载已完成的报表</Button>)}{outcomes.map(outcome => <div key={outcome.key}><output aria-live="polite">{outcome.text}</output>{onNavigate ? <Button disabled={Boolean(busy)} onClick={() => onNavigate(outcome.path)}>返回已完成操作的业务页面</Button> : null}</div>)}{outcomes.length ? <Button onClick={() => { setOutcomes([]); setReceipts({}); }}>关闭已确认结果</Button> : null}{error && <span role="alert">{error}</span>}</Space>} />;
}
