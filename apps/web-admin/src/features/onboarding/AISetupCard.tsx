import { Button } from "antd";
import { ArrowRight, Bot, Cloud, UserRound } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

const modes = [
  { key: "local", icon: <Bot size={17} />, title: "本地模型", copy: "答题数据保留在本地环境" },
  { key: "external", icon: <Cloud size={17} />, title: "第三方模型 API", copy: "按数据策略调用已配置的模型" },
  { key: "manual", icon: <UserRound size={17} />, title: "人工阅卷", copy: "不依赖 AI 也可完成阅卷" }
];

export function AISetupCard({ check, onNavigate }: { check: OnboardingCheck; onNavigate: (path: string) => void }) {
  const currentMode = typeof check.metadata?.mode === "string" ? check.metadata.mode : "manual";
  return (
    <OnboardingCheckCard check={check} action={<Button onClick={() => onNavigate(check.action_path || "/platform/model-config")}>查看 AI 设置 <ArrowRight size={15} /></Button>}>
      <div className="onboarding-mode-list">
        {modes.map((mode) => <div key={mode.key} className={mode.key === currentMode ? "selected" : ""}>{mode.icon}<span><strong>{mode.title}</strong><small>{mode.copy}</small></span></div>)}
      </div>
    </OnboardingCheckCard>
  );
}
