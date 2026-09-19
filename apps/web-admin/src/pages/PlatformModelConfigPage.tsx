import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  App as AntApp,
  AutoComplete,
  Button,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  Select,
  Space,
  Tooltip,
  type TableColumnsType
} from "antd";
import { Building2, CheckCircle2, KeyRound, MoreHorizontal, Pencil, Plus, RefreshCw, ShieldCheck, Unplug, Zap } from "lucide-react";
import {
  autoCreateManagedModelAPIConfig,
  deleteManagedModelAPIConfig,
  listAvailableManagedModels,
  listManagedModelAPIConfigs,
  probeManagedModelAPIConfig,
  updateManagedModelAPIConfig,
  type ManagedAdapterType,
  type ManagedAPIProbeMode,
  type ManagedAPIProbeResult,
  type ManagedModelAPIConfig
} from "../api/modelApiConfig";
import { getUserErrorMessage } from "../api/client";
import { listTenants, type Tenant } from "../api/org";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { onboardingQueryKey } from "../features/onboarding/queries";

type SupplierPreset = "aliyun" | "deepseek" | "openai" | "zhipu" | "moonshot" | "anthropic" | "gemini" | "custom";

interface ConfigFormValues {
  supplier: SupplierPreset;
  provider_key?: string;
  display_name?: string;
  adapter_type?: ManagedAdapterType;
  base_url?: string;
  api_key: string;
  model_name: string;
  model_version?: string;
  region?: string;
  enabled?: boolean;
  is_default?: boolean;
}

const supplierOptions = [
  { value: "aliyun", label: "阿里云百炼" },
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "zhipu", label: "智谱 AI" },
  { value: "moonshot", label: "Moonshot / Kimi" },
  { value: "anthropic", label: "Anthropic" },
  { value: "gemini", label: "Google Gemini" },
  { value: "custom", label: "其他兼容接口" }
];

const supplierDefaults: Record<SupplierPreset, Partial<ConfigFormValues>> = {
  aliyun: { provider_key: "aliyun", display_name: "阿里云百炼", adapter_type: "openai_compatible", region: "cn" },
  deepseek: { provider_key: "deepseek", display_name: "DeepSeek", adapter_type: "openai_compatible", region: "global" },
  openai: { provider_key: "openai", display_name: "OpenAI", adapter_type: "openai_compatible", region: "global" },
  zhipu: { provider_key: "zhipu", display_name: "智谱 AI", adapter_type: "openai_compatible", region: "cn" },
  moonshot: { provider_key: "moonshot", display_name: "Moonshot / Kimi", adapter_type: "openai_compatible", region: "cn" },
  anthropic: { provider_key: "anthropic", display_name: "Anthropic", adapter_type: "openai_compatible", region: "global" },
  gemini: { provider_key: "gemini", display_name: "Google Gemini", adapter_type: "openai_compatible", region: "global" },
  custom: { provider_key: "custom", display_name: "自定义供应商", adapter_type: "openai_compatible", region: "global" }
};

function formatDateTime(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  }).format(date);
}

function presetForConfig(config: ManagedModelAPIConfig): SupplierPreset {
  if (config.provider_key === "aliyun") return "aliyun";
  if (config.provider_key === "deepseek") return "deepseek";
  if (config.provider_key === "openai") return "openai";
  if (config.provider_key === "zhipu") return "zhipu";
  if (config.provider_key === "moonshot") return "moonshot";
  if (config.provider_key === "anthropic") return "anthropic";
  if (config.provider_key === "gemini") return "gemini";
  return "custom";
}

function capabilityVerified(config: ManagedModelAPIConfig) {
  return config.last_capability_status === "success" && config.last_capability_probe_version === "structured-json-v3";
}

function probeFormatLabel(value?: string) {
  if (value === "json_object") return "JSON Object 模式";
  if (value === "json_schema") return "严格 JSON Schema";
  if (value === "prompt_only") return "提示词约束";
  return "—";
}

async function loadAllSchoolTenants() {
  const schools: Tenant[] = [];
  let cursor = "";
  for (let page = 0; page < 20; page += 1) {
    const response = await listTenants({ limit: 100, cursor: cursor || undefined });
    schools.push(...response.tenants.filter((tenant) => tenant.code !== "platform"));
    if (!response.has_more || !response.next_cursor) break;
    cursor = response.next_cursor;
  }
  return schools;
}

export function PlatformModelConfigPage() {
  const queryClient = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const [form] = Form.useForm<ConfigFormValues>();
  const [schools, setSchools] = useState<Tenant[]>([]);
  const [schoolLoading, setSchoolLoading] = useState(true);
  const [selectedTenantID, setSelectedTenantID] = useState("");
  const [configs, setConfigs] = useState<ManagedModelAPIConfig[]>([]);
  const [configLoading, setConfigLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<ManagedModelAPIConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [probingID, setProbingID] = useState("");
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelOptionsOpen, setModelOptionsOpen] = useState(false);
  const [filterAvailableModels, setFilterAvailableModels] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const configRequestRef = useRef(0);
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
      setSelectedTenantID((current) => current && items.some((school) => school.id === current)
        ? current
        : items.find((school) => school.status === "active")?.id ?? items[0]?.id ?? "");
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
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    form.setFieldsValue({
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
  };

  const openEdit = useCallback((config: ManagedModelAPIConfig) => {
    setEditing(config);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    form.setFieldsValue({
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
  }, [form]);

  const changeSupplier = (supplier: SupplierPreset) => {
    if (editing) return;
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
      await form.validateFields(supplier === "custom" ? ["supplier", "api_key", "base_url"] : ["supplier", "api_key"]);
      values = form.getFieldsValue();
    } catch {
      return;
    }
    setModelsLoading(true);
    try {
      const response = await listAvailableManagedModels({
        tenant_id: selectedTenantID,
        api_key: values.api_key.trim(),
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
      message.error(getUserErrorMessage(error, "获取模型列表失败"));
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
    setSaving(true);
    try {
      if (editing) {
        const response = await updateManagedModelAPIConfig(editing.id, selectedTenantID, {
          display_name: editing.display_name,
          adapter_type: editing.adapter_type,
          base_url: editing.base_url,
          api_key: values.api_key.trim(),
          model_name: values.model_name.trim(),
          model_version: values.model_name.trim(),
          region: editing.region,
          status: editing.status,
          is_default: editing.is_default
        });
        setConfigs((current) => current.map((item) => item.id === response.config.id
          ? response.config
          : response.config.is_default ? { ...item, is_default: false } : item));
        message.success(values.api_key ? "配置和密钥已更新" : "配置已更新");
      } else {
        const response = await autoCreateManagedModelAPIConfig({
          tenant_id: selectedTenantID,
          api_key: values.api_key.trim(),
          model_name: values.model_name.trim(),
          provider: values.supplier,
          base_url: values.supplier === "custom" ? values.base_url?.trim() : undefined
        });
        setConfigs((current) => [response.config, ...current.map((item) => response.config.is_default ? { ...item, is_default: false } : item)]);
        const validationSummary = `API Key 有效 · 模型可用 · 0 生成 Token · ${response.validation.latency_ms}ms`;
        message.success(`${response.config.display_name} 已保存为备用模型（${validationSummary}）`);
      }
      setDrawerOpen(false);
      setModelOptionsOpen(false);
      form.resetFields();
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      message.error(getUserErrorMessage(error, editing ? "配置更新失败" : "配置保存失败"));
    } finally {
      setSaving(false);
    }
  };

  const showCapabilityDetails = useCallback((config: ManagedModelAPIConfig, result?: ManagedAPIProbeResult) => {
    const diagnostic = result?.diagnostic ?? config.last_capability_diagnostic ?? {};
    const usage = result?.usage ?? config.last_capability_usage;
    const failed = result ? !result.ok : config.last_capability_status === "failed";
    modal[failed ? "error" : "info"]({
      title: failed ? `${config.display_name}：结构化输出检测未通过` : `${config.display_name}：能力检测详情`,
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
        message.error(`${config.display_name}：${response.result.message}`);
        if (mode === "capability") showCapabilityDetails(response.config, response.result);
      }
    } catch (error) {
      await loadConfigs(selectedTenantID);
      message.error(getUserErrorMessage(error, `${config.display_name}连接失败`));
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
        ? `${config.display_name} 已设为当前使用`
        : config.is_default ? "已切回本地模型"
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
          <StatusTag tone={config.last_test_status === "success" ? "success" : config.last_test_status === "failed" ? "danger" : "neutral"}>
            {config.last_test_status === "success"
              ? "连接正常"
              : config.last_test_status === "failed" ? "异常" : "未测试"}
          </StatusTag>
          <small title={config.last_test_message}>{config.last_test_status === "failed" && config.last_test_message
            ? config.last_test_message
            : <>{config.last_test_latency_ms ? `${config.last_test_latency_ms}ms · ` : ""}{formatDateTime(config.last_tested_at)}</>}</small>
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
        ? <StatusTag tone="info">当前使用</StatusTag>
        : <StatusTag tone={config.status === "active" ? "success" : "neutral"}>{config.status === "active" ? "备用" : "已停用"}</StatusTag>
    },
    {
      title: "操作",
      key: "actions",
      width: 170,
      render: (_value, config) => (
        <Space size={6}>
          <Tooltip title="只检查接口、密钥和模型权限，不发送模型生成请求">
            <Button size="small" icon={<Zap size={14} />} loading={probingID === config.id} onClick={() => void probe(config, "quick")}>检测连接</Button>
          </Tooltip>
          <Tooltip title="编辑配置或更换密钥">
            <Button size="small" type="text" icon={<Pencil size={14} />} aria-label={`编辑${config.display_name}`} onClick={() => openEdit(config)} />
          </Tooltip>
          <Dropdown
            trigger={["click"]}
            menu={{ items: [
              { key: "capability", label: "完整能力检测" },
              ...(config.status === "active" && !config.is_default ? [{
                key: "current",
                label: capabilityVerified(config) ? "设为当前使用" : "设为当前使用（需先完整检测）",
                disabled: !capabilityVerified(config)
              }] : []),
              ...(config.is_default ? [{ key: "local", label: "切回本地模型" }] : []),
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
  ], [openEdit, probe, probingID, removeConfig, showCapabilityDetails, updateUsage]);

  const activeCount = configs.filter((config) => config.status === "active").length;
  const healthyCount = configs.filter((config) => config.last_test_status === "success").length;

  return (
    <div className="platform-model-page">
      <section className="page-heading platform-model-heading">
        <div>
          <span className="platform-model-kicker"><ShieldCheck size={15} /> 平台级密钥托管</span>
          <h1>AI 模型接入</h1>
          <p>选择学校，配置考试资料解析使用的模型和访问密钥。</p>
        </div>
        <Space>
          <Button icon={<RefreshCw size={16} />} loading={schoolLoading || configLoading} onClick={() => { void loadSchools(); void loadConfigs(selectedTenantID); }}>刷新</Button>
          <Button type="primary" icon={<Plus size={16} />} disabled={!selectedTenantID || configLoading} onClick={openCreate}>添加模型</Button>
        </Space>
      </section>

      <section className="platform-model-schoolbar">
        <div className="platform-model-school-select">
          <label htmlFor="platform-model-school">配置学校</label>
          <Select
            id="platform-model-school"
            showSearch
            optionFilterProp="label"
            loading={schoolLoading}
            disabled={drawerOpen || saving || Boolean(probingID)}
            value={selectedTenantID || undefined}
            placeholder="选择一所学校"
            options={schools.map((school) => ({ value: school.id, label: `${school.name} · ${school.code}`, disabled: school.status !== "active" }))}
            onChange={(tenantID) => {
              configRequestRef.current += 1;
              setConfigs([]);
              setConfigLoading(true);
              setSelectedTenantID(tenantID);
            }}
          />
        </div>
        <div className="platform-model-school-summary">
          <span><Building2 size={15} /> 当前模型：{currentConfig ? `${currentConfig.display_name} · ${currentConfig.model_name} · ${currentConfig.last_test_status === "success" ? "正常" : currentConfig.last_test_status === "failed" ? "异常" : "未测试"}` : "本地模型"}</span>
          <span><Zap size={15} /> {activeCount} 个可用配置</span>
          <span><CheckCircle2 size={15} /> {healthyCount} 个连接正常</span>
        </div>
      </section>

      <Alert
        className="platform-model-security-note"
        type="info"
        showIcon
        message="密钥加密保存且学校配置相互隔离。连接正常即可保存为备用模型；通过完整能力检测后，才能设为当前使用。"
      />

      <section className="platform-model-table-shell">
        {selectedTenantID ? (
          <ResponsiveTable<ManagedModelAPIConfig>
            className="dense-data-table"
            rowKey="id"
            loading={configLoading}
            columns={columns}
            dataSource={configs}
            pagination={false}
            locale={{ emptyText: <Empty image={<Unplug size={38} />} description={<span>暂未配置 AI 模型<br /><small>选择供应商并准备 API Key 即可获取模型</small></span>} /> }}
          />
        ) : (
          <Empty description="请先选择学校" />
        )}
      </section>

      <Drawer
        title={editing ? `编辑 ${editing.display_name}` : `为${selectedSchool?.name ?? "学校"}添加模型`}
        width={560}
        open={drawerOpen}
        closable={!saving}
        maskClosable={!saving}
        keyboard={!saving}
        onClose={() => { if (!saving) { setDrawerOpen(false); setModelOptionsOpen(false); form.resetFields(); } }}
        extra={<Space><Button disabled={saving} onClick={() => setDrawerOpen(false)}>取消</Button><Button type="primary" loading={saving} onClick={() => void save()}>{editing?.is_default ? "验证并更新" : editing ? "检测连接并更新" : "检测连接并保存"}</Button></Space>}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} className="platform-model-form">
          {!editing ? (
            <Form.Item name="supplier" label="供应商" rules={[{ required: true, message: "请选择供应商" }]}>
              <Select options={supplierOptions} onChange={changeSupplier} placeholder="选择 API Key 所属供应商" />
            </Form.Item>
          ) : null}
          <Form.Item
            name="api_key"
            label={editing ? `API Key（当前 ${editing.credential_hint ?? "已配置"}）` : "API Key"}
            rules={editing ? [{ min: 16, message: "密钥至少 16 个字符" }] : [{ required: true, message: "请输入 API Key" }, { min: 16, message: "密钥至少 16 个字符" }]}
            extra={editing ? "留空则继续使用原密钥；填写新密钥后会先验证再替换。" : "密钥将加密保存；保存只做零生成 Token 连接检测。"}
          >
            <Input.Password
              prefix={<KeyRound size={15} />}
              maxLength={1024}
              autoComplete="new-password"
              placeholder={editing ? `当前 ${editing.credential_hint ?? "已配置"}` : "输入供应商密钥"}
              onChange={() => {
                if (!editing) {
                  setAvailableModels([]);
                  setModelOptionsOpen(false);
                  setFilterAvailableModels(false);
                }
              }}
            />
          </Form.Item>
          {!editing && watchedSupplier === "custom" ? (
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
            extra={!editing ? <span className="platform-model-field-help">选择供应商并填写 API Key 后，可零 Token 获取模型列表。<Button type="link" size="small" loading={modelsLoading} onClick={() => {
              if (availableModels.length > 0) {
                setFilterAvailableModels(false);
                setModelOptionsOpen(true);
              }
              else void fetchAvailableModels();
            }}>{availableModels.length > 0 ? `已找到 ${availableModels.length} 个，点击选择` : "获取可用模型"}</Button></span> : undefined}
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
          {editing ? (
            <div className="platform-model-readonly-details" aria-label="只读技术信息">
              <span>供应商：{editing.display_name}</span>
              <span>接口：{editing.base_url}</span>
              <span>区域：{editing.region}</span>
            </div>
          ) : null}
        </Form>
      </Drawer>
    </div>
  );
}
