import { Button } from "antd";
import { ArrowRight } from "lucide-react";
import type { OnboardingCheck, OnboardingReadiness } from "../../api/onboarding";

export function NextActionCard({ readiness, onNavigate }: { readiness: OnboardingReadiness; onNavigate: (path: string) => void }) {
  const next = readiness.next_action;
  const check: OnboardingCheck | undefined = next ? readiness.checks.find((item) => item.action_code === next.code) : undefined;
  return (
    <section className="onboarding-next-action">
      <div><span>{readiness.ready_for_use ? "建议下一步" : "下一步"}</span><strong>{check?.title || (readiness.ready_for_use ? "进入工作台" : "继续完成首次启用")}</strong><p>{check?.description || "基础设置已经完成，可以开始正常使用。"}</p></div>
      <Button type="primary" onClick={() => onNavigate(next?.path || "/dashboard")}>{readiness.ready_for_use && !next ? "进入工作台" : "继续"} <ArrowRight size={16} /></Button>
    </section>
  );
}
