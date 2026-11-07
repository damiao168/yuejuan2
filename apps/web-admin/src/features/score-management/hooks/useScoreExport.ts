import { useEffect, useRef } from "react";
import { getBusinessCommandSession } from "../../../api/businessCommand";
import type { ScoreExport } from "../../../api/scores";
import type { ScoreActionRunner } from "./useScoreAction";

type ExportOwner = { key: string; active: boolean; controller?: AbortController };

export function useScoreExport({ ownerKey, enabled, selectedExamId, runAction, onExport }: {
  ownerKey: string;
  enabled: boolean;
  selectedExamId: string;
  runAction: ScoreActionRunner;
  onExport: (result: ScoreExport, fallbackFilename: string) => void;
}) {
  const ownerRef = useRef<ExportOwner>({ key: ownerKey, active: true });
  if (ownerRef.current.key !== ownerKey) {
    // 在 effect 清理前先使旧归属失效；请求恰好在换考试的渲染后完成，也不能触发旧文件下载。
    ownerRef.current.active = false;
    ownerRef.current = { key: ownerKey, active: true };
  }
  const renderedOwner = ownerRef.current;
  useEffect(() => {
    const owner = renderedOwner;
    owner.active = true;
    const abort = () => owner.controller?.abort();
    window.addEventListener("business-command-scope-changed", abort);
    return () => {
      owner.active = false;
      abort();
      window.removeEventListener("business-command-scope-changed", abort);
    };
  }, [renderedOwner]);

  return async (key: string, request: (examId: string, signal: AbortSignal) => Promise<ScoreExport>, successText: string, fallbackFilename: string) => {
    const owner = ownerRef.current;
    if (!enabled || !selectedExamId || !owner.active || owner.key !== ownerKey || owner.controller) return;
    const session = getBusinessCommandSession();
    const controller = new AbortController();
    // 同步占用当前归属，防止按钮尚未重绘时双击发出第二份导出。
    owner.controller = controller;
    const isCurrent = () => owner === ownerRef.current && owner.active && !controller.signal.aborted && session === getBusinessCommandSession();
    try {
      await runAction(key, async () => {
        const result = await request(selectedExamId, controller.signal);
        // 即使服务端或测试传输忽略取消信号，也须在使用 Blob 和水印之前再次核对归属。
        if (isCurrent()) onExport(result, fallbackFilename);
      }, successText, isCurrent);
    } finally {
      if (owner.controller === controller) owner.controller = undefined;
    }
  };
}
