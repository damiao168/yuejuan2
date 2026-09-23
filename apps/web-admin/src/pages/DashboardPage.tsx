import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Space } from "antd";
import { motion } from "framer-motion";
import { BrainCircuit, Building2, Plus, RefreshCw, ScrollText, ServerCog } from "lucide-react";
import { getSafeUserText, getUserErrorMessage } from "../api/client";
import { getDashboardSummary, type DashboardSummary } from "../api/dashboard";
import { getSystemStatus, type SystemStatus } from "../api/system";
import { hasEveryPermission, type SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { SchoolDashboard, type DashboardWorkItem } from "../features/dashboard/SchoolDashboard";
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
}: {
  user: SessionUser;
  onNavigate: (path: string) => void;
  readiness: ReturnType<typeof useOnboardingReadiness>;
}) {
  const [status, setStatus] = useState<SystemStatus>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      setStatus(await getSystemStatus());
    } catch (loadError) {
      setError(getUserErrorMessage(loadError, "平台状态加载失败"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const quickActions = [
    ...(hasEveryPermission(user, ["tenant:manage"]) ? [{ label: "学校管理", path: "/platform/schools", icon: <Building2 size={17} /> }] : []),
    ...(hasEveryPermission(user, ["system:read"]) ? [{ label: "系统状态", path: "/system/status", icon: <ServerCog size={17} /> }] : []),
    ...(hasEveryPermission(user, ["model:read"]) ? [{ label: "模型治理", path: "/system/models", icon: <BrainCircuit size={17} /> }] : []),
    ...(hasEveryPermission(user, ["audit:read"]) ? [{ label: "操作审计", path: "/audit", icon: <ScrollText size={17} /> }] : [])
  ];

  if (!status && loading) return <LoadingState label="正在加载平台状态" />;
  if (!status && error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="page-stack role-dashboard">
      <section className="page-heading dashboard-heading platform-dashboard-heading">
        <div><h1>平台状态</h1><p>管理学校并确认平台服务是否可用</p></div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>
      {error ? <Alert type="error" showIcon message="刷新失败" description={error} /> : null}
      <ReadinessBanner readiness={readiness.data} loading={readiness.isLoading} unavailable={readiness.isError} onNavigate={onNavigate} onRetry={() => void readiness.refetch()} />
      {status ? (
        <section className="operations-strip">
          <strong>服务状态</strong>
          {status.dependencies.map((dependency) => (
            <span key={dependency.name} title={dependency.detail || dependency.name}>
              {dependencyNames[dependency.name] ?? dependency.name}
              <StatusTag tone={dependencyTone(dependency.status)}>{dependencyText(dependency.status)}</StatusTag>
            </span>
          ))}
        </section>
      ) : <EmptyState title="服务状态暂时不可用" description="请刷新页面；若仍失败，请进入系统运维查看原因。" />}
      <section className="quick-actions">
        <Space wrap>{quickActions.map((action) => <Button key={action.label} icon={action.icon} onClick={() => onNavigate(action.path)}>{action.label}</Button>)}</Space>
      </section>
    </div>
  );
}

export function DashboardPage({ user, onNavigate }: { user: SessionUser; onNavigate: (path: string) => void }) {
  const isPlatform = user.roles.includes("platform_admin");
  const canReadOnboarding = user.roles.some((role) => ["platform_admin", "tenant_admin", "school_admin"].includes(role));
  const readiness = useOnboardingReadiness(canReadOnboarding);
  const canCreateExam = hasEveryPermission(user, ["exam:manage"]);
  const [data, setData] = useState<DashboardSummary>();
  const [loading, setLoading] = useState(!isPlatform);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    if (isPlatform) return;
    setLoading(true);
    setError(undefined);
    try {
      setData(await getDashboardSummary());
    } catch (loadError) {
      setError(getUserErrorMessage(loadError, "工作台加载失败"));
    } finally {
      setLoading(false);
    }
  }, [isPlatform]);

  useEffect(() => { void load(); }, [load]);

  const stats = data?.statistics;
  const todo = useMemo<DashboardWorkItem[]>(() => {
    if (!stats || !data) return [];
    const blockerByCode = new Map(data.blocking_issues.map((issue) => [issue.code, issue]));
    const withBlocker = (code: string, fallback: { detail: string; action: string; path: string }) => {
      const issue = blockerByCode.get(code);
      return issue ? { detail: issue.impact, action: issue.action, path: issue.drilldown_path } : fallback;
    };
    return [
      ...(stats.failed_submission_count ? [{ label: "答卷处理失败", value: stats.failed_submission_count, unit: "份", tone: "danger" as StatusTone, ...withBlocker("failed_submissions", { detail: "会阻断后续阅卷", action: "查看异常", path: "/capture?issue=failed" }) }] : []),
      ...(stats.unmatched_submission_count ? [{ label: "学生身份待确认", value: stats.unmatched_submission_count, unit: "份", tone: "warning" as StatusTone, ...withBlocker("unmatched_submissions", { detail: "答卷尚未匹配学生", action: "立即确认", path: "/capture?issue=unmatched" }) }] : []),
      ...(stats.pending_arbitration_submission_count ? [{ label: "待人工复核", value: stats.pending_arbitration_count, unit: "项", tone: "danger" as StatusTone, ...withBlocker("pending_arbitration", { detail: `来自 ${stats.pending_arbitration_submission_count} 份答卷`, action: "开始复核", path: "/arbitration?status=pending" }) }] : []),
      ...(stats.pending_review_question_count ? [{ label: "主观题等待确认", detail: `来自 ${stats.pending_review_submission_count} 份答题卡`, value: stats.pending_review_question_count, unit: "题", action: "继续阅卷", path: "/grading?status=pending", tone: "processing" as StatusTone }] : []),
      ...(stats.finalized_exam_count ? [{ label: "成绩等待发布", detail: "阅卷已完成，检查后即可发布", value: stats.finalized_exam_count, unit: "场", action: "去发布", path: "/scores?status=finalized", tone: "warning" as StatusTone }] : [])
    ];
  }, [data, stats]);

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
        organizationStatistics={data.organization_statistics}
        statistics={stats}
        activeExamCount={stats.active_exam_count}
        exams={data.active_exams}
        workItems={todo}
        activities={data.recent_activities}
        onNavigate={onNavigate}
      />
    </div>
  );
}
