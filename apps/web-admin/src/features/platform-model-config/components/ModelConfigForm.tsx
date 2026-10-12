// 表单只整理输入和密钥复用约束；保存、学校范围和网络请求由页面持有。
import { Alert, AutoComplete, Button, Form, Input, Select, type FormInstance } from "antd";
import { KeyRound } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { presetForConfig, supplierOptions, type ConfigFormValues, type SupplierPreset } from "../lib/modelConfig";

export function ModelConfigForm({
  form, editing, credentialSource, watchedSupplier, changeSupplier, availableModels, modelOptionsOpen,
  filterAvailableModels, modelsLoading, setAvailableModels, setModelOptionsOpen, setFilterAvailableModels, fetchAvailableModels,
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
  setAvailableModels: (models: string[]) => void;
  setModelOptionsOpen: (open: boolean) => void;
  setFilterAvailableModels: (filter: boolean) => void;
  fetchAvailableModels: () => Promise<void>;
}>) {
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

}
