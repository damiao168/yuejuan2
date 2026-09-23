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
  onChange
}: {
  schools: Tenant[];
  selectedTenantID: string;
  currentConfig?: ManagedModelAPIConfig;
  loading: boolean;
  disabled: boolean;
  activeCount: number;
  healthyCount: number;
  temporarilyUnavailableCount: number;
  onChange: (tenantID: string) => void;
}) {
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
        <span><Building2 size={15} /> 日常对话模型：{currentConfig ? `${currentConfig.display_name} · ${currentConfig.model_name} · ${connectionStatusLabel(currentConfig)}` : "本地模型"}</span>
        <span><Zap size={15} /> {activeCount} 个可用配置</span>
        <span><CheckCircle2 size={15} /> {healthyCount} 个连接正常</span>
        {temporarilyUnavailableCount > 0 ? <span>{temporarilyUnavailableCount} 个暂时无法验证</span> : null}
      </div>
    </section>
  );
}
