import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  App as AntApp,
  AutoComplete,
  Button,
  Drawer,
  Form,
  Input,
  Segmented,
  Select,
  Space,
  Tooltip
} from "antd";
import { KeyRound, Plus, RefreshCw, Zap } from "lucide-react";
import {
  autoCreateManagedModelAPIConfig,
  deleteManagedModelAPIConfig,
  listAvailableManagedModels,
  listManagedModelAPIConfigs,
  managedModelDiscoveryErrorMessage,
  probeManagedModelAPIConfig,
  updateManagedModelAPIConfig,
  type ManagedAPIProbeMode,
  type ManagedAPIProbeResult,
  type ManagedModelAPIConfig
} from "../api/modelApiConfig";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import type { Tenant } from "../api/org";
import { onboardingQueryKey } from "../features/onboarding/queries";
import { SchoolModelSelector } from "../features/platform-model-config/components/SchoolModelSelector";
import { SchoolModelTable } from "../features/platform-model-config/components/SchoolModelTable";
import { modelConfigColumns } from "../features/platform-model-config/components/modelConfigColumns";
import { useModelConfigDrafts } from "../features/platform-model-config/hooks/useModelConfigDrafts";
import {
  supplierOptions,
  supplierDefaults,
  presetForConfig,
  modelConfigDraftKey,
  isTransientProbeError,
  applySavedModelConfig,
  modelUsageMessage,
  modelSaveLabel,
  hasModelFormChanges,
  capabilityDialogPresentation,
  capabilityVerified,
  probeFormatLabel,
  loadAllSchoolTenants,
  type SupplierPreset,
  type ConfigFormValues
} from "../features/platform-model-config/lib/modelConfig";
import { PanelModelBindingsSection } from "./PanelModelBindingsSection";
import { ManagedGovernanceWorkspace } from "../components/model-governance/ManagedGovernanceWorkspace";
import { hashQueryParam } from "../router/query";
import { rememberModelConfigLocation, selectModelSchool } from "../features/platform-model-config/lib/modelConfigLocation";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";

type ModelFailureNotice = { kind: "warning" | "error"; text: string };

function modelDiscoveryFailure(error: unknown, hasSavedCredential: boolean): ModelFailureNotice {
  const temporarilyUnavailable = error instanceof ApiClientError && isTransientProbeError(error.code);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${managedModelDiscoveryErrorMessage(error, hasSavedCredential)}${temporarilyUnavailable ? "（本次暂时无法验证，不代表 API Key 无效）" : ""}`
  };
}

function modelSaveFailure(error: unknown, editing: ManagedModelAPIConfig | null): ModelFailureNotice {
  const temporarilyUnavailable = error instanceof ApiClientError && isTransientProbeError(error.code);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${getUserErrorMessage(error, editing ? "配置更新失败" : "配置保存失败")}${temporarilyUnavailable ? "（本次暂时无法验证，已保留当前填写内容）" : ""}`
  };
}

function modelProbeFailure(config: ManagedModelAPIConfig, result: ManagedAPIProbeResult): ModelFailureNotice {
  // 供应商暂不可用只表示本次未能验证，不能把模型或密钥显示为已失效。
  const temporarilyUnavailable = isTransientProbeError(result.error_code);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${config.display_name}：${result.message}${temporarilyUnavailable ? "（本次暂时无法验证，不代表模型已失效）" : ""}`
  };
}

function savedModelAccessDescription(config?: ManagedModelAPIConfig) {
  // 访问检查通过不等于评分能力已验证；只认当前协议的能力检测结果。
  if (config && capabilityVerified(config)) return "该配置已通过当前结构化能力检测。正式评分仍需要角色绑定、真实评测、人工批准和使用策略。";
  return "此次保存未生成回复，尚未验证回复与结构化能力。检测会发送合成测试内容并产生少量用量，供应商可能收费。正式评分还需要角色绑定、真实评测、人工批准和使用策略。";
}

export function PlatformModelConfigPage() {
  const queryClient = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const [form] = Form.useForm<ConfigFormValues>();
  const [schools, setSchools] = useState<Tenant[]>([]);
  const [schoolLoading, setSchoolLoading] = useState(true);
  const [selectedTenantID, setSelectedTenantID] = useState("");
  const [activeTab, setActiveTab] = useState<"models" | "bindings" | "governance">(() => {
    const requested = hashQueryParam("tab");
    return requested === "bindings" || requested === "governance" ? requested : "models";
  });
  const [bindingsDirty, setBindingsDirty] = useState(false);
  const [governanceDirty, setGovernanceDirty] = useState(false);
  const [governanceSaving, setGovernanceSaving] = useState(false);
  const governanceSavingRef = useRef(false);
  const handleGovernanceSaving = useCallback((busy: boolean) => { governanceSavingRef.current = busy; setGovernanceSaving(busy); }, []);
  const [modelFilter, setModelFilter] = useState<"all" | "enabled" | "connection" | "capability">("all");
  const [savedAccess, setSavedAccess] = useState<{ tenantID: string; config: ManagedModelAPIConfig }>();
  const [bindingsSaving, setBindingsSaving] = useState(false);
  const bindingsSavingRef = useRef(false);
  const handleBindingsSaving = useCallback((busy: boolean) => {
    bindingsSavingRef.current = busy;
    setBindingsSaving(busy);
  }, []);
  const [configs, setConfigs] = useState<ManagedModelAPIConfig[]>([]);
  const [configLoading, setConfigLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<ManagedModelAPIConfig | null>(null);
  const [credentialSource, setCredentialSource] = useState<ManagedModelAPIConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [probingID, setProbingID] = useState("");
  const [batchProbeProgress, setBatchProbeProgress] = useState<{ completed: number; total: number } | null>(null);
  const [updatingID, setUpdatingID] = useState("");
  // ref 在 React 重绘前阻止重复提交；state 同时锁住学校选择和配置操作。
  const modelOperationRef = useRef(false);
  const modelBusy = Boolean(probingID || batchProbeProgress || updatingID) || saving;
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelOptionsOpen, setModelOptionsOpen] = useState(false);
  const [filterAvailableModels, setFilterAvailableModels] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const configRequestRef = useRef(0);
  const { clearFormDraft, saveFormDraft, restoreFormDraft } = useModelConfigDrafts(form);
  const watchedSupplier = Form.useWatch("supplier", form);
  useUnsavedChangesGuard({ dirty: (activeTab === "bindings" && bindingsDirty) || governanceDirty, busy: bindingsSaving || governanceSaving || saving, description: "模型角色或学校使用策略尚未保存，离开后将重新读取服务端版本。" });
  // 三智能体角色配置只在点击各自保存按钮后写入服务端；离开当前学校或标签前先处理未保存输入。
  const changeBindingContext = (action: () => void) => {
    // 子组件开始保存即同步通知；排队的学校切换和先前打开的确认不能在保存中卸载它。
    if (bindingsSavingRef.current || governanceSavingRef.current) return;
    const proceed = () => { if (!bindingsSavingRef.current && !governanceSavingRef.current) action(); };
    if ((activeTab !== "bindings" || !bindingsDirty) && !governanceDirty) { proceed(); return; }
    modal.confirm({
      title: "放弃未保存的模型设置？",
      content: "切换学校或标签后，当前输入不会保留。",
      okText: "放弃并继续", cancelText: "继续编辑", okButtonProps: { danger: true }, onOk: proceed
    });
  };
  // 切换学校立即递增请求代次，旧学校配置不得回填到当前模型列表。
  const selectedSchool = useMemo(
    () => schools.find((school) => school.id === selectedTenantID),
    [schools, selectedTenantID]
  );
  const currentConfig = useMemo(
    () => configs.find((config) => config.is_default && config.status === "active"),
    [configs]
  );

  const loadSchools = useCallback(async () => {
    setSchoolLoading(true);
    try {
      const items = await loadAllSchoolTenants();
      setSchools(items);
      const requestedTenantID = new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("tenant_id");
      setSelectedTenantID((current) => !current && !requestedTenantID && hashQueryParam("choose_school") === "1" ? "" : selectModelSchool(items, current, requestedTenantID ?? ""));
      if (requestedTenantID && !items.some((school) => school.id === requestedTenantID)) {
        message.warning("地址中的学校已不可用，请重新选择配置学校。");
      }
    } catch {
      message.error("学校列表加载失败");
    } finally {
      setSchoolLoading(false);
    }
  }, [message]);

  const loadConfigs = useCallback(async (tenantID: string) => {
    const requestID = ++configRequestRef.current;
    if (!tenantID) {
      setConfigs([]);
      setConfigLoading(false);
      return;
    }
    setConfigLoading(true);
    try {
      const response = await listManagedModelAPIConfigs(tenantID);
      if (requestID !== configRequestRef.current) return;
      setConfigs(response.configs);
    } catch {
      if (requestID !== configRequestRef.current) return;
      setConfigs([]);
      message.error("模型 API 配置加载失败");
    } finally {
      if (requestID === configRequestRef.current) setConfigLoading(false);
    }
  }, [message]);

  useEffect(() => { void loadSchools(); }, [loadSchools]);
  useEffect(() => {
    setConfigs([]);
    void loadConfigs(selectedTenantID);
    return () => { configRequestRef.current += 1; };
  }, [loadConfigs, selectedTenantID]);

  const openCreate = () => {
    setEditing(null);
    setCredentialSource(null);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID), {
      supplier: undefined,
      provider_key: undefined,
      display_name: undefined,
      adapter_type: undefined,
      base_url: "",
      api_key: "",
      model_name: "",
      model_version: "",
      region: "global",
      enabled: true,
      is_default: configs.length === 0
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的模型配置");
  };

  const openCreateWithKey = useCallback((config: ManagedModelAPIConfig) => {
    setEditing(null);
    setCredentialSource(config);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID, undefined, config.id), {
      supplier: presetForConfig(config),
      provider_key: config.provider_key,
      display_name: config.display_name,
      adapter_type: config.adapter_type,
      base_url: config.base_url,
      api_key: "",
      model_name: "",
      model_version: "",
      region: config.region,
      enabled: true,
      is_default: false
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的模型配置");
  }, [message, restoreFormDraft, selectedTenantID]);

  const openEdit = useCallback((config: ManagedModelAPIConfig) => {
    setEditing(config);
    setCredentialSource(null);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID, config.id), {
      supplier: presetForConfig(config),
      provider_key: config.provider_key,
      display_name: config.display_name,
      adapter_type: config.adapter_type,
      base_url: config.base_url,
      api_key: "",
      model_name: config.model_name,
      model_version: config.model_version,
      region: config.region,
      enabled: config.status === "active",
      is_default: config.is_default
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的编辑内容");
  }, [message, restoreFormDraft, selectedTenantID]);

  const changeSupplier = (supplier: SupplierPreset) => {
    if (editing || credentialSource) return;
    form.setFieldsValue({ ...supplierDefaults[supplier], supplier, base_url: "", model_name: "" });
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
  };

  const fetchAvailableModels = async () => {
    if (!selectedTenantID || modelsLoading) return;
    let values: ConfigFormValues;
    try {
      const supplier = form.getFieldValue("supplier") as SupplierPreset | undefined;
      const apiKey = String(form.getFieldValue("api_key") ?? "").trim();
      const canReuseSavedCredential = Boolean(credentialSource) || Boolean(editing && !apiKey && (
        supplier !== "custom" || String(form.getFieldValue("base_url") ?? "").trim() === editing.base_url
      ));
      await form.validateFields([
        "supplier",
        ...(supplier === "custom" ? ["base_url"] : []),
        ...(!canReuseSavedCredential ? ["api_key"] : [])
      ]);
      values = form.getFieldsValue();
    } catch {
      return;
    }
    setModelsLoading(true);
    try {
      const apiKey = String(values.api_key ?? "").trim();
      const reusableCredential = credentialSource ?? (editing && !apiKey ? editing : null);
      const response = await listAvailableManagedModels({
        tenant_id: selectedTenantID,
        ...(reusableCredential ? { credential_source_id: reusableCredential.id } : { api_key: apiKey }),
        provider: values.supplier,
        base_url: values.supplier === "custom" ? values.base_url?.trim() : undefined
      });
      setAvailableModels(response.models);
      setFilterAvailableModels(false);
      setModelOptionsOpen(response.models.length > 0);
      if (response.models.length > 0) {
        message.success(`已从${response.provider.display_name}获取 ${response.models.length} 个模型，请选择`);
      } else {
        message.warning(`${response.provider.display_name}没有返回可用模型`);
      }
    } catch (error) {
      setModelOptionsOpen(false);
      setFilterAvailableModels(false);
      const notice = modelDiscoveryFailure(error, credentialSource !== null || Boolean(editing));
      const notify = message[notice.kind];
      notify(notice.text);
    } finally {
      setModelsLoading(false);
    }
  };

  const save = async () => {
    if (!selectedTenantID || saving) return;
    let values: ConfigFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    const draftKey = modelConfigDraftKey(selectedTenantID, editing?.id, credentialSource?.id);
    const apiKey = String(values.api_key ?? "").trim();
    setSaving(true);
    try {
      if (editing) {
        const response = await updateManagedModelAPIConfig(editing.id, selectedTenantID, {
          display_name: editing.display_name,
          adapter_type: editing.adapter_type,
          base_url: values.supplier === "custom" ? String(values.base_url ?? "").trim() : editing.base_url,
          api_key: apiKey,
          model_name: values.model_name.trim(),
          model_version: values.model_name.trim(),
          region: editing.region,
          status: editing.status,
          is_default: editing.is_default
        });
        setConfigs((current) => applySavedModelConfig(current, response.config));
        message.success(apiKey ? "配置和密钥已更新" : "配置已更新");
      } else {
        const response = await autoCreateManagedModelAPIConfig({
          tenant_id: selectedTenantID,
          ...(credentialSource ? { credential_source_id: credentialSource.id } : { api_key: apiKey }),
          model_name: values.model_name.trim(),
          provider: values.supplier,
          base_url: values.supplier === "custom" ? values.base_url?.trim() : undefined
        });
        setConfigs((current) => [response.config, ...current.map((item) => response.config.is_default ? { ...item, is_default: false } : item)]);
        setSavedAccess({ tenantID: selectedTenantID, config: response.config });
        message.success(`${response.config.display_name} 已保存；密钥及模型访问权限检查通过，尚未验证回复能力。`);
      }
      clearFormDraft(draftKey);
      setDrawerOpen(false);
      setModelOptionsOpen(false);
      form.resetFields();
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      const notice = modelSaveFailure(error, editing);
      const notify = message[notice.kind];
      notify(notice.text);
    } finally {
      setSaving(false);
    }
  };

  const closeDrawer = () => {
    if (saving) return;
    const values = form.getFieldsValue(true) as ConfigFormValues;
    const hasChanges = hasModelFormChanges(values, editing, credentialSource);
    const draftKey = modelConfigDraftKey(selectedTenantID, editing?.id, credentialSource?.id);
    if (hasChanges) {
      saveFormDraft(draftKey, values);
      message.info("未保存内容已暂存 2 分钟");
    } else {
      clearFormDraft(draftKey);
    }
    setDrawerOpen(false);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    form.resetFields();
  };

  const showCapabilityDetails = useCallback((config: ManagedModelAPIConfig, result?: ManagedAPIProbeResult) => {
    const diagnostic = result?.diagnostic ?? config.last_capability_diagnostic ?? {};
    const usage = result?.usage ?? config.last_capability_usage;
    const temporarilyUnavailable = Boolean(result && isTransientProbeError(result.error_code));
    const failed = result ? !result.ok && !temporarilyUnavailable : config.last_capability_status === "failed";
    const presentation = capabilityDialogPresentation(config.display_name, temporarilyUnavailable, failed);
    modal[presentation.kind]({
      title: presentation.title,
      width: 560,
      content: (
        <div className="platform-model-diagnostic">
          <p>{result?.message ?? config.last_capability_message ?? "暂无能力检测结果"}</p>
          <dl>
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
  }, [modal]);

  const probe = useCallback(async (config: ManagedModelAPIConfig, mode: ManagedAPIProbeMode = "quick") => {
    if (modelOperationRef.current) return;
    modelOperationRef.current = true;
    setProbingID(config.id);
    try {
      const response = await probeManagedModelAPIConfig(config.id, selectedTenantID, mode, mode === "capability");
      setConfigs((current) => current.map((item) => item.id === response.config.id ? response.config : item));
      if (response.result.ok) {
        const tokenSummary = response.result.generated_request
          ? ` · ${response.result.usage?.total_tokens ?? 0} Tokens`
          : " · 0 生成 Token";
        message.success(`${config.display_name}：${response.result.message}${tokenSummary}`);
        void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
      }
      else {
        const notice = modelProbeFailure(config, response.result);
        const notify = message[notice.kind];
        notify(notice.text);
        if (mode === "capability") showCapabilityDetails(response.config, response.result);
      }
    } catch (error) {
      await loadConfigs(selectedTenantID);
      const notify = error instanceof ApiClientError && isTransientProbeError(error.code) ? message.warning : message.error;
      notify(getUserErrorMessage(error, `${config.display_name}连接失败`));
    } finally {
      modelOperationRef.current = false;
      setProbingID("");
    }
  }, [loadConfigs, message, queryClient, selectedTenantID, showCapabilityDetails]);

  const probeAll = useCallback(async () => {
    if (!selectedTenantID || configLoading || configs.length === 0 || modelOperationRef.current) return;
    modelOperationRef.current = true;
    const tenantID = selectedTenantID;
    const requestID = configRequestRef.current;
    const targets = [...configs];
    let nextIndex = 0;
    let completed = 0;
    let healthy = 0;
    let temporary = 0;
    let failed = 0;
    let requestFailed = false;
    setBatchProbeProgress({ completed, total: targets.length });
    // 暂不可用单独计数，批量汇总不能把本次无法验证当成模型失效。
    const countProbeResult = (result: ManagedAPIProbeResult) => {
      if (result.ok) healthy += 1;
      else if (isTransientProbeError(result.error_code)) temporary += 1;
      else failed += 1;
    };
    // 每份配置的密钥和模型权限都可能不同，已停用配置也检测；最多并发三个，单项失败不终止整批。
    const worker = async () => {
      while (nextIndex < targets.length) {
        const config = targets[nextIndex++];
        try {
          const response = await probeManagedModelAPIConfig(config.id, tenantID, "quick");
          if (requestID !== configRequestRef.current) return;
          setConfigs((current) => current.map((item) => item.id === response.config.id ? response.config : item));
          countProbeResult(response.result);
        } catch (error) {
          requestFailed = true;
          if (error instanceof ApiClientError && isTransientProbeError(error.code)) temporary += 1;
          else failed += 1;
        }
        if (requestID !== configRequestRef.current) return;
        completed += 1;
        setBatchProbeProgress({ completed, total: targets.length });
      }
    };
    try {
      await Promise.all(Array.from({ length: Math.min(3, targets.length) }, worker));
      if (requestID !== configRequestRef.current) return;
      const summary = `全部连接检测完成：${healthy} 个正常，${temporary} 个暂时无法验证，${failed} 个失败`;
      if (temporary || failed) message.warning(summary);
      else message.success(summary);
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
      // HTTP 错误也可能已在服务端记录检测状态，整批结束后重新读取一次。
      if (requestFailed) await loadConfigs(tenantID);
    } finally {
      modelOperationRef.current = false;
      setBatchProbeProgress(null);
    }
  }, [configLoading, configs, loadConfigs, message, queryClient, selectedTenantID]);

  const updateUsage = useCallback(async (config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) => {
    if (modelOperationRef.current) return;
    modelOperationRef.current = true;
    setUpdatingID(config.id);
    try {
      const response = await updateManagedModelAPIConfig(config.id, selectedTenantID, {
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
      setConfigs((current) => applySavedModelConfig(current, response.config));
      message.success(modelUsageMessage(config, status, isDefault));
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      message.error(getUserErrorMessage(error));
    } finally {
      modelOperationRef.current = false;
      setUpdatingID("");
    }
  }, [message, queryClient, selectedTenantID]);

  const removeConfig = useCallback((config: ManagedModelAPIConfig) => {
    if (modelOperationRef.current || config.is_default) return;
    const tenantID = selectedTenantID;
    modal.confirm({
      title: `删除 ${config.display_name}？`,
      content: "删除后该配置无法在此恢复，已保存的 API Key 将不再可用。被评分角色或治理配置引用的模型无法删除。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        if (modelOperationRef.current) return;
        modelOperationRef.current = true;
        setUpdatingID(config.id);
        const requestID = configRequestRef.current;
        try {
          await deleteManagedModelAPIConfig(config.id, tenantID);
          if (requestID !== configRequestRef.current) return;
          setConfigs((current) => current.filter((item) => item.id !== config.id));
          message.success(`${config.display_name} 已删除`);
          void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
        } catch (error) {
          message.error(getUserErrorMessage(error));
        } finally {
          modelOperationRef.current = false;
          setUpdatingID("");
        }
      }
    });
  }, [message, modal, queryClient, selectedTenantID]);

  const columns = useMemo(() => modelConfigColumns({
    configLoading, modelBusy, openCreateWithKey, openEdit, probe, probingID,
    removeConfig, showCapabilityDetails, updateUsage, updatingID
  }), [configLoading, modelBusy, openCreateWithKey, openEdit, probe, probingID, removeConfig, showCapabilityDetails, updateUsage, updatingID]);

  const activeCount = configs.filter((config) => config.status === "active").length;
  const healthyCount = configs.filter((config) => config.status === "active" && config.last_test_status === "success").length;
  const capabilityCount = configs.filter((config) => config.status === "active" && config.last_capability_status === "success" && config.last_capability_probe_version === "structured-json-v3").length;
  const visibleConfigs = configs.filter((config) => modelFilter === "all" || (config.status === "active" && (modelFilter === "enabled" || (modelFilter === "connection" ? config.last_test_status === "success" : config.last_capability_status === "success" && config.last_capability_probe_version === "structured-json-v3"))));
  const temporarilyUnavailableCount = configs.filter((config) => config.last_test_status === "temporary_unavailable").length;

  const renderCredentialField = () => {
    if (credentialSource) {
      return <Alert type="info" showIcon message={`沿用已保存的密钥（${credentialSource.credential_hint || "已加密"}）`} description="新模型会单独验证并加密保存；后续更换密钥需逐个更新模型配置。" />;
    }
    return <Form.Item
      name="api_key"
      label={editing ? `API Key（当前 ${editing.credential_hint ?? "已配置"}）` : "API Key"}
      dependencies={editing ? ["base_url"] : undefined}
      rules={editing ? [
        { min: 16, message: "密钥至少 16 个字符" },
        ({ getFieldValue }) => ({
          validator: async (_rule, value) => {
            const endpointChanged = presetForConfig(editing) === "custom" &&
              String(getFieldValue("base_url") ?? "").trim() !== editing.base_url;
            if (endpointChanged && !String(value ?? "").trim()) {
              throw new Error("更换 API 地址时请输入新 API Key");
            }
          }
        })
      ] : [{ required: true, message: "请输入 API Key" }, { min: 16, message: "密钥至少 16 个字符" }]}
      extra={editing ? "原密钥不会回显；留空则继续使用。更换 API 地址时必须输入新密钥。" : "密钥将加密保存；保存只做零生成 Token 连接检测。"}
    >
      <Input.Password
        prefix={<KeyRound size={15} />}
        maxLength={1024}
        autoComplete="new-password"
        placeholder={editing ? `当前 ${editing.credential_hint ?? "已配置"}` : "输入供应商密钥"}
        onChange={() => {
          setAvailableModels([]);
          setModelOptionsOpen(false);
          setFilterAvailableModels(false);
        }}
      />
    </Form.Item>;
  };

  const renderModelForm = () => (
    <Form form={form} layout="vertical" requiredMark={false} className="platform-model-form">
      <Form.Item
        name="supplier"
        label="供应商"
        rules={[{ required: true, message: "请选择供应商" }]}
        extra={editing ? "供应商决定密钥归属；如需更换供应商，请新建模型配置。" : undefined}
      >
        <Select options={supplierOptions} onChange={changeSupplier} disabled={Boolean(credentialSource || editing)} placeholder="选择 API Key 所属供应商" />
      </Form.Item>
      {renderCredentialField()}
      {watchedSupplier === "custom" && !credentialSource ? (
        <Form.Item
          name="base_url"
          label="API 地址"
          rules={[{ required: true, message: "请输入 API 地址" }, { pattern: /^https:\/\/[^\s]+$/i, message: "第三方接口必须使用 HTTPS" }]}
          extra="填写到 API 版本层级，例如 https://api.example.com/v1。"
        >
          <Input
            placeholder="https://api.example.com/v1"
            autoComplete="url"
            onChange={() => {
              setAvailableModels([]);
              setModelOptionsOpen(false);
              setFilterAvailableModels(false);
            }}
          />
        </Form.Item>
      ) : null}
      <Form.Item
        name="model_name"
        label="模型名称"
        rules={[{ required: true, message: "请输入模型名称" }]}
        extra={<span className="platform-model-field-help">{credentialSource || editing ? "可使用已保存的密钥" : "选择供应商并填写 API Key"}，零 Token 获取模型列表。<Button type="link" size="small" loading={modelsLoading} onClick={() => {
          if (availableModels.length > 0) {
            setFilterAvailableModels(false);
            setModelOptionsOpen(true);
          }
          else void fetchAvailableModels();
        }}>{availableModels.length > 0 ? `已找到 ${availableModels.length} 个，点击选择` : "获取可用模型"}</Button></span>}
      >
        <AutoComplete
          options={availableModels.map((model) => ({ value: model }))}
          open={modelOptionsOpen && availableModels.length > 0}
          onOpenChange={(open) => setModelOptionsOpen(open && availableModels.length > 0)}
          onFocus={() => setModelOptionsOpen(availableModels.length > 0)}
          placeholder="获取后选择，也可手工输入模型名称"
          onChange={(value) => {
            if (availableModels.includes(value)) {
              setModelOptionsOpen(false);
              setFilterAvailableModels(false);
            } else if (availableModels.length > 0) {
              setFilterAvailableModels(true);
              setModelOptionsOpen(true);
            }
          }}
          filterOption={filterAvailableModels
            ? (value, option) => String(option?.value ?? "").toLowerCase().includes(value.toLowerCase())
            : false}
        />
      </Form.Item>
    </Form>
  );

  return (
    <div className="platform-model-page">
      <section className="page-heading platform-model-heading">
        <div>
          <h1>模型管理</h1>
          <p>选择学校，管理学校模型、三智能体评分和模型治理。</p>
        </div>
        <Space>
          <Button icon={<RefreshCw size={16} />} disabled={modelBusy || bindingsSaving} loading={schoolLoading || configLoading} onClick={() => {
            if (bindingsSavingRef.current || modelOperationRef.current) return;
            void loadSchools();
            void loadConfigs(selectedTenantID);
          }}>刷新</Button>
          {activeTab === "models" ? <Button type="primary" icon={<Plus size={16} />} disabled={!selectedTenantID || configLoading || modelBusy} onClick={openCreate}>添加模型</Button> : null}
        </Space>
      </section>

      <SchoolModelSelector
        schools={schools}
        selectedTenantID={selectedTenantID}
        currentConfig={currentConfig}
        loading={schoolLoading}
        disabled={drawerOpen || bindingsSaving || governanceSaving || modelBusy}
        activeCount={activeCount}
        healthyCount={healthyCount}
        capabilityCount={capabilityCount}
        onFilter={(filter) => changeBindingContext(() => { setModelFilter(filter); setActiveTab("models"); rememberModelConfigLocation(selectedTenantID, "models"); })}
        temporarilyUnavailableCount={temporarilyUnavailableCount}
        onChange={(tenantID) => changeBindingContext(() => {
          configRequestRef.current += 1;
          setSavedAccess(undefined); setModelFilter("all");
          setConfigs([]);
          setConfigLoading(true);
          rememberModelConfigLocation(tenantID, activeTab);
          setSelectedTenantID(tenantID);
        })}
      />

      <Alert
        className="platform-model-security-note"
        type="info"
        showIcon
        message="同一学校可为同一供应商和模型配置多个 API Key；密钥分别加密保存。模型库中的日常对话默认选择与下方三智能体绑定分开管理；完整能力检测通过后才能启用评分角色。"
      />

      <Segmented
        value={activeTab}
        disabled={bindingsSaving}
        options={[{ value: "models", label: "学校模型" }, { value: "bindings", label: "三智能体配置" }, { value: "governance", label: "治理与评测" }]}
        onChange={(value) => {
          const next = value as "models" | "bindings" | "governance";
          changeBindingContext(() => {
            setActiveTab(next);
            rememberModelConfigLocation(selectedTenantID, next);
          });
        }}
      />

      {activeTab === "models" ? <><div className="platform-model-library-heading">
        <h2 className="platform-model-section-title">学校模型库</h2>
        <Tooltip title="检测当前学校所有厂商的模型配置（含已停用配置），只检查连接，不发送模型生成请求">
          <Button icon={<Zap size={16} />} loading={Boolean(batchProbeProgress)}
            disabled={!selectedTenantID || configLoading || configs.length === 0 || modelBusy}
            onClick={() => void probeAll()}>
            {batchProbeProgress ? `检测中 ${batchProbeProgress.completed}/${batchProbeProgress.total}` : "检测全部连接"}
          </Button>
        </Tooltip>
      </div>
      {savedAccess?.tenantID === selectedTenantID ? <Alert type="success" showIcon message={`${savedAccess.config.display_name}：密钥及模型访问权限检查通过`} description={savedModelAccessDescription(configs.find((item) => item.id === savedAccess.config.id))} action={<Button disabled={modelBusy} onClick={() => void probe(savedAccess.config, "capability")}>检测回复与结构化能力</Button>} /> : null}
      <Space><span>{({ all: "全部配置", enabled: "已启用配置", connection: "已启用且访问检查通过", capability: "已启用且结构化能力已验证" })[modelFilter]} · {visibleConfigs.length} 个</span>{modelFilter !== "all" ? <Button onClick={() => setModelFilter("all")}>显示全部</Button> : null}</Space>
      <SchoolModelTable tenantID={selectedTenantID} loading={configLoading} columns={columns} configs={visibleConfigs} />
      </> : null}
      {activeTab === "bindings" ? <PanelModelBindingsSection tenantID={selectedTenantID} configs={configs} loadingConfigs={configLoading} onDirtyChange={setBindingsDirty} onSavingChange={handleBindingsSaving} onOpenGovernance={(view, scope) => changeBindingContext(() => {
        rememberModelConfigLocation(selectedTenantID, "governance");
        const [path, query = ""] = window.location.hash.split("?");
        const params = new URLSearchParams(query); params.set("view", view);
        // 评分组合入口没有题目风险事实；旧地址中的风险不能沿用为新组合的检查范围。
        params.delete("risk");
        if (scope) { params.set("stage", scope.stage); params.set("subject", scope.subject); params.set("archetype", scope.archetype); }
        window.history.replaceState(window.history.state, "", `${window.location.pathname}${window.location.search}${path}?${params}`);
        setActiveTab("governance");
      })} /> : null}
      {activeTab === "governance" ? <ManagedGovernanceWorkspace key={selectedTenantID} tenantID={selectedTenantID} schoolName={selectedSchool?.name} configs={configs} onDirtyChange={setGovernanceDirty} onSavingChange={handleGovernanceSaving} /> : null}

      <Drawer
        title={editing ? `编辑 ${editing.display_name}` : `为${selectedSchool?.name ?? "学校"}添加模型`}
        width={560}
        open={drawerOpen}
        closable={!saving}
        maskClosable={!saving}
        keyboard={!saving}
        onClose={closeDrawer}
        extra={<Space><Button disabled={saving} onClick={closeDrawer}>关闭</Button><Button type="primary" loading={saving} onClick={() => void save()}>{modelSaveLabel(editing)}</Button></Space>}
        destroyOnHidden
      >
        {renderModelForm()}
      </Drawer>
    </div>
  );
}
