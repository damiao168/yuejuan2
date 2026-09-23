import { Button } from "antd";
import { ArrowRight } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

function enabled(value: unknown) { return value === true ? "已允许" : "关闭"; }

export function DataPolicyCard({ check, onNavigate }: { check: OnboardingCheck; onNavigate: (path: string) => void }) {
  return (
    <OnboardingCheckCard check={check} action={<Button onClick={() => onNavigate(check.action_path || "/system/models")}>模型质量治理 <ArrowRight size={15} /></Button>}>
      <dl className="onboarding-policy-summary">
        <div><dt>外部 AI</dt><dd>{enabled(check.metadata?.external_enabled)}</dd></div>
        <div><dt>答题文本外发</dt><dd>{enabled(check.metadata?.text_export_enabled)}</dd></div>
        <div><dt>答题图片外发</dt><dd>{enabled(check.metadata?.image_export_enabled)}</dd></div>
        <div><dt>失败回退</dt><dd>{check.metadata?.fallback_mode === "manual_only" ? "人工处理" : "已批准模型"}</dd></div>
      </dl>
    </OnboardingCheckCard>
  );
}
