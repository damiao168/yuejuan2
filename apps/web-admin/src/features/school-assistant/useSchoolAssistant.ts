import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getSchoolAssistantCapabilities, streamSchoolAssistantQuery,
  type SchoolAssistantCapabilities, type SchoolAssistantContext, type SchoolAssistantEvent,
  type SchoolAssistantIntent, type SchoolAssistantResult, type SchoolAssistantScope, type SchoolAssistantSource
} from "../../api/schoolAssistant";
import { getUserErrorMessage } from "../../api/client";

export interface AssistantContext extends SchoolAssistantContext { exam_name?: string }
interface QueryState {
  status: "idle" | "running" | "completed" | "cancelled" | "error";
  scope?: SchoolAssistantScope;
  result?: SchoolAssistantResult;
  sources: SchoolAssistantSource[];
  progress: string;
  error: string;
}
const emptyQuery = (): QueryState => ({ status: "idle", sources: [], progress: "", error: "" });

export function useSchoolAssistant(accountKey: string) {
  const [contextState, setContextState] = useState<AssistantContext>({});
  const [query, setQuery] = useState<QueryState>(emptyQuery);
  const [capabilities, setCapabilities] = useState<SchoolAssistantCapabilities>();
  const [capabilitiesError, setCapabilitiesError] = useState("");
  const [capabilitiesLoading, setCapabilitiesLoading] = useState(true);
  const [capabilityRevision, setCapabilityRevision] = useState(0);
  const active = useRef<{ generation: number; controller?: AbortController }>({ generation: 0 });
  const latestAccount = useRef(accountKey);
  latestAccount.current = accountKey;
  const lastQuery = useRef<{ intent: SchoolAssistantIntent; question: string } | undefined>(undefined);

  const invalidate = useCallback(() => {
    active.current.generation += 1;
    active.current.controller?.abort();
    active.current.controller = undefined;
  }, []);

  useEffect(() => {
    // 同一组件可能换账户；即使网络层没有及时终止，旧账户事件也不能进入新的页面状态。
    invalidate();
    setContextState({}); setQuery(emptyQuery()); lastQuery.current = undefined;
    return invalidate;
  }, [accountKey, invalidate]);

  useEffect(() => {
    const controller = new AbortController();
    let live = true;
    setCapabilities(undefined); setCapabilitiesError(""); setCapabilitiesLoading(true);
    getSchoolAssistantCapabilities(controller.signal).then((response) => {
      if (live && latestAccount.current === accountKey) setCapabilities(response);
    }).catch((error) => {
      if (live && !controller.signal.aborted) setCapabilitiesError(getUserErrorMessage(error, "任务权限读取失败，请重试"));
    }).finally(() => { if (live) setCapabilitiesLoading(false); });
    return () => { live = false; controller.abort(); };
  }, [accountKey, capabilityRevision]);

  const setContext = useCallback((next: AssistantContext) => {
    invalidate(); setContextState(next); setQuery(emptyQuery()); lastQuery.current = undefined;
  }, [invalidate]);

  const cancel = useCallback(() => {
    invalidate();
    setQuery((previous) => previous.status === "running" ? { ...previous, status: "cancelled", progress: "已停止查询" } : previous);
  }, [invalidate]);

  const run = useCallback(async (intent: SchoolAssistantIntent, question: string) => {
    if (!capabilities?.intents.some((item) => item.intent === intent && item.available)) return;
    invalidate();
    const generation = active.current.generation;
    const controller = new AbortController();
    active.current.controller = controller;
    const requestId = crypto.randomUUID();
    const owner = accountKey;
    const expectedExam = contextState.exam_id;
    let lastSequence = 0;
    let receivedResult = false;
    let terminal = false;
    lastQuery.current = { intent, question };
    setQuery({ ...emptyQuery(), status: "running", progress: "正在核实考试范围" });
    const isCurrent = () => active.current.generation === generation && latestAccount.current === owner && !controller.signal.aborted;
    const onEvent = (event: SchoolAssistantEvent) => {
      if (!isCurrent() || terminal || event.request_id !== requestId || !Number.isInteger(event.seq) || event.seq <= lastSequence) return;
      lastSequence = event.seq;
      switch (event.type) {
        case "scope_resolved":
          if (expectedExam && event.scope.exam_id !== expectedExam) throw new Error("查询范围发生变化，请重新选择考试");
          setQuery((previous) => ({ ...previous, scope: event.scope })); break;
        case "source_started": setQuery((previous) => ({ ...previous, progress: `正在读取${event.label}` })); break;
        case "source_finished": setQuery((previous) => ({ ...previous, sources: [...previous.sources.filter((source) => source.id !== event.source.id), event.source] })); break;
        case "result":
          if (event.result.request_id !== requestId || event.result.intent !== intent || (expectedExam && event.result.scope.exam_id !== expectedExam)) throw new Error("查询结果与当前任务不一致，请重新查询");
          receivedResult = true;
          setQuery((previous) => ({ ...previous, result: event.result, scope: event.result.scope, sources: event.result.sources })); break;
        case "done":
          terminal = true;
          setQuery((previous) => receivedResult ? { ...previous, status: "completed", progress: "查询完成" } : { ...previous, status: "error", error: "查询没有返回结果，请重新查询" }); break;
        case "error":
          terminal = true;
          setQuery((previous) => ({ ...previous, status: "error", error: event.message || "查询失败，请重试", progress: "" })); break;
      }
    };
    try {
      const { exam_name: _displayName, ...hint } = contextState;
      await streamSchoolAssistantQuery({ request_id: requestId, question, preset_intent: intent, context_hint: { ...hint, ...(intent === "publish_diagnosis" ? { release_channel: "student_portal" as const } : {}) } }, controller.signal, onEvent);
      if (isCurrent() && !terminal) setQuery((previous) => ({ ...previous, status: "error", error: "连接已结束，查询未完成。请重新查询。", progress: "" }));
    } catch (error) {
      if (isCurrent()) setQuery((previous) => ({ ...previous, status: "error", error: getUserErrorMessage(error, "查询失败，请重试"), progress: "" }));
    } finally {
      if (isCurrent()) active.current.controller = undefined;
    }
  }, [accountKey, capabilities, contextState, invalidate]);

  const refreshCapabilities = useCallback(() => setCapabilityRevision((value) => value + 1), []);
  // 重试仍读取最近一次请求；run 更新时也要更新回调，不能沿用旧账户、权限或考试范围。
  const retry = useCallback(() => {
    if (lastQuery.current) void run(lastQuery.current.intent, lastQuery.current.question);
  }, [run]);

  return useMemo(() => ({
    context: contextState, setContext, query, capabilities, capabilitiesError, capabilitiesLoading,
    refreshCapabilities, run, cancel, retry
  }), [contextState, setContext, query, capabilities, capabilitiesError, capabilitiesLoading, refreshCapabilities, run, cancel, retry]);
}
