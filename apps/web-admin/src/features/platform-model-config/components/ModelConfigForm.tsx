// 表单只整理输入和密钥复用约束；保存、学校范围和网络请求由页面持有。
import { Alert, AutoComplete, Button, Form, Input, InputNumber, Select, type FormInstance } from "antd";
import { KeyRound } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { isSameModelAPIOrigin, presetForConfig, supplierOptions, type ConfigFormValues, type SupplierPreset } from "../lib/modelConfig";

export function ModelConfigForm({
  form, editing, credentialSource, watchedSupplier, changeSupplier, availableModels, modelOptionsOpen,
  filterAvailableModels, modelsLoading, discoveryDisabledReason, setAvailableModels, setModelOptionsOpen, setFilterAvailableModels, fetchAvailableModels, onConnectionChange,
}: Readonly<{
  form: FormInstance<ConfigFormValues>;
  editing: ManagedModelAPIConfig | null;
  credentialSource: ManagedModelAPIConfig | null;
  watchedSupplier?: SupplierPreset;
  changeSupplier: (supplier: SupplierPreset) => void;
  availableModels: string[];
  modelOptionsOpen: boolean;
  filterAvailableModels: boolean;
  modelsLoading: boolean;
  discoveryDisabledReason?: string;
  setAvailableModels: (models: string[]) => void;
  setModelOptionsOpen: (open: boolean) => void;
  setFilterAvailableModels: (filter: boolean) => void;
  fetchAvailableModels: () => Promise<void>;
  onConnectionChange?: () => void;
}>) {
  const baseURL = Form.useWatch("base_url", form);
  const modelName = Form.useWatch("model_name", form);
  const clearContextPolicy = () => form.setFieldsValue({ context_window_tokens: undefined, max_input_tokens: undefined, max_output_tokens: undefined, tokenizer: undefined });
  const originChanged = Boolean(editing && presetForConfig(editing) === "custom" &&
    !isSameModelAPIOrigin(editing.base_url, String(baseURL ?? editing.base_url)));
  const renderCredentialField = () => {
    if (credentialSource) {
      return <Alert type="info" showIcon message={`沿用已保存的密钥（${credentialSource.credential_hint || "已加密"}）`} description="此配置将单独验证并加密保存；轮换时可在学校模型库中批量更换同一供应商连接的密钥。" />;
    }
    let credentialNotice = "密钥将加密保存。保存时检查连接，不生成回复。";
    if (editing) {
      credentialNotice = originChanged
        ? "API 域名或端口已变，请重新输入密钥；可以使用原密钥。"
        : "已保存的密钥不回显；留空继续使用。";
    }
    return <Form.Item
      name="api_key"
      label={editing ? `API Key（当前 ${editing.credential_hint ?? "已配置"}）` : "API Key"}
      dependencies={editing ? ["base_url"] : undefined}
      rules={editing ? [
        { min: 16, message: "密钥至少 16 个字符" },
        ({ getFieldValue }) => ({
          validator: async (_rule, value) => {
            const originChanged = presetForConfig(editing) === "custom" &&
              !isSameModelAPIOrigin(editing.base_url, String(getFieldValue("base_url") ?? ""));
            if (originChanged && !String(value ?? "").trim()) {
              throw new Error("更换 API 域名或端口时，请重新输入 API Key（可以使用原密钥）");
            }
          }
        })
      ] : [{ required: true, message: "请输入 API Key" }, { min: 16, message: "密钥至少 16 个字符" }]}
      extra={credentialNotice}
    >
      <Input.Password
        prefix={<KeyRound size={15} />}
        maxLength={1024}
        autoComplete="new-password"
        placeholder={editing ? `当前 ${editing.credential_hint ?? "已配置"}` : "输入供应商密钥"}
        onChange={() => {
          onConnectionChange?.();
          setAvailableModels([]);
          setModelOptionsOpen(false);
          setFilterAvailableModels(false);
        }}
      />
    </Form.Item>;
  };

  return (
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
              onConnectionChange?.();
              clearContextPolicy();
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
        extra={<span className="platform-model-field-help"><span>{discoveryDisabledReason ?? "可获取模型列表，也可直接输入模型名称；获取列表不生成回复。"}</span><Button type="link" size="small" loading={modelsLoading} disabled={Boolean(discoveryDisabledReason)} onClick={() => {
          if (availableModels.length > 0) {
            setFilterAvailableModels(false);
            setModelOptionsOpen(true);
          }
          else void fetchAvailableModels();
        }}>{availableModels.length > 0 ? `已找到 ${availableModels.length} 个，点击选择` : "获取可用模型"}</Button></span>}
      >
        <AutoComplete
          options={availableModels.map((model) => ({ value: model }))}
          open={!discoveryDisabledReason && modelOptionsOpen && availableModels.length > 0}
          onOpenChange={(open) => setModelOptionsOpen(open && availableModels.length > 0)}
          onFocus={() => setModelOptionsOpen(availableModels.length > 0)}
          placeholder="获取后选择，也可手工输入模型名称"
          onChange={(value) => {
            // 模型变更后容量需要重新核对，不能把旧模型的大窗口无声带给新模型。
            if (value.trim() !== String(modelName ?? "").trim()) clearContextPolicy();
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
      <details>
        <summary>对话容量设置</summary>
        <p>请按供应商文档填写 Token 上限。新建或更换模型时全部留空，使用应用默认预算：上下文 32,768，输入 27,648，输出 4,096；需与实际模型容量核对。编辑原模型时全部留空会保留已有设置；部分留空按默认规则计算。</p>
        <Form.Item name="tokenizer" label="文本计数方式" extra="只有供应商确认模型使用相应编码时才选择该编码。消息协议与图片仍有估算余量；未知模型使用保守估算。">
          <Select allowClear placeholder="保守估算（默认）" options={[
            { value: "utf8_bytes", label: "保守估算" },
            { value: "cl100k_base", label: "cl100k_base 编码" },
            { value: "o200k_base", label: "o200k_base 编码" }
          ]} />
        </Form.Item>
        <Form.Item name="context_window_tokens" label="上下文上限" rules={[{ type: "integer", min: 2_048, max: 2_097_152, message: "请输入 2,048 至 2,097,152 的整数" }]}>
          <InputNumber min={2_048} max={2_097_152} precision={0} placeholder="32768" style={{ width: "100%" }} />
        </Form.Item>
        <Form.Item name="max_output_tokens" label="单次输出上限" rules={[{ type: "integer", min: 128, max: 32_768, message: "请输入 128 至 32,768 的整数" }]}>
          <InputNumber min={128} max={32_768} precision={0} placeholder="4096" style={{ width: "100%" }} />
        </Form.Item>
        <Form.Item name="max_input_tokens" label="输入上限" dependencies={["context_window_tokens", "max_output_tokens"]}
          extra="输入与输出之和还需为上下文留出安全余量；输入留空时自动计算。"
          rules={[
            { type: "integer", min: 256, max: 2_097_152, message: "请输入 256 至 2,097,152 的整数" },
            ({ getFieldValue }) => ({ validator: async (_rule, value) => {
              const window = getFieldValue("context_window_tokens") ?? 32_768;
              const output = getFieldValue("max_output_tokens") ?? Math.min(4_096, Math.floor(window / 4));
              const safety = Math.min(1_024, Math.max(128, Math.floor(window / 16)));
              if (output + safety + (value ?? 256) > window) throw new Error("输入、输出与安全余量之和超过上下文上限");
            } })
          ]}>
          <InputNumber min={256} max={2_097_152} precision={0} placeholder="自动计算" style={{ width: "100%" }} />
        </Form.Item>
      </details>
    </Form>
  );

}
