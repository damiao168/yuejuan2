import { recoverScoringCommand, startScoringRun } from "../../../../api/review";

type Storage = Pick<globalThis.Storage, "getItem" | "setItem" | "removeItem">;

export async function submitScoringCommand(options: {
  examId: string;
  storageKey: string;
  storage: Storage;
  ready: () => Promise<boolean>;
  pending: (id: string | null) => void;
}, transport = { recover: recoverScoringCommand, start: startScoringRun }) {
  let commandId = options.storage.getItem(options.storageKey);
  let accepted = false;
  if (commandId) {
    // 有待恢复命令时先查询原结果，不重新运行就绪检查或生成新 ID，避免响应丢失后重复建评分任务。
    const recovery = await transport.recover(options.examId, commandId);
    if (recovery.command_id !== commandId) throw new Error("评分命令恢复结果不匹配");
    accepted = recovery.status === "succeeded" && Boolean(recovery.scoring_run);
  } else {
    if (!await options.ready()) return false;
    commandId = `web-${crypto.randomUUID()}`;
    options.storage.setItem(options.storageKey, commandId);
    options.pending(commandId);
  }
  if (!accepted) await transport.start(options.examId, commandId);
  try {
    options.storage.removeItem(options.storageKey);
    options.pending(null);
  } catch { /* The accepted command remains safe to recover. */ }
  return true;
}
