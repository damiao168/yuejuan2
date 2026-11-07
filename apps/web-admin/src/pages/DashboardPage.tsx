import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";
import { motion } from "framer-motion";
import { BrainCircuit, Building2, Plus, RefreshCw, ScrollText, ServerCog } from "lucide-react";
import { getSafeUserText, getUserErrorMessage } from "../api/client";
import { getDashboardSummary, type DashboardSummary } from "../api/dashboard";
import { listPlatformSchools } from "../api/platformSchools";
import { getSystemStatus, type SystemStatus } from "../api/system";
import { hasAnyPermission, hasEveryPermission, sessionAuthorizationKey, type SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { SchoolDashboard } from "../features/dashboard/SchoolDashboard";
import { ReadinessBanner } from "../features/onboarding/ReadinessBanner";
import { useOnboardingReadiness } from "../features/onboarding/queries";
import type { StatusTone } from "../types";

const dependencyNames: Record<string, string> = {
  postgres: "数据库",
  redis: "缓存队列",
  minio: "文件存储",
  qdrant: "检索服务",
  ai_service: "智能评分服务",
  ocr_worker: "文字识别服务"
};

function dependencyTone(status: string): StatusTone {
  if (status === "ok") return "success";
  if (status === "mock") return "info";
  if (status === "disabled" || status === "not_configured") return "neutral";
  return "danger";
}

function dependencyText(status: string) {
  if (status === "ok") return "正常";
  if (status === "disabled") return "已关闭";
  if (status === "mock") return "演示模式";
  if (status === "not_configured") return "未启用";
  return "异常";
}

function PlatformDashboard({
  user,
  onNavigate,
  readiness
}: Readonly<{
  user: SessionUser;
  onNavigate: (path: string) => void;
  readiness: ReturnType<typeof useOnboardingReadiness>;
}>) {
  const [status, setStatus] = useState<SystemStatus>();
  const [schoolCounts, setSchoolCounts] = useState<{ total?: number; active?: number; disabled?: number; warning?: number; unconfigured?: number }>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    const canReadSchools = hasEveryPermission(user, ["tenant:manage"]);
    const results = await Promise.allSettled([
      getSystemStatus(),
      ...(canReadSchools ? [
        listPlatformSchools({ limit: 1 }),
        listPlatformSchools({ model_health: "warning", limit: 1 }),
        listPlatformSchools({ model_health: "unconfigured", limit: 1 })
      ] : [])
    ]);
    const [systemResult, allSchoolsResult, warningResult, unconfiguredResult] = results;
    setStatus(systemResult.status === "fulfilled" ? systemResult.value as SystemStatus : undefined);
    // 学校接口在服务端先统计筛选结果，再切出当前页；这里只读取 summary，不拿第一页估算全量。
    if (canReadSchools) setSchoolCounts({
      ...(allSchoolsResult?.status === "fulfilled" ? {
        total: allSchoolsResult.value.summary.total,
        active: allSchoolsResult.value.summary.active,
        disabled: allSchoolsResult.value.summary.disabled
      } : {}),
      ...(warningResult?.status === "fulfilled" ? { warning: warningResult.value.summary.total } : {}),
      ...(unconfiguredResult?.status === "fulfilled" ? { unconfigured: unconfiguredResult.value.summary.total } : {})
    });
    if (results.some((result) => result.status === "rejected")) {
      const failure = results.find((result) => result.status === "rejected");
      setError(getUserErrorMessage(failure?.status === "rejected" ? failure.reason : undefined, "部分平台统计加载失败"));
    }
    setLoading(false);
  }, [user]);

  useEffect(() => { void load(); }, [load]);

  const quickActions = [
    ...(hasEveryPermission(user, ["tenant:manage"]) ? [{ label: "学校管理", path: "/platform/schools", icon: <Building2 size={17} /> }] : []),
    ...(hasEveryPermission(user, ["system:read"]) ? [{ label: "系统状态", path: "/system/status", icon: <ServerCog size={17} /> }] : []),
    ...(hasAnyPermission(user, ["model:config:manage", "model:provider:manage"]) ? [{ label: "模型管理", path: "/platform/model-config", icon: <BrainCircuit size={17} /> }] : []),
    ...(hasEveryPermission(user, ["audit:read"]) ? [{ label: "操作审计", path: "/audit", icon: <ScrollText size={17} /> }] : [])
  ];

  if (!status && loading) return <LoadingState label="正在加载平台状态" />;
  if (!status && error && schoolCounts.total === undefined) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="page-stack role-dashboard">
      <section className="page-heading dashboard-heading platform-dashboard-heading">
        <div><h1>平台工作台</h1><p>先处理需要关注的学校，再查看平台服务状态</p></div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>
      {error ? <Alert type="warning" showIcon message="部分统计暂不可用" description={`${error}；已显示成功返回的数据。`} /> : null}
      <ReadinessBanner readiness={readiness.data} loading={readiness.isLoading} unavailable={readiness.isError} onNavigate={onNavigate} onRetry={() => void readiness.refetch()} />
      {hasEveryPermission(user, ["tenant:manage"]) ? <section className="platform-fleet-section" aria-labelledby="platform-fleet-title">
        <div className="platform-fleet-heading"><div><h2 id="platform-fleet-title">学校概览</h2><p>数据来自全量筛选结果；数字可打开对应学校列表。</p></div><Button type="link" onClick={() => onNavigate("/platform/schools")}>查看学校</Button></div>
        <div className="platform-fleet-grid">
          {[
            { label: "学校总数", value: schoolCounts.total, path: "/platform/schools", tone: "normal" },
            { label: "使用中", value: schoolCounts.active, path: "/platform/schools?status=active", tone: "normal" },
            { label: "已停用", value: schoolCounts.disabled, path: "/platform/schools?status=disabled", tone: "normal" },
            { label: "对话默认模型异常", value: schoolCounts.warning, path: "/platform/schools?model_health=warning", tone: "warning" },
            { label: "对话默认模型未配置", value: schoolCounts.unconfigured, path: "/platform/schools?model_health=unconfigured", tone: "warning" }
          ].map((item) => <button key={item.label} className={`platform-fleet-item ${item.tone}`} type="button" onClick={() => onNavigate(item.path)}>
            <span>{item.label}</span><strong>{item.value === undefined ? "—" : item.value.toLocaleString("zh-CN")}</strong><small>查看学校 →</small>
          </button>)}
        </div>
      </section> : null}
      {status ? (
        <section className="operations-strip platform-service-strip">
          <strong>服务状态</strong>
          {status.dependencies.map((dependency) => (
            <span key={dependency.name} title={dependency.detail || dependency.name}>
              {dependencyNames[dependency.name] ?? dependency.name}
              <StatusTag tone={dependencyTone(dependency.status)}>{dependencyText(dependency.status)}</StatusTag>
            </span>
          ))}
        </section>
      ) : <EmptyState title="服务状态暂时不可用" description="请刷新页面；若仍失败，请进入系统运维查看原因。" />}
      <section className="quick-actions platform-shortcuts" aria-label="平台常用入口">
        <h2>常用入口</h2><Space wrap>{quickActions.map((action) => <Button key={action.label} icon={action.icon} onClick={() => onNavigate(action.path)}>{action.label}</Button>)}</Space>
      </section>
    </div>
  );
}

export function DashboardPage({ user, onNavigate }: Readonly<{ user: SessionUser; onNavigate: (path: string) => void }>) {
  const isPlatform = user.roles.includes("platform_admin");
  const canViewReviewTeachers = user.roles.includes("school_admin") && !user.roles.some(role => ["platform_admin", "tenant_admin"].includes(role));
  const canReadOnboarding = user.roles.some((role) => ["platform_admin", "tenant_admin", "school_admin"].includes(role));
  const readiness = useOnboardingReadiness(canReadOnboarding);
  const canCreateExam = hasEveryPermission(user, ["exam:manage"]);
  const canManageOrganization = hasEveryPermission(user, ["org:manage"]);
  const identity = sessionAuthorizationKey(user);
  const identityRef = useRef(identity);
  identityRef.current = identity;
  const requestVersion = useRef(0);
  const pendingRequest = useRef<{ identity: string; version: number } | undefined>(undefined);
  const [snapshot, setSnapshot] = useState<{ identity: string; data: DashboardSummary }>();
  const data = snapshot?.identity === identity ? snapshot.data : undefined;
  const [loading, setLoading] = useState(!isPlatform);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    if (isPlatform || pendingRequest.current?.identity === identity) return;
    const version = ++requestVersion.current;
    pendingRequest.current = { identity, version };
    setLoading(true);
    setError(undefined);
    try {
      const result = await getDashboardSummary();
      if (version === requestVersion.current && identityRef.current === identity) setSnapshot({ identity, data: result });
    } catch (loadError) {
      if (version === requestVersion.current && identityRef.current === identity) setError(getUserErrorMessage(loadError, "工作台加载失败"));
    } finally {
      if (pendingRequest.current?.version === version) pendingRequest.current = undefined;
      if (version === requestVersion.current && identityRef.current === identity) setLoading(false);
    }
  }, [identity, isPlatform]);

  useEffect(() => {
    void load();
    if (isPlatform) return;
    const refreshVisible = () => { if (document.visibilityState === "visible") void load(); };
    const timer = canViewReviewTeachers ? window.setInterval(refreshVisible, 30_000) : undefined;
    if (canViewReviewTeachers) document.addEventListener("visibilitychange", refreshVisible);
    // 页面卸载或身份改变后作废旧响应；隐藏页面不定时请求，回到页面时补读最新在线状态。
    return () => { requestVersion.current += 1; pendingRequest.current = undefined; window.clearInterval(timer); document.removeEventListener("visibilitychange", refreshVisible); };
  }, [canViewReviewTeachers, isPlatform, load]);

  const stats = data?.statistics;

  if (isPlatform) return <PlatformDashboard user={user} onNavigate={onNavigate} readiness={readiness} />;
  if (!data && loading) return <LoadingState label="正在加载工作台" />;
  if (!data && error) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!data || !stats) return <EmptyState title="工作台暂时不可用" description="请刷新页面；考试数据不会受影响。" />;

  return (
    <div className="page-stack role-dashboard school-dashboard">
      <motion.section className="dashboard-heading" initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}>
        <div><h1>考试工作台</h1><p>{user.school}</p></div>
        <div className="dashboard-update">
          <span>最后更新 {new Date(data.updated_at).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false })}</span>
          <Button type="text" size="small" aria-label="刷新工作台" icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()} />
          {canCreateExam ? <Button type="primary" icon={<Plus size={16} />} onClick={() => onNavigate("/exams/new")}>新建考试</Button> : null}
        </div>
      </motion.section>

      {error ? <Alert type="error" showIcon message="刷新失败" description={`${error}；页面继续显示上次成功数据。`} /> : null}
      <ReadinessBanner readiness={readiness.data} loading={readiness.isLoading} unavailable={readiness.isError} onNavigate={onNavigate} onRetry={() => void readiness.refetch()} />
      {data.warnings.map((warning) => <Alert key={warning} type="warning" showIcon message={getSafeUserText(warning, "部分统计暂时不可用")} description="其他统计仍可使用，请稍后刷新。" />)}

      <SchoolDashboard
        statistics={stats}
        organizationStatistics={data.organization_statistics}
        exams={data.active_exams}
        gradingProgress={data.grading_progress}
        appealStatistics={data.appeal_statistics}
        warnings={data.warnings}
        activities={data.recent_activities}
        canManageOrganization={canManageOrganization}
        reviewTeachers={canViewReviewTeachers ? data.review_teachers : undefined}
        reviewTeacherSummary={canViewReviewTeachers ? data.review_teacher_summary : undefined}
        onNavigate={onNavigate}
      />
    </div>
  );
}
