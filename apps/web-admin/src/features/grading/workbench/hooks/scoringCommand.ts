import { recoverScoringCommand, startScoringRun } from "../../../../api/review";

// 默认适配器只保存接口函数，命令状态仍由每次调用的 options 和局部变量承载。
const defaultScoringTransport = { recover: recoverScoringCommand, start: startScoringRun };

type Storage = Pick<globalThis.Storage, "getItem" | "setItem" | "removeItem">;

export async function submitScoringCommand(options: {
  examId: string;
  storageKey: string;
  storage: Storage;
  ready: () => Promise<boolean>;
  isCurrent?: () => boolean;
  pending: (id: string | null) => void;
}, transport = defaultScoringTransport) {
  if (options.isCurrent && !options.isCurrent()) return false;
  let commandId = options.storage.getItem(options.storageKey);
  let accepted = false;
  if (commandId) {
    // 有待恢复命令时先查询原结果，不重新运行就绪检查或生成新 ID，避免响应丢失后重复建评分任务。
    const recovery = await transport.recover(options.examId, commandId);
    if (options.isCurrent && !options.isCurrent()) return false;
    if (recovery.command_id !== commandId) throw new Error("评分命令恢复结果不匹配");
    accepted = recovery.status === "succeeded" && Boolean(recovery.scoring_run);
  } else {
    if (!await options.ready()) return false;
    if (options.isCurrent && !options.isCurrent()) return false;
    commandId = `web-${crypto.randomUUID()}`;
    options.storage.setItem(options.storageKey, commandId);
    options.pending(commandId);
  }
  // 原命令查询可能跨过登录会话切换；写请求前再次确认作用域，保留命令供原身份恢复。
  if (options.isCurrent && !options.isCurrent()) return false;
  if (!accepted) await transport.start(options.examId, commandId);
  if (options.isCurrent && !options.isCurrent()) return false;
  try {
    options.storage.removeItem(options.storageKey);
    options.pending(null);
  } catch { /* 已接受的命令仍可安全恢复。 */ }
  return true;
}
