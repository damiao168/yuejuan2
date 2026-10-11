import type { DesktopApiClient } from "../api/client";
import { getReviewCommand, submitHumanGrade } from "../api/review";
import type { AuthUser, OfflineGradeCommand } from "../types";

export function assertOfflineGradeCommandIdentity(command: OfflineGradeCommand, taskId: string, client: DesktopApiClient, user: AuthUser | null) {
  const message = "待确认评分属于其他服务器或账号，请切回原账号核对；当前账号不会提交它。";
  // 空用户必须拒绝；先执行原有命令和服务器检查，再读取已存在用户的身份字段。
  if (!command.id || command.taskId !== taskId || command.server !== client.url("/") || !user) {
    throw new Error(message);
  }
  if (command.actorId !== user.id || command.tenantId !== user.tenant_id) {
    throw new Error(message);
  }
}

type Outcome = { status: "synced" | "conflict" | "failed"; message: string; resolved: boolean };
type ReceiptDecision = { outcome: Outcome | null; needsPreflight: boolean };

/** 先查原命令回执，再决定是否发送冻结载荷；调用方负责在网络前加密保存并检查页面会话。 */
export async function syncOfflineGradeCommand(
  client: DesktopApiClient,
  command: OfflineGradeCommand,
  recover: boolean,
  assertCurrent: () => void,
  detectConflict: () => Promise<string | null>
): Promise<Outcome> {
  assertCurrent();
  let needsPreflight = true;
  if (recover) {
    const receipt = await getReviewCommand(client, command.id);
    assertCurrent();
    const decision = inspectOfflineGradeReceipt(receipt, command);
    if (decision.outcome) return decision.outcome;
    needsPreflight = decision.needsPreflight;
  }
  if (needsPreflight) {
    const conflict = await detectConflict();
    assertCurrent();
    if (conflict) return { status: "conflict", message: conflict, resolved: true };
  }
  assertCurrent();
  await submitHumanGrade(client, command.taskId, command.payload, command.id);
  assertCurrent();
  return { status: "synced", message: "同步成功，服务端已接收人工评分。", resolved: false };
}

function inspectOfflineGradeReceipt(receipt: Awaited<ReturnType<typeof getReviewCommand>>, command: OfflineGradeCommand): ReceiptDecision {
  // 普通复核入口也可能由盲测 hook 接管；恢复时保留统一结果，不能据此向评分者展示参考分。
  if (receipt.command_id !== command.id || (receipt.operation && !["review.submit", "review.seed-submit"].includes(receipt.operation)) || (receipt.target_id && receipt.target_id !== command.taskId)) {
    throw new Error("评分回执与原操作不匹配，请保留草稿并联系管理员核对。");
  }
  if (receipt.status === "succeeded") {
    const result = receipt.result as { task?: { id?: string } } | undefined;
    if (result?.task?.id !== command.taskId) throw new Error("评分回执缺少原任务结果，请保留草稿并稍后核对。");
    return {
      outcome: { status: "synced", message: "同步成功，已从原操作回执确认人工评分。", resolved: false },
      needsPreflight: false
    };
  }
  if (receipt.status === "rejected") {
    return {
      outcome: { status: "failed", message: "原评分操作已被服务器明确拒绝，请重新核对后提交。", resolved: true },
      needsPreflight: false
    };
  }
  if (!["not_accepted", "takeover_ready"].includes(receipt.status)) {
    throw new Error(receipt.status === "processing" ? "原评分仍在处理中，请稍后继续确认；当前不会重复提交。" : "原评分结果暂时无法确认，请保留草稿并稍后继续确认。");
  }
  // 过期预留只允许用原 ID 和加密保存的原载荷接管；不能换 revision 或编辑后的分数。
  return { outcome: null, needsPreflight: receipt.status === "not_accepted" };
}
