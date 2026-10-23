import { useCallback, useRef, useState, type RefObject } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { App as AntApp } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { deleteManagedModelAPIConfig, probeManagedModelAPIConfig, updateManagedModelAPIConfig, type ManagedAPIProbeMode, type ManagedAPIProbeResult, type ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { onboardingQueryKey } from "../../onboarding/queries";
import { capabilityDialogPresentation, formatDateTime, isTransientProbeError, modelUsageMessage, probeFormatLabel } from "../lib/modelConfig";
import { isTransientModelAPIError, modelProbeFailure } from "../lib/modelConfigNotices";
import { modelOperationKey, type ModelOperationQueue } from "../lib/modelOperationQueue";

type ProbeOutcome = { status: "success" | "temporary" | "failed"; requestFailed?: boolean };
type ProbeBatch = { group: string; completed: number; total: number; stopped: boolean };
export type ModelConfigPatch = { sequence: number; tenantID: string; config: ManagedModelAPIConfig | null; modelID: string; syncDefault: boolean };

interface SchoolModelActionsOptions {
  selectedTenantID: string;
  configs: ManagedModelAPIConfig[];
  configLoading: boolean;
  queue: ModelOperationQueue;
  mountedRef: RefObject<boolean>;
  selectedTenantRef: RefObject<string>;
  configPatchSequenceRef: RefObject<number>;
  configPatchesRef: RefObject<Map<string, ModelConfigPatch>>;
  knownConfigVersionsRef: RefObject<Map<string, string>>;
  probeDetailsRef: RefObject<Map<string, { config: ManagedModelAPIConfig; result: ManagedAPIProbeResult }>>;
  commitConfig: (tenantID: string, config: ManagedModelAPIConfig | null, modelID: string, syncDefault?: boolean) => void;
  loadConfigs: (tenantID: string) => Promise<void>;
}

// 检测、状态写入和删除共用页面的队列与修订引用，避免晚到的检测覆盖较新的配置。
// 批量停止只撤销排队请求；已发出的请求继续等待结果。
export function useSchoolModelActions({ selectedTenantID, configs, configLoading, queue, mountedRef, selectedTenantRef,
  configPatchSequenceRef, configPatchesRef, knownConfigVersionsRef, probeDetailsRef, commitConfig, loadConfigs }: SchoolModelActionsOptions) {
  const { message, modal } = AntApp.useApp();
  const queryClient = useQueryClient();
  const [batches, setBatches] = useState<Record<string, ProbeBatch>>({});
  const batchRef = useRef(new Map<string, ProbeBatch>());
  const batchSequenceRef = useRef(0);
  const batchProbeProgress = batches[selectedTenantID];

  const showConnectionDetails = useCallback((config: ManagedModelAPIConfig) => {
    const kind = config.last_test_status === "failed" ? "error" : config.last_test_status === "temporary_unavailable" ? "warning" : "info";
    modal[kind]({
      title: `${config.model_name}：连接检测详情`,
      width: 560,
      content: <div className="platform-model-diagnostic">
        <p>{config.last_test_message || "暂无连接检测说明"}</p>
        <dl>
          <div><dt>最近检测</dt><dd>{formatDateTime(config.last_tested_at)}</dd></div>
          <div><dt>上次连接成功</dt><dd>{formatDateTime(config.last_successful_tested_at)}</dd></div>
          <div><dt>检测耗时</dt><dd>{config.last_test_latency_ms !== undefined ? `${config.last_test_latency_ms}ms` : "—"}</dd></div>
          <div><dt>API 地址</dt><dd>{config.base_url}</dd></div>
        </dl>
      </div>,
    });
  }, [modal]);

  const showCapabilityDetails = useCallback((config: ManagedModelAPIConfig, result?: ManagedAPIProbeResult) => {
    const recent = probeDetailsRef.current.get(modelOperationKey(config.tenant_id, config.id));
    if (!result && recent && recent.config.updated_at === config.updated_at && recent.config.last_capability_tested_at === config.last_capability_tested_at) result = recent.result;
    const diagnostic = result?.diagnostic ?? config.last_capability_diagnostic ?? {};
    const usage = result?.usage ?? config.last_capability_usage;
    const temporarilyUnavailable = result ? isTransientProbeError(result.error_code, result.status_code) : config.last_capability_status === "temporary_unavailable";
    const failed = result ? !result.ok && !temporarilyUnavailable : config.last_capability_status === "failed";
    const presentation = capabilityDialogPresentation(config.display_name, temporarilyUnavailable, failed);
    modal[presentation.kind]({
      title: presentation.title,
      width: 560,
      content: (
        <div className="platform-model-diagnostic">
          <p>{result?.message ?? config.last_capability_message ?? "暂无结构化检测结果"}</p>
          <dl>
            <div><dt>检测时间</dt><dd>{formatDateTime(config.last_capability_tested_at)}</dd></div>
            <div><dt>错误分类</dt><dd>{result?.error_code ?? (failed ? "检测失败" : "—")}</dd></div>
            <div><dt>结束原因</dt><dd>{diagnostic.finish_reason || "—"}</dd></div>
            <div><dt>输出约束</dt><dd>{probeFormatLabel(diagnostic.response_format)}</dd></div>
            <div><dt>返回长度</dt><dd>{diagnostic.content_length ?? 0} 字符</dd></div>
            <div><dt>Token 用量</dt><dd>{usage?.total_tokens ?? 0}（输出 {usage?.output_tokens ?? 0}）</dd></div>
          </dl>
          {diagnostic.content_preview ? (
            <><small>本次返回摘要（最多 256 个字符，不会写入数据库）</small><pre>{diagnostic.content_preview}</pre></>
          ) : <small>供应商没有返回可展示的内容摘要。</small>}
        </div>
      )
    });
  }, [modal, probeDetailsRef]);

  const showTextDetails = useCallback((config: ManagedModelAPIConfig) => {
    // 普通回复详情只读取独立的文本检测记录，不能借用连接或结构化检测结果。
    const diagnostic = config.last_text_diagnostic;
    const usage = config.last_text_usage;
    const kind = config.last_text_status === "failed" ? "error" : config.last_text_status === "temporary_unavailable" ? "warning" : "info";
    modal[kind]({
      title: `${config.model_name}：普通回复检测详情`,
      width: 560,
      content: <div className="platform-model-diagnostic">
        <p>{config.last_text_message || "暂无普通回复检测说明"}</p>
        <dl>
          <div><dt>检测时间</dt><dd>{formatDateTime(config.last_text_tested_at)}</dd></div>
          <div><dt>结束原因</dt><dd>{diagnostic?.finish_reason || "—"}</dd></div>
          <div><dt>返回长度</dt><dd>{diagnostic?.content_length !== undefined ? `${diagnostic.content_length} 字符` : "—"}</dd></div>
          <div><dt>Token 用量</dt><dd>{usage ? `${usage.total_tokens}（输出 ${usage.output_tokens}）` : "—"}</dd></div>
        </dl>
        {diagnostic?.content_preview ? <pre>{diagnostic.content_preview}</pre> : null}
      </div>,
    });
  }, [modal]);

  const runProbe = useCallback((config: ManagedModelAPIConfig, tenantID: string, mode: ManagedAPIProbeMode, group?: string) => {
    return queue.enqueue<ProbeOutcome>(tenantID, config.id, mode, async () => {
      const startSequence = configPatchSequenceRef.current;
      const visible = () => mountedRef.current && tenantID === selectedTenantRef.current;
      try {
        const response = group
          ? await probeManagedModelAPIConfig(config.id, tenantID, mode)
          : await probeManagedModelAPIConfig(config.id, tenantID, mode, mode === "structured" || mode === "capability");
        if (!mountedRef.current) return { status: "failed" };
        const laterPatch = configPatchesRef.current.get(modelOperationKey(tenantID, config.id));
        // 切换默认项等写入可能同时更新这一行；先前检测不能带回旧的默认选择或连接信息。
        const knownVersion = knownConfigVersionsRef.current.get(modelOperationKey(tenantID, config.id));
        const superseded = Boolean(laterPatch && laterPatch.sequence > startSequence) || knownVersion === "deleted" ||
          Boolean(knownVersion && response.config.updated_at && knownVersion !== response.config.updated_at);
        if (!superseded) {
          commitConfig(tenantID, response.config, config.id);
          if (mode === "structured" || mode === "capability") {
            probeDetailsRef.current.set(modelOperationKey(tenantID, config.id), response);
          }
        } else if (visible()) void loadConfigs(tenantID);
        if (visible() && !group && !superseded) {
          if (response.result.ok) {
            const tokenSummary = response.result.generated_request ? ` · ${response.result.usage?.total_tokens ?? 0} Tokens` : " · 0 生成 Token";
            message.success(`${config.display_name}：${response.result.message}${tokenSummary}`);
          } else {
            const notice = modelProbeFailure(config, response.result);
            message[notice.kind](notice.text);
            // 检测结果不自动打开弹窗，避免打断另一模型正在进行的编辑。
          }
        }
        if (!group) void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
        return { status: response.result.ok ? "success" : isTransientProbeError(response.result.error_code, response.result.status_code) ? "temporary" : "failed" };
      } catch (error) {
        const temporarilyUnavailable = isTransientModelAPIError(error);
        if (visible() && !group) {
          void loadConfigs(tenantID);
          const notify = temporarilyUnavailable ? message.warning : message.error;
          notify(`${getUserErrorMessage(error, `${config.display_name}连接失败`)}${temporarilyUnavailable ? "（本次暂时无法验证，不代表模型已失效）" : ""}`);
        }
        return { status: temporarilyUnavailable ? "temporary" : "failed", requestFailed: true };
      }
    }, group);
  }, [queue, commitConfig, loadConfigs, message, queryClient, mountedRef, selectedTenantRef, configPatchSequenceRef, configPatchesRef, knownConfigVersionsRef, probeDetailsRef]);

  const probe = useCallback(async (config: ManagedModelAPIConfig, mode: ManagedAPIProbeMode = "quick") => {
    await runProbe(config, selectedTenantID, mode);
  }, [runProbe, selectedTenantID]);

  const probeAll = useCallback(async () => {
    const tenantID = selectedTenantID;
    if (!tenantID || configLoading || !configs.length || batchRef.current.has(tenantID)) return;
    const batch: ProbeBatch = { group: `batch-${++batchSequenceRef.current}`, completed: 0, total: configs.length, stopped: false };
    batchRef.current.set(tenantID, batch);
    setBatches((current) => ({ ...current, [tenantID]: { ...batch } }));
    let healthy = 0, temporary = 0, failed = 0, skipped = 0;
    let requestFailed = false;
    try {
      await Promise.all(configs.map(async (config) => {
        const outcome = await runProbe(config, tenantID, "quick", batch.group);
        if (!outcome) skipped += 1;
        else {
          if (outcome.status === "success") healthy += 1;
          else if (outcome.status === "temporary") temporary += 1;
          else failed += 1;
          requestFailed ||= Boolean(outcome.requestFailed);
        }
        batch.completed += 1;
        if (mountedRef.current) setBatches((current) => ({ ...current, [tenantID]: { ...batch } }));
      }));
      if (!mountedRef.current || tenantID !== selectedTenantRef.current) return;
      const summary = `${batch.stopped ? "连接检测已停止" : "全部连接检测完成"}：${healthy} 个正常，${temporary} 个暂时无法验证，${failed} 个失败${skipped ? `，${skipped} 个未检测` : ""}`;
      if (temporary || failed || batch.stopped) message.warning(summary);
      else message.success(summary);
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
      if (requestFailed) await loadConfigs(tenantID);
    } finally {
      batchRef.current.delete(tenantID);
      if (mountedRef.current) setBatches((current) => { const next = { ...current }; delete next[tenantID]; return next; });
    }
  }, [configLoading, configs, loadConfigs, message, queryClient, runProbe, selectedTenantID, mountedRef, selectedTenantRef]);

  const stopBatch = () => {
    const batch = batchRef.current.get(selectedTenantID);
    if (!batch) return;
    batch.stopped = true;
    queue.cancelQueued(batch.group);
    setBatches((current) => ({ ...current, [selectedTenantID]: { ...batch } }));
  };

  const updateUsage = useCallback(async (config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) => {
    const tenantID = selectedTenantID;
    const release = queue.acquireWrite(tenantID, config.id);
    if (!release) return;
    // 默认选择属于整所学校；同校的两次切换必须串行，避免响应乱序显示成不同的默认项。
    const changesDefault = isDefault || config.is_default;
    const releaseDefault = changesDefault ? queue.acquireWrite(tenantID, "__default__") : undefined;
    if (changesDefault && !releaseDefault) { release(); return; }
    try {
      const response = await updateManagedModelAPIConfig(config.id, tenantID, {
        display_name: config.display_name,
        adapter_type: config.adapter_type,
        base_url: config.base_url,
        api_key: "",
        model_name: config.model_name,
        model_version: config.model_version,
        region: config.region,
        status,
        is_default: isDefault
      });
      commitConfig(tenantID, response.config, config.id, true);
      if (mountedRef.current && tenantID === selectedTenantRef.current) message.success(modelUsageMessage(config, status, isDefault));
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      if (mountedRef.current && tenantID === selectedTenantRef.current) message.error(getUserErrorMessage(error));
    } finally {
      releaseDefault?.();
      release();
    }
  }, [commitConfig, message, queryClient, queue, selectedTenantID, mountedRef, selectedTenantRef]);

  const removeConfig = useCallback((config: ManagedModelAPIConfig) => {
    if (queue.isBusy(selectedTenantID, config.id) || config.is_default) return;
    const tenantID = selectedTenantID;
    modal.confirm({
      title: `删除 ${config.display_name}？`,
      content: "删除后该配置无法在此恢复，已保存的 API Key 将不再可用。被阅卷角色、试卷解析或治理配置引用的模型无法删除。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        const release = queue.acquireWrite(tenantID, config.id);
        if (!release) return;
        try {
          await deleteManagedModelAPIConfig(config.id, tenantID);
          commitConfig(tenantID, null, config.id);
          probeDetailsRef.current.delete(modelOperationKey(tenantID, config.id));
          if (mountedRef.current && tenantID === selectedTenantRef.current) message.success(`${config.display_name} 已删除`);
          void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
        } catch (error) {
          if (mountedRef.current && tenantID === selectedTenantRef.current) message.error(getUserErrorMessage(error));
        } finally {
          release();
        }
      }
    });
  }, [commitConfig, message, modal, queryClient, queue, selectedTenantID, mountedRef, selectedTenantRef, probeDetailsRef]);

  return { showConnectionDetails, showCapabilityDetails, showTextDetails, probe, probeAll, stopBatch, updateUsage, removeConfig, batchProbeProgress };
}
