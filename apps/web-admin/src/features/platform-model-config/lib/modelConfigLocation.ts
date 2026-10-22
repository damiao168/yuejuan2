import type { Tenant } from "../../../api/org";
import { currentRoutePath } from "../../../router/query";

export type ModelConfigTab = "models" | "bindings" | "governance";

export function selectModelSchool(schools: Tenant[], current: string, requested: string): string {
  if (current && schools.some((school) => school.id === current)) return current;
  // 已失效的学校深链不能悄悄改为另一所学校；由管理员重新选择操作对象。
  if (requested) return schools.find((school) => school.id === requested)?.id ?? "";
  return schools.find((school) => school.status === "active")?.id ?? "";
}

export function modelConfigLocation(location: string, tenantID: string, tab: ModelConfigTab): string {
  // 旧书签仍可读取，但恢复学校和页签时统一写回不含 # 的地址。
  const [path, query = ""] = location.replace(/^#/, "").split("?", 2);
  const params = new URLSearchParams(query);
  if (tenantID) params.set("tenant_id", tenantID);
  else params.delete("tenant_id");
  params.set("tab", tab);
  return `${path || "/admin/platform/model-config"}?${params}`;
}

export function rememberModelConfigLocation(tenantID: string, tab: ModelConfigTab): void {
  const location = modelConfigLocation(currentRoutePath(), tenantID, tab);
  // 范围切换已由页面处理未保存确认；这里只更新恢复地址，不新增一次路由离开。
  window.history.replaceState(window.history.state, "", location);
}
