import type { ReactNode } from "react";
import { AlertTriangle, Check, Circle, CircleHelp } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";

function statePresentation(check: OnboardingCheck) {
  if (check.state === "ready") return { icon: <Check size={17} />, label: "已完成", tone: "ready" };
  if (check.state === "warning") return { icon: <AlertTriangle size={17} />, label: "请确认", tone: "warning" };
  if (check.state === "unavailable") return { icon: <CircleHelp size={17} />, label: "暂不可用", tone: "unavailable" };
  return { icon: <Circle size={17} />, label: check.severity === "blocking" ? "待完成" : "建议", tone: "pending" };
}

export function OnboardingCheckCard({ check, action, children }: { check: OnboardingCheck; action?: ReactNode; children?: ReactNode }) {
  const presentation = statePresentation(check);
  return (
    <section className={`onboarding-check ${presentation.tone}`} data-check-key={check.key}>
      <div className="onboarding-check-icon" aria-hidden>{presentation.icon}</div>
      <div className="onboarding-check-copy">
        <div className="onboarding-check-title">
          <h2>{check.title}</h2>
          <span>{presentation.label}</span>
        </div>
        {check.description ? <p>{check.description}</p> : null}
        {children}
      </div>
      {action ? <div className="onboarding-check-action">{action}</div> : null}
    </section>
  );
}
