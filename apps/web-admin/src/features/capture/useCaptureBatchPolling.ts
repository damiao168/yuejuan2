import { useEffect } from "react";

export function useCaptureBatchPolling(
  batchId: string | undefined,
  status: string | undefined,
  loadDetail: (batchId: string, quiet: boolean) => Promise<void>,
): void {
  useEffect(() => {
    if (!batchId || !status || !["uploading", "processing"].includes(status)) return;
    let active = true;
    let busy = false;
    let timer: number | undefined;
    const clear = () => { window.clearTimeout(timer); timer = undefined; };
    const schedule = () => {
      clear();
      if (active && !busy && !document.hidden) timer = window.setTimeout(poll, 3000);
    };
    const poll = async () => {
      if (!active || busy || document.hidden) return;
      busy = true;
      try { await loadDetail(batchId, true); }
      catch { /* 详情 hook 负责显示失败；下次轮询仍可恢复。 */ }
      finally {
        busy = false;
        // 上次响应结束后才开始计时，慢请求不会被下一轮作废或叠加；后台页暂停。
        schedule();
      }
    };
    document.addEventListener("visibilitychange", schedule);
    schedule();
    return () => {
      active = false;
      clear();
      document.removeEventListener("visibilitychange", schedule);
    };
  }, [batchId, status, loadDetail]);
}
