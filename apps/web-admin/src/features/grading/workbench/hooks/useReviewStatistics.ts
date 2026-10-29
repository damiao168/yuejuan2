import { useEffect, useState } from "react";
import { getReviewStatistics, type ReviewStatistics } from "../../../../api/reviewStatistics";
import { getUserErrorMessage } from "../../../../api/client";

export interface ReviewStatisticsScope {
  examId: string;
  tenantId: string;
  actorId: string;
  personalScope: boolean;
  refreshRevision?: number;
}
interface StatisticsState {
  scope: string;
  data?: ReviewStatistics;
  error?: string;
  loading: boolean;
}

/** 每次读取属于当前登录身份与考试；失焦停止轮询，切换范围时旧请求不能写入新统计。 */
export function useReviewStatistics({ examId, tenantId, actorId, personalScope, refreshRevision = 0 }: ReviewStatisticsScope) {
  const scope = JSON.stringify([tenantId, actorId, examId, personalScope]);
  const [focused, setFocused] = useState(() => document.visibilityState !== "hidden" && document.hasFocus());
  const [reload, setReload] = useState(0);
  const [state, setState] = useState<StatisticsState>({ scope, loading: false });
  useEffect(() => {
    const focus = () => setFocused(document.visibilityState !== "hidden" && document.hasFocus());
    window.addEventListener("focus", focus);
    window.addEventListener("blur", focus);
    document.addEventListener("visibilitychange", focus);
    return () => {
      window.removeEventListener("focus", focus);
      window.removeEventListener("blur", focus);
      document.removeEventListener("visibilitychange", focus);
    };
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 交分回执与手动刷新计数是主动重读触发键，不能删掉后只等待下次轮询。
  useEffect(() => {
    if (!examId || !tenantId || !actorId || !focused) return;
    let current = true;
    let inFlight = false;
    const controller = new AbortController();
    const read = async () => {
      if (inFlight) return;
      inFlight = true;
      setState(previous => ({ ...(previous.scope === scope ? previous : {}), scope, loading: true, error: undefined }));
      try {
        const data = await getReviewStatistics(examId, actorId, personalScope, controller.signal);
        if (current) setState({ scope, data, loading: false });
      } catch (error) {
        if (current) setState(previous => ({ ...(previous.scope === scope ? previous : {}), scope, loading: false, error: getUserErrorMessage(error, "考试统计读取失败，请重试") }));
      } finally { inFlight = false; }
    };
    void read();
    const timer = window.setInterval(() => void read(), 10_000);
    return () => { current = false; controller.abort(); window.clearInterval(timer); };
  }, [scope, examId, tenantId, actorId, personalScope, focused, refreshRevision, reload]);
  const visible = state.scope === scope ? state : { scope, loading: false };
  return { statistics: visible.data, error: visible.error, loading: focused && visible.loading, focused, refresh: () => setReload(value => value + 1) };
}
