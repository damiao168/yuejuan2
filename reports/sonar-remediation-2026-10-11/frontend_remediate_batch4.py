from pathlib import Path
import re
p=Path('apps/web-admin/src/utils/displayValue.ts');p.parent.mkdir(exist_ok=True)
p.write_text('''// 后端扩展字段未经具体结构校验，不能把对象当作标签或数值展示成 [object Object]。
export function primitiveDisplayValue(value: unknown, fallback = "-"): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
  return fallback;
}
''',encoding='utf-8')
p=Path('apps/web-admin/src/components/ResponsiveTable.tsx');s=p.read_text(encoding='utf-8');s=s.replace('import { keyboardAccessibleRows','import { primitiveDisplayValue } from "../utils/displayValue";\nimport { keyboardAccessibleRows')
s=s.replace('if (["string", "number", "boolean"].includes(typeof rendered))','if (typeof rendered === "string" || typeof rendered === "number" || typeof rendered === "boolean")')
idx=s.index('function recordKey');s=s[:idx]+'''function columnIdentity<T extends object>(column: ColumnType<T>, index?: number): string {
  const value = column.key ?? column.dataIndex ?? index;
  // AntD 的复合 dataIndex 是字段路径，保留已有逗号拼接；对象不适合作为列标识。
  if (Array.isArray(value)) return value.map((part) => primitiveDisplayValue(part, "")).join(",");
  return primitiveDisplayValue(value, index === undefined ? "undefined" : String(index));
}

'''+s[idx:]
s=s.replace('String(column.key ?? column.dataIndex)','columnIdentity(column)').replace('String(column.key ?? column.dataIndex ?? columnIndex)','columnIdentity(column, columnIndex)');p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/src/features/capture/captureColumns.tsx');s=p.read_text(encoding='utf-8');s='import { primitiveDisplayValue } from "../../utils/displayValue";\n'+s
s=s.replace('String(top.template_name || "候选模板")','primitiveDisplayValue(top.template_name, "候选模板")').replace('String(candidate.template_name || candidate.template_id || "候选模板")','primitiveDisplayValue(candidate.template_name, primitiveDisplayValue(candidate.template_id, "候选模板"))').replace('String(item.page_identity.quality_status ?? "")','primitiveDisplayValue(item.page_identity.quality_status, "")');p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/src/features/gold-papers/GoldPaperManagerDrawer.tsx');s=p.read_text(encoding='utf-8');s='import { primitiveDisplayValue } from "../../utils/displayValue";\n'+s;s=s.replace('String(latest.trait_scores[point.id] ?? "未标注")','primitiveDisplayValue(latest.trait_scores[point.id], "未标注")');p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/src/pages/QualityDashboardPage.tsx');s=p.read_text(encoding='utf-8');s='import { primitiveDisplayValue } from "../utils/displayValue";\n'+s;s=s.replace('incident.metric_snapshot.sample_count ?? "未知"','primitiveDisplayValue(incident.metric_snapshot.sample_count, "未知")');p.write_text(s,encoding='utf-8')
p=Path('apps/desktop-client/src/components/OfflineWorkbench.tsx');s=p.read_text(encoding='utf-8');s=s.replace('id: String(point.id ?? ""), description: String(point.description ?? ""),','id: typeof point.id === "string" ? point.id : "", description: typeof point.description === "string" ? point.description : "",');p.write_text(s,encoding='utf-8')
# 原空字符串仍使用默认标签；先沿用原来的 truthiness 回退，再限制显示值类型。
p=Path('apps/web-admin/src/features/capture/captureColumns.tsx');s=p.read_text(encoding='utf-8');s=s.replace('primitiveDisplayValue(top.template_name, "候选模板")','primitiveDisplayValue(top.template_name || "候选模板", "候选模板")').replace('primitiveDisplayValue(candidate.template_name, primitiveDisplayValue(candidate.template_id, "候选模板"))','primitiveDisplayValue(candidate.template_name || candidate.template_id || "候选模板", "候选模板")');p.write_text(s,encoding='utf-8')
