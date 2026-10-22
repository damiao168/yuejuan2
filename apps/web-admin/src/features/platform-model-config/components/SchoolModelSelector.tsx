import { Button, Select, Tooltip } from "antd";
import { Building2, CheckCircle2, Zap } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import type { Tenant } from "../../../api/org";
import { textStatusLabel } from "../lib/modelConfig";

export function SchoolModelSelector({
  schools,
  selectedTenantID,
  currentConfig,
  loading,
  disabled,
  activeCount,
  healthyCount,
  textCount = 0,
  temporarilyUnavailableCount,
  capabilityCount = 0,
  chatModelCount,
  localSwitchBusy = false,
  onUseLocal,
  onFilter,
  onChange
}: Readonly<{
  schools: Tenant[];
  selectedTenantID: string;
  currentConfig?: ManagedModelAPIConfig;
  loading: boolean;
  disabled: boolean;
  activeCount: number;
  healthyCount: number;
  textCount?: number;
  temporarilyUnavailableCount: number;
  capabilityCount?: number;
  chatModelCount?: number;
  localSwitchBusy?: boolean;
  onUseLocal?: () => void;
  onFilter?: (filter: "enabled" | "connection" | "text" | "capability") => void;
  onChange: (tenantID: string) => void;
}>) {
  return (
    <section className="platform-model-schoolbar">
      <div className="platform-model-school-select">
        <label htmlFor="platform-model-school">配置学校</label>
        <Select
          id="platform-model-school"
          showSearch
          optionFilterProp="label"
          loading={loading}
          disabled={disabled}
          value={selectedTenantID || undefined}
          placeholder="选择一所学校"
          options={schools.map((school) => ({ value: school.id, label: `${school.name} · ${school.code}`, disabled: school.status !== "active" }))}
          onChange={onChange}
        />
      </div>
      <div className="platform-model-school-summary">
        <span><Building2 size={15} /> 对话已开放 {chatModelCount ?? "—"} 个，学校管理员可切换</span>
        <div className="platform-model-conversation-summary"><span>默认配置：{currentConfig ? `${currentConfig.display_name} · ${currentConfig.model_name} · 普通回复${textStatusLabel(currentConfig)}` : "未指定"}</span>
          {currentConfig && onUseLocal ? <Tooltip title="不取消已开放的对话模型；跟随默认配置的试卷解析改用本地解析"><Button type="link" size="small" disabled={disabled || localSwitchBusy} onClick={onUseLocal}>清除默认</Button></Tooltip> : null}
        </div>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("enabled")}><Zap size={15} /> 已启用 {activeCount} 个</button>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("connection")}><CheckCircle2 size={15} /> 访问检查通过 {healthyCount} 个</button>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("text")}>普通回复已验证 {textCount} 个</button>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("capability")}>结构化能力已验证 {capabilityCount} 个</button>
        {temporarilyUnavailableCount > 0 ? <span>{temporarilyUnavailableCount} 个暂时无法验证</span> : null}
      </div>
    </section>
  );
}
