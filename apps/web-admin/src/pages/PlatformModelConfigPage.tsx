import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  App as AntApp,
  AutoComplete,
  Button,
  Drawer,
  Dropdown,
  Form,
  Input,
  Segmented,
  Select,
  Space,
  Tooltip,
  type TableColumnsType
} from "antd";
import { KeyRound, MoreHorizontal, Pencil, Plus, RefreshCw, Zap } from "lucide-react";
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
import { StatusTag } from "../components/StatusTag";
import { onboardingQueryKey } from "../features/onboarding/queries";
import { SchoolModelSelector } from "../features/platform-model-config/components/SchoolModelSelector";
import { SchoolModelTable } from "../features/platform-model-config/components/SchoolModelTable";
import { useModelConfigDrafts } from "../features/platform-model-config/hooks/useModelConfigDrafts";
import {
  supplierOptions,
  supplierDefaults,
  formatDateTime,
  presetForConfig,
  modelConfigDraftKey,
  capabilityVerified,
  isTransientProbeError,
  connectionStatusLabel,
  probeFormatLabel,
  loadAllSchoolTenants,
  type SupplierPreset,
  type ConfigFormValues
} from "../features/platform-model-config/lib/modelConfig";
import { PanelModelBindingsSection } from "./PanelModelBindingsSection";
import { ManagedGovernanceWorkspace } from "../components/model-governance/ManagedGovernanceWorkspace";
import { hashQueryParam } from "../router/query";

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
  const [configs, setConfigs] = useState<ManagedModelAPIConfig[]>([]);
  const [configLoading, setConfigLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<ManagedModelAPIConfig | null>(null);
  const [credentialSource, setCredentialSource] = useState<ManagedModelAPIConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [probingID, setProbingID] = useState("");
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelOptionsOpen, setModelOptionsOpen] = useState(false);
  const [filterAvailableModels, setFilterAvailableModels] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const configRequestRef = useRef(0);
  const { clearFormDraft, saveFormDraft, restoreFormDraft } = useModelConfigDrafts(form);
  const watchedSupplier = Form.useWatch("supplier", form);

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
      setSelectedTenantID((current) => current && items.some((school) => school.id === current)
        ? current
        : items.find((school) => school.id === requestedTenantID)?.id ?? items.find((school) => school.status === "active")?.id ?? items[0]?.id ?? "");
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
      const temporarilyUnavailable = error instanceof ApiClientError && isTransientProbeError(error.code);
      const notify = temporarilyUnavailable ? message.warning : message.error;
      notify(`${managedModelDiscoveryErrorMessage(error, credentialSource !== null || Boolean(editing))}${temporarilyUnavailable ? "（本次暂时无法验证，不代表 API Key 无效）" : ""}`);
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
        setConfigs((current) => current.map((item) => item.id === response.config.id
          ? response.config
          : response.config.is_default ? { ...item, is_default: false } : item));
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
        const validationSummary = `API Key 有效 · 模型可用 · 0 生成 Token · ${response.validation.latency_ms}ms`;
        message.success(`${response.config.display_name} 已保存为备用模型（${validationSummary}）`);
      }
      clearFormDraft(draftKey);
      setDrawerOpen(false);
      setModelOptionsOpen(false);
      form.resetFields();
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      const temporarilyUnavailable = error instanceof ApiClientError && isTransientProbeError(error.code);
      const notify = temporarilyUnavailable ? message.warning : message.error;
      notify(`${getUserErrorMessage(error, editing ? "配置更新失败" : "配置保存失败")}${temporarilyUnavailable ? "（本次暂时无法验证，已保留当前填写内容）" : ""}`);
    } finally {
      setSaving(false);
    }
  };

  const closeDrawer = () => {
    if (saving) return;
    const values = form.getFieldsValue(true) as ConfigFormValues;
    const apiKey = String(values.api_key ?? "").trim();
    const baseURL = String(values.base_url ?? "").trim();
    const modelName = String(values.model_name ?? "").trim();
    const hasChanges = editing
      ? apiKey !== "" || baseURL !== editing.base_url || modelName !== editing.model_name
      : credentialSource
        ? modelName !== ""
        : Boolean(values.supplier || apiKey || baseURL || modelName);
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
    modal[temporarilyUnavailable ? "warning" : failed ? "error" : "info"]({
      title: temporarilyUnavailable
        ? `${config.display_name}：能力检测暂时无法完成`
        : failed ? `${config.display_name}：结构化输出检测未通过` : `${config.display_name}：能力检测详情`,
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
        const notify = isTransientProbeError(response.result.error_code) ? message.warning : message.error;
        notify(`${config.display_name}：${response.result.message}${isTransientProbeError(response.result.error_code) ? "（本次暂时无法验证，不代表模型已失效）" : ""}`);
        if (mode === "capability") showCapabilityDetails(response.config, response.result);
      }
    } catch (error) {
      await loadConfigs(selectedTenantID);
      const notify = error instanceof ApiClientError && isTransientProbeError(error.code) ? message.warning : message.error;
      notify(getUserErrorMessage(error, `${config.display_name}连接失败`));
    } finally {
      setProbingID("");
    }
  }, [loadConfigs, message, queryClient, selectedTenantID, showCapabilityDetails]);

  const updateUsage = useCallback(async (config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) => {
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
      setConfigs((current) => current.map((item) => item.id === response.config.id
        ? response.config
        : response.config.is_default ? { ...item, is_default: false } : item));
      message.success(isDefault
        ? `${config.display_name} 已设为日常对话模型`
        : config.is_default ? "日常对话已切回本地模型"
        : status === "disabled" ? `${config.display_name} 已停用` : `${config.display_name} 已启用`);
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      message.error(getUserErrorMessage(error));
    }
  }, [message, queryClient, selectedTenantID]);

  const removeConfig = useCallback((config: ManagedModelAPIConfig) => {
    modal.confirm({
      title: `删除 ${config.display_name}？`,
      content: "删除后无法恢复，已保存的 API Key 也会一并移除。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        try {
          await deleteManagedModelAPIConfig(config.id, selectedTenantID);
          setConfigs((current) => current.filter((item) => item.id !== config.id));
          message.success(`${config.display_name} 已删除`);
          void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
        } catch (error) {
          message.error(getUserErrorMessage(error));
        }
      }
    });
  }, [message, modal, queryClient, selectedTenantID]);

  const columns = useMemo<TableColumnsType<ManagedModelAPIConfig>>(() => [
    {
      title: "模型",
      key: "identity",
      render: (_value, config) => (
        <div className="platform-model-identity">
          <span className={config.status === "active" ? "active" : "disabled"}><Zap size={16} /></span>
          <div>
            <strong>{config.display_name}</strong>
            <small>{config.model_name}</small>
            <small>配置 {config.id.slice(0, 8)}</small>
          </div>
        </div>
      )
    },
    {
      title: "密钥",
      key: "credential",
      width: 130,
      render: (_value, config) => (
        <div className="platform-model-secret">
          <KeyRound size={14} />
          <span>已配置 · {config.credential_hint || "已加密"}</span>
        </div>
      )
    },
    {
      title: "连接状态",
      key: "probe",
      width: 210,
      render: (_value, config) => (
        <div className="platform-model-probe">
          <StatusTag tone={config.last_test_status === "success" ? "success" : config.last_test_status === "temporary_unavailable" ? "warning" : config.last_test_status === "failed" ? "danger" : "neutral"}>
            {config.last_test_status === "success" ? "连接正常" : connectionStatusLabel(config)}
          </StatusTag>
          <small title={config.last_test_message}>{(config.last_test_status === "failed" || config.last_test_status === "temporary_unavailable") && config.last_test_message
            ? config.last_test_message
            : <>{config.last_test_latency_ms ? `${config.last_test_latency_ms}ms · ` : ""}{formatDateTime(config.last_tested_at)}</>}</small>
          {config.last_test_status === "temporary_unavailable" ? <small>上次连接正常：{config.last_successful_tested_at ? formatDateTime(config.last_successful_tested_at) : "暂无记录"}</small> : null}
          <small title={config.last_capability_message}>结构化能力：{config.last_capability_status === "success"
            ? config.last_capability_probe_version === "structured-json-v3"
              ? `已验证 · ${formatDateTime(config.last_capability_tested_at)}`
              : "待按新策略复验"
            : config.last_capability_status === "failed" ? "验证失败" : "未验证"}</small>
          {config.last_capability_status === "failed" ? <Button className="platform-model-detail-link" type="link" size="small" onClick={() => showCapabilityDetails(config)}>查看原因</Button> : null}
        </div>
      )
    },
    {
      title: "用途",
      key: "assignment",
      width: 120,
      render: (_value, config) => config.is_default
        ? <StatusTag tone="info">日常对话</StatusTag>
        : <StatusTag tone={config.status === "active" ? "success" : "neutral"}>{config.status === "active" ? "备用" : "已停用"}</StatusTag>
    },
    {
      title: "操作",
      key: "actions",
      width: 270,
      render: (_value, config) => (
        <Space size={6}>
          <Tooltip title="只检查接口、密钥和模型权限，不发送模型生成请求">
            <Button size="small" icon={<Zap size={14} />} loading={probingID === config.id} onClick={() => void probe(config, "quick")}>检测连接</Button>
          </Tooltip>
          {config.status === "active" ? <Button size="small" icon={<Plus size={14} />} onClick={() => openCreateWithKey(config)}>同 Key 模型</Button> : null}
          <Tooltip title="编辑配置或更换密钥">
            <Button size="small" type="text" icon={<Pencil size={14} />} aria-label={`编辑${config.display_name}`} onClick={() => openEdit(config)} />
          </Tooltip>
          <Dropdown
            trigger={["click"]}
            menu={{ items: [
              { key: "capability", label: "完整能力检测" },
              ...(config.status === "active" && !config.is_default ? [{
                key: "current",
                label: capabilityVerified(config) ? "设为日常对话模型" : "设为日常对话模型（需先完整检测）",
                disabled: !capabilityVerified(config)
              }] : []),
              ...(config.is_default ? [{ key: "local", label: "日常对话切回本地模型" }] : []),
              ...(!config.is_default ? [{ key: config.status === "active" ? "disable" : "enable", label: config.status === "active" ? "停用" : "启用" }] : []),
              ...(config.status === "disabled" ? [{ key: "delete", label: "删除", danger: true }] : [])
            ], onClick: async ({ key }) => {
              if (key === "capability") {
                await probe(config, "capability");
                return;
              }
              if (key === "delete") {
                removeConfig(config);
                return;
              }
              const status = key === "disable" ? "disabled" : "active";
              await updateUsage(config, status, key === "current" ? true : key === "local" ? false : config.is_default);
            } }}
          >
            <Button size="small" type="text" icon={<MoreHorizontal size={15} />} aria-label={`${config.display_name}更多操作`} />
          </Dropdown>
        </Space>
      )
    }
  ], [openCreateWithKey, openEdit, probe, probingID, removeConfig, showCapabilityDetails, updateUsage]);

  const activeCount = configs.filter((config) => config.status === "active").length;
  const healthyCount = configs.filter((config) => config.last_test_status === "success").length;
  const temporarilyUnavailableCount = configs.filter((config) => config.last_test_status === "temporary_unavailable").length;

  return (
    <div className="platform-model-page">
      <section className="page-heading platform-model-heading">
        <div>
          <h1>模型管理</h1>
          <p>选择学校，管理学校模型、三智能体评分和模型治理。</p>
        </div>
        <Space>
          <Button icon={<RefreshCw size={16} />} loading={schoolLoading || configLoading} onClick={() => { void loadSchools(); void loadConfigs(selectedTenantID); }}>刷新</Button>
          {activeTab === "models" ? <Button type="primary" icon={<Plus size={16} />} disabled={!selectedTenantID || configLoading} onClick={openCreate}>添加模型</Button> : null}
        </Space>
      </section>

      <SchoolModelSelector
        schools={schools}
        selectedTenantID={selectedTenantID}
        currentConfig={currentConfig}
        loading={schoolLoading}
        disabled={drawerOpen || saving || Boolean(probingID)}
        activeCount={activeCount}
        healthyCount={healthyCount}
        temporarilyUnavailableCount={temporarilyUnavailableCount}
        onChange={(tenantID) => {
          configRequestRef.current += 1;
          setConfigs([]);
          setConfigLoading(true);
          setSelectedTenantID(tenantID);
        }}
      />

      <Alert
        className="platform-model-security-note"
        type="info"
        showIcon
        message="同一学校可为同一供应商和模型配置多个 API Key；密钥分别加密保存。模型库中的日常对话默认选择与下方三智能体绑定分开管理；完整能力检测通过后才能启用评分角色。"
      />

      <Segmented
        value={activeTab}
        options={[{ value: "models", label: "学校模型" }, { value: "bindings", label: "三智能体配置" }, { value: "governance", label: "治理与评测" }]}
        onChange={(value) => {
          const next = value as "models" | "bindings" | "governance";
          setActiveTab(next);
          window.location.hash = `/admin/platform/model-config?tab=${next}`;
        }}
      />

      {activeTab === "models" ? <><h2 className="platform-model-section-title">学校模型库</h2>
      <SchoolModelTable tenantID={selectedTenantID} loading={configLoading} columns={columns} configs={configs} />
      </> : null}
      {activeTab === "bindings" ? <PanelModelBindingsSection tenantID={selectedTenantID} configs={configs} loadingConfigs={configLoading} /> : null}
      {activeTab === "governance" ? <ManagedGovernanceWorkspace tenantID={selectedTenantID} configs={configs} /> : null}

      <Drawer
        title={editing ? `编辑 ${editing.display_name}` : `为${selectedSchool?.name ?? "学校"}添加模型`}
        width={560}
        open={drawerOpen}
        closable={!saving}
        maskClosable={!saving}
        keyboard={!saving}
        onClose={closeDrawer}
        extra={<Space><Button disabled={saving} onClick={closeDrawer}>关闭</Button><Button type="primary" loading={saving} onClick={() => void save()}>{editing?.is_default ? "验证并更新" : editing ? "检测连接并更新" : "检测连接并保存"}</Button></Space>}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} className="platform-model-form">
          <Form.Item
            name="supplier"
            label="供应商"
            rules={[{ required: true, message: "请选择供应商" }]}
            extra={editing ? "供应商决定密钥归属；如需更换供应商，请新建模型配置。" : undefined}
          >
            <Select options={supplierOptions} onChange={changeSupplier} disabled={Boolean(credentialSource || editing)} placeholder="选择 API Key 所属供应商" />
          </Form.Item>
          {credentialSource ? <Alert type="info" showIcon message={`沿用已保存的密钥（${credentialSource.credential_hint || "已加密"}）`} description="新模型会单独验证并加密保存；后续更换密钥需逐个更新模型配置。" /> : <Form.Item
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
          </Form.Item>}
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
              onDropdownVisibleChange={(open) => setModelOptionsOpen(open && availableModels.length > 0)}
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
      </Drawer>
    </div>
  );
}
