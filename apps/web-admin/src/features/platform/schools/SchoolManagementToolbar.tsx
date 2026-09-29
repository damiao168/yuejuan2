import { Button, Input, Select } from "antd";
import { Plus, RefreshCw, Search } from "lucide-react";
import type { PlatformSchoolListFilter, UsageWindow } from "../../../api/platformSchools";

interface Props {
  filter: PlatformSchoolListFilter;
  onChange: (next: PlatformSchoolListFilter) => void;
  onRefresh: () => void;
  onCreate: () => void;
  loading: boolean;
  summary: { total: number; active: number; disabled: number };
}

// 筛选条件改变后旧游标不再对应当前结果集，必须从第一页重新查询。
const update = (filter: PlatformSchoolListFilter, key: keyof PlatformSchoolListFilter, value: string | undefined) => ({ ...filter, [key]: value, cursor: undefined });

export function SchoolManagementToolbar({ filter, onChange, onRefresh, onCreate, loading, summary }: Props) {
  return <div className="platform-school-toolbar">
    <div className="platform-school-toolbar-filters">
      <Input
        className="platform-school-search"
        prefix={<Search size={15} />}
        allowClear
        value={filter.q ?? ""}
        placeholder="搜索学校名称、代码或管理员"
        onChange={(event) => onChange(update(filter, "q", event.target.value))}
      />
      <Select aria-label="学校状态" value={filter.status ?? "all"} onChange={(value) => onChange(update(filter, "status", value === "all" ? undefined : value))} options={[
        { label: "全部状态", value: "all" }, { label: "使用中", value: "active" }, { label: "已停用", value: "disabled" }
      ]} />
      <Select aria-label="活跃度" value={filter.activity ?? "all"} onChange={(value) => onChange(update(filter, "activity", value === "all" ? undefined : value))} options={[
        { label: "全部活跃度", value: "all" }, { label: "今天活跃", value: "today" }, { label: "7 天内活跃", value: "7d" },
        { label: "30 天内活跃", value: "30d" }, { label: "30 天未活跃", value: "inactive_30d" }, { label: "从未活跃", value: "never" }
      ]} />
      <Select aria-label="模型状态" value={filter.model_health ?? "all"} onChange={(value) => onChange(update(filter, "model_health", value === "all" ? undefined : value))} options={[
        { label: "全部模型", value: "all" }, { label: "正常", value: "healthy" }, { label: "异常", value: "warning" }, { label: "未配置", value: "unconfigured" }
      ]} />
      <Select<UsageWindow> aria-label="AI 用量时间范围" value={filter.usage_window ?? "30d"} onChange={(value) => onChange({ ...filter, usage_window: value, cursor: undefined })} options={[
        { label: "AI 用量 · 今天", value: "today" }, { label: "AI 用量 · 7 天", value: "7d" }, { label: "AI 用量 · 30 天", value: "30d" }, { label: "AI 用量 · 90 天", value: "90d" }
      ]} />
    </div>
    <div className="platform-school-toolbar-bottom">
      <span className="platform-school-summary-line">共 {summary.total} 所学校 · {summary.active} 使用中 · {summary.disabled} 已停用</span>
      <div className="platform-school-toolbar-actions">
        <Select aria-label="排序" value={`${filter.sort ?? "created_at"}:${filter.order ?? "desc"}`} onChange={(value) => {
          const [sort, order] = value.split(":");
          onChange({ ...filter, sort: sort as PlatformSchoolListFilter["sort"], order: order as PlatformSchoolListFilter["order"], cursor: undefined });
        }} options={[
          { label: "创建时间 · 最新", value: "created_at:desc" }, { label: "创建时间 · 最早", value: "created_at:asc" },
          { label: "最近活跃 · 最新", value: "last_activity:desc" }, { label: "AI Token · 最多", value: "token_usage:desc" },
          { label: "学生数量 · 最多", value: "student_count:desc" }
        ]} />
        <Button icon={<RefreshCw size={15} />} loading={loading} onClick={onRefresh}>刷新</Button>
        <Button type="primary" icon={<Plus size={15} />} onClick={onCreate}>新建学校</Button>
      </div>
    </div>
  </div>;
}
