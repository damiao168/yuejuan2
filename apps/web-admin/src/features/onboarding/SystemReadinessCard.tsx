import { Button } from "antd";
import { ArrowRight } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

export function SystemReadinessCard({ check, onNavigate }: { check: OnboardingCheck; onNavigate: (path: string) => void }) {
  return <OnboardingCheckCard check={check} action={<Button type="text" onClick={() => onNavigate(check.action_path || "/system/status")}>系统运维 <ArrowRight size={15} /></Button>} />;
}
