import { Alert, Button } from "antd";
import { RefreshCw, Rocket } from "lucide-react";
import type { OnboardingCheck } from "../api/onboarding";
import { AISetupCard } from "../features/onboarding/AISetupCard";
import { DataPolicyCard } from "../features/onboarding/DataPolicyCard";
import { FirstSchoolCard } from "../features/onboarding/FirstSchoolCard";
import { NextActionCard } from "../features/onboarding/NextActionCard";
import { OnboardingCheckCard } from "../features/onboarding/OnboardingCheckCard";
import { OnboardingProgress } from "../features/onboarding/OnboardingProgress";
import { SystemReadinessCard } from "../features/onboarding/SystemReadinessCard";
import { useOnboardingReadiness } from "../features/onboarding/queries";
import { ErrorState, LoadingState } from "../components/PageState";

const fallbackCheck = (key: string, title: string): OnboardingCheck => ({ key, title, state: "unavailable", severity: key === "system_core" || key === "first_school" || key === "platform_admin" ? "blocking" : "recommended", description: "暂时无法确认此项状态，请刷新后重试。" });

export function PlatformGettingStartedPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const readiness = useOnboardingReadiness();
  const refresh = async () => { await readiness.refetch(); };
  if (readiness.isLoading) return <LoadingState label="正在检查首次启用状态" />;
  if (!readiness.data && readiness.error) return <ErrorState message="暂时无法读取首次启用状态；其他平台功能仍可使用。" onRetry={() => void readiness.refetch()} />;
  if (!readiness.data) return null;
  const find = (key: string, title: string) => readiness.data.checks.find((check) => check.key === key) || fallbackCheck(key, title);
  const adminCheck = find("platform_admin", "平台管理员有效");

  return (
    <div className="page-stack platform-onboarding-page">
      <section className="platform-onboarding-heading">
        <div className="platform-onboarding-mark"><Rocket size={22} /></div>
        <div><span>平台管理 · 首次启用</span><h1>把系统交付给第一所学校</h1><p>检查实际运行状态，完成阻断项，再把学校管理员账号安全交付给使用方。</p></div>
        <Button icon={<RefreshCw size={16} />} loading={readiness.isFetching} onClick={() => void readiness.refetch()}>重新检查</Button>
      </section>

      {readiness.isError ? <Alert type="warning" showIcon message="状态刷新失败" description="页面继续显示上次成功读取的结果。" /> : null}
      <OnboardingProgress completed={readiness.data.completed_count} total={readiness.data.total_required} ready={readiness.data.ready_for_use} />

      <div className="onboarding-checklist">
        <SystemReadinessCard check={find("system_core", "系统运行状态")} onNavigate={onNavigate} />
        <OnboardingCheckCard check={adminCheck} />
        <FirstSchoolCard check={find("first_school", "创建第一所学校")} onCreated={refresh} />
        <AISetupCard check={find("ai_mode", "AI 阅卷模式")} onNavigate={onNavigate} />
        <DataPolicyCard check={find("data_policy", "数据处理方式")} onNavigate={onNavigate} />
      </div>

      <NextActionCard readiness={readiness.data} onNavigate={onNavigate} />
    </div>
  );
}
