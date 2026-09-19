import { Alert, Button } from "antd";
import { ArrowRight, RefreshCw } from "lucide-react";
import type { OnboardingReadiness } from "../../api/onboarding";

export function ReadinessBanner({ readiness, loading, unavailable, onNavigate, onRetry }: {
  readiness?: OnboardingReadiness;
  loading?: boolean;
  unavailable?: boolean;
  onNavigate: (path: string) => void;
  onRetry: () => void;
}) {
  if (loading) return null;
  if (!readiness && unavailable) {
    return <Alert type="warning" showIcon message="暂时无法读取启用状态" description="工作台仍可正常使用；稍后可以重新检查。" action={<Button size="small" icon={<RefreshCw size={14} />} onClick={onRetry}>重试</Button>} />;
  }
  if (!readiness) return null;
  const pendingBlocking = readiness.checks.filter((check) => check.severity === "blocking" && check.state !== "ready");
  const pendingRecommended = readiness.checks.filter((check) => check.severity === "recommended" && check.state !== "ready");
  if (!pendingBlocking.length && !pendingRecommended.length) return null;
  const target = pendingBlocking[0] || pendingRecommended[0];
  return (
    <Alert
      type={pendingBlocking.length ? "warning" : "info"}
      showIcon
      message={pendingBlocking.length ? (readiness.scope === "platform" ? "系统尚未完成首次启用" : "学校资料尚未准备完成") : "基础启用已完成"}
      description={pendingBlocking.length ? `还需完成：${pendingBlocking.map((check) => check.title).join("、")}` : `建议完成：${pendingRecommended.map((check) => check.title).join("、")}`}
      action={<Button type="primary" size="small" onClick={() => onNavigate(target.action_path || (readiness.scope === "platform" ? "/platform/getting-started" : "/organization/setup"))}>{pendingBlocking.length ? "继续设置" : "查看建议"} <ArrowRight size={14} /></Button>}
    />
  );
}
