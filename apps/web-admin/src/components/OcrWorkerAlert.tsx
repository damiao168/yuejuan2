import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { Activity } from "lucide-react";
import { getOcrAvailability, type WorkerServiceStatus } from "../api/system";

export function OcrWorkerAlert({ enabled }: { enabled: boolean }) {
  const [worker, setWorker] = useState<WorkerServiceStatus | null>(null);
  const [statusError, setStatusError] = useState(false);
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    if (!enabled) return;
    const requestId = ++requestRef.current;
    try {
      const status = await getOcrAvailability();
      if (requestId !== requestRef.current) return;
      setWorker(status.worker);
      setStatusError(false);
    } catch {
      if (requestId !== requestRef.current) return;
      setStatusError(true);
    }
  }, [enabled]);

  useEffect(() => {
    if (!enabled) return;
    void load();
    const timer = window.setInterval(() => void load(), 30_000);
    return () => {
      // 停用或卸载时使已发出的查询失效，避免迟到结果重新显示过期告警。
      requestRef.current += 1;
      window.clearInterval(timer);
    };
  }, [enabled, load]);

  if (!enabled || (!statusError && (!worker || worker.automation_available))) return null;

  const stale = worker?.availability === "stale";
  return (
    <Alert
      className="ocr-worker-alert"
      type={stale || statusError ? "warning" : "error"}
      showIcon
      icon={<Activity size={18} />}
      message={statusError ? "暂时无法获取自动识别状态" : stale ? "自动识别服务可能已中断" : "自动识别已暂停"}
      description={statusError ? (
        "暂时无法获取识别服务状态。填空题如未自动识别，请先按人工方式评阅，稍后点击『重新检查』。"
      ) : (
        <div className="ocr-worker-alert-detail">
          <span>{worker!.impact}</span>
          <strong>待处理 {worker!.queued_tasks} · 处理中 {worker!.in_flight_tasks} · 多次失败 {worker!.dead_letter_tasks}</strong>
          <span>处理建议：{worker!.action}</span>
        </div>
      )}
      action={<Button size="small" onClick={() => void load()}>重新检查</Button>}
    />
  );
}
