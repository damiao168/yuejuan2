import { useState } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { isStepUpCancelledError } from "../../../auth/stepUpContext";

type ScoreActionOptions = Readonly<{
  selectedExamId: string;
  loadExamList: () => Promise<void>;
  loadScores: (examId: string) => Promise<void>;
}>;
type ActionMessage = ReturnType<typeof App.useApp>["message"];

function formatError(error: unknown) {
  if (error instanceof ApiClientError) console.error("请求失败", error.status, error.code, error.message);
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

async function handleActionFailure(key: string, error: unknown, options: ScoreActionOptions, message: ActionMessage) {
  if (isStepUpCancelledError(error)) return;
  const outcomeUnknown = !(error instanceof ApiClientError) || error.status >= 500;
  const publishing = key === "publish" || key === "release-publish";
  if (!publishing || !outcomeUnknown) {
    message.error(formatError(error));
    return;
  }
  // 发布请求可能已在服务端完成；网络失败后先重读两条流程状态，不能直接提示“发布失败”。
  await options.loadExamList();
  if (options.selectedExamId) await options.loadScores(options.selectedExamId);
  message.warning("发布结果未确认，已尝试刷新状态。请核对当前状态后再决定是否重试。");
}

export function useScoreAction(options: ScoreActionOptions) {
  const { message } = App.useApp();
  const [actioning, setActioning] = useState<string | null>(null);
  const runAction = async (key: string, action: () => Promise<void>, successText: string) => {
    setActioning(key);
    try {
      await action();
      message.success(successText);
      if (options.selectedExamId) await options.loadScores(options.selectedExamId);
    } catch (error) {
      await handleActionFailure(key, error, options, message);
    } finally {
      setActioning(null);
    }
  };
  return { actioning, setActioning, runAction };
}
