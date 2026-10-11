import { Button } from "antd";
import { ArrowRight } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

function enabled(value: unknown) {
  // 元数据只接受明确布尔值；字符串或缺项不能直接显示为已启用。
  if (value === true) return "已允许";
  if (value === false) return "关闭";
  return "尚未核实";
}

function fallbackModeLabel(value: unknown) {
  if (value === "manual_only") return "人工处理";
  if (value === "approved_deployment_only") return "已批准模型";
  return "尚未核实";
}

export function DataPolicyCard({ check, onNavigate }: Readonly<{ check: OnboardingCheck; onNavigate: (path: string) => void }>) {
  return (
    <OnboardingCheckCard check={check} action={<Button onClick={() => onNavigate("/platform/model-config?tab=governance&view=policy&choose_school=1")}>选择学校检查使用策略 <ArrowRight size={15} /></Button>}>
      <p>当前检查：平台默认数据政策。它不代表任何学校已完成数据外发授权。</p>
      <dl className="onboarding-policy-summary">
        <div><dt>外部 AI</dt><dd>{enabled(check.metadata?.external_enabled)}</dd></div>
        <div><dt>答题文本外发</dt><dd>{enabled(check.metadata?.text_export_enabled)}</dd></div>
        <div><dt>答题图片外发</dt><dd>{enabled(check.metadata?.image_export_enabled)}</dd></div>
        <div><dt>失败回退</dt><dd>{fallbackModeLabel(check.metadata?.fallback_mode)}</dd></div>
      </dl>
    </OnboardingCheckCard>
  );
}
