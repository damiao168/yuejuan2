import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, Modal } from "antd";
import { Building2 } from "lucide-react";
import { listPlatformSchools, type PlatformSchoolListFilter, type PlatformSchoolSummary } from "../api/platformSchools";
import { updateTenantStatus } from "../api/org";
import { CreateSchoolForm } from "../features/platform/schools/CreateSchoolForm";
import { SchoolManagementToolbar } from "../features/platform/schools/SchoolManagementToolbar";
import { SchoolSummaryTable, type SchoolDetailTab, type SchoolDetailTarget } from "../features/platform/schools/SchoolSummaryTable";
import { SchoolDetailDrawer } from "../features/platform/schools/SchoolDetailDrawer";
import { isStepUpCancelledError, useStepUp } from "../auth/stepUpContext";

export function PlatformSchoolsPage() {
  const { message } = AntApp.useApp();
  const { runWithStepUp } = useStepUp();
  const [filter, setFilter] = useState<PlatformSchoolListFilter>({ usage_window: "30d", sort: "created_at", order: "desc", limit: 50 });
  const [schools, setSchools] = useState<PlatformSchoolSummary[]>([]);
  const [summary, setSummary] = useState({ total: 0, active: 0, disabled: 0 });
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(false);
  const [nextCursor, setNextCursor] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<SchoolDetailTab>("overview");
  const [overviewFocus, setOverviewFocus] = useState<"created" | "status" | null>(null);
  const [membersFocus, setMembersFocus] = useState<"administrators" | null>(null);
  const listRequestRef = useRef(0);

  const query = useMemo(() => ({ ...filter, cursor: undefined }), [filter]);
  const loadSchools = useCallback(async () => {
    const requestID = ++listRequestRef.current;
    setLoading(true);
    try {
      const response = await listPlatformSchools(query);
      if (requestID !== listRequestRef.current) return;
      setSchools(response.schools);
      setSummary(response.summary);
      setHasMore(response.has_more);
      setNextCursor(response.next_cursor);
    } catch {
      if (requestID === listRequestRef.current) message.error("学校列表加载失败");
    } finally {
      if (requestID === listRequestRef.current) setLoading(false);
    }
  }, [message, query]);

  useEffect(() => { void loadSchools(); }, [loadSchools]);
  const refresh = useCallback(() => { void loadSchools(); }, [loadSchools]);
  const loadMore = useCallback(async () => {
    if (!hasMore || !nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const response = await listPlatformSchools({ ...query, cursor: nextCursor });
      setSchools((current) => [...current, ...response.schools]);
      setHasMore(response.has_more);
      setNextCursor(response.next_cursor);
    } catch { message.error("更多学校加载失败"); }
    finally { setLoadingMore(false); }
  }, [hasMore, nextCursor, loadingMore, query, message]);

  const openSchool = useCallback((school: PlatformSchoolSummary, target: SchoolDetailTarget) => {
    setSelectedId(school.tenant_id);
    setActiveTab(target === "created" || target === "status" ? "overview" : target === "administrator" ? "members" : target);
    setOverviewFocus(target === "created" || target === "status" ? target : null);
    setMembersFocus(target === "administrator" ? "administrators" : null);
  }, []);

  const changeStatus = useCallback(async (school: PlatformSchoolSummary) => {
    const status = school.status === "active" ? "disabled" : "active";
    try {
      // 学校启停影响整个租户，实际变更必须在再次验证管理员身份后执行。
      await runWithStepUp({
        reason: `${status === "active" ? "启用" : "停用"}学校“${school.name}”`,
        description: "此操作会改变整个学校机构的可用状态，需要验证平台管理员身份。",
        action: () => updateTenantStatus(school.tenant_id, status)
      });
      message.success(status === "active" ? "学校已启用" : "学校已停用");
      refresh();
    } catch (error) { if (!isStepUpCancelledError(error)) message.error("状态更新失败"); }
  }, [message, refresh, runWithStepUp]);

  const selected = schools.find((school) => school.tenant_id === selectedId) ?? null;

  return <div className="page-stack platform-school-page">
    <section className="page-heading platform-school-page-heading"><div><h1>学校管理</h1><p>统一管理学校账号、成员规模、AI 用量和运行状态</p></div></section>
    <SchoolManagementToolbar filter={filter} onChange={setFilter} onRefresh={refresh} onCreate={() => setCreateOpen(true)} loading={loading} summary={summary} />
    <SchoolSummaryTable schools={schools} loading={loading} usageWindow={filter.usage_window ?? "30d"} onOpen={openSchool} onChangeStatus={(school) => void changeStatus(school)} />
    {hasMore ? <div className="load-more-row"><Button loading={loadingMore} onClick={() => void loadMore()}>加载更多学校</Button></div> : null}
    <SchoolDetailDrawer school={selected} activeTab={activeTab} overviewFocus={overviewFocus} membersFocus={membersFocus} onTabChange={(tab) => { setActiveTab(tab); setOverviewFocus(null); setMembersFocus(null); }} onClose={() => setSelectedId(null)} />
    <Modal title={<span className="platform-school-create-title"><Building2 size={18} /> 新建学校</span>} open={createOpen} footer={null} onCancel={() => setCreateOpen(false)} destroyOnHidden>
      <CreateSchoolForm onCancel={() => setCreateOpen(false)} onCreated={() => { setCreateOpen(false); refresh(); }} />
    </Modal>
  </div>;
}
