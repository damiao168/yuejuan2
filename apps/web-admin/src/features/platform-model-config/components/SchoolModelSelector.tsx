import { Select } from "antd";
import { Building2, CheckCircle2, Zap } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import type { Tenant } from "../../../api/org";
import { connectionStatusLabel } from "../lib/modelConfig";

export function SchoolModelSelector({
  schools,
  selectedTenantID,
  currentConfig,
  loading,
  disabled,
  activeCount,
  healthyCount,
  temporarilyUnavailableCount,
  capabilityCount = 0,
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
  temporarilyUnavailableCount: number;
  capabilityCount?: number;
  onFilter?: (filter: "enabled" | "connection" | "capability") => void;
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
        <span><Building2 size={15} /> 日常对话模型：{currentConfig ? `${currentConfig.display_name} · ${currentConfig.model_name} · ${connectionStatusLabel(currentConfig)}` : "未指定学校默认模型"}</span>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("enabled")}><Zap size={15} /> 已启用 {activeCount} 个</button>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("connection")}><CheckCircle2 size={15} /> 访问检查通过 {healthyCount} 个</button>
        <button type="button" disabled={disabled} onClick={() => onFilter?.("capability")}>结构化能力已验证 {capabilityCount} 个</button>
        {temporarilyUnavailableCount > 0 ? <span>{temporarilyUnavailableCount} 个暂时无法验证</span> : null}
      </div>
    </section>
  );
}
