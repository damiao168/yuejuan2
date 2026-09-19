import { Progress } from "antd";

export function OnboardingProgress({ completed, total, ready }: { completed: number; total: number; ready: boolean }) {
  const percent = total > 0 ? Math.round((completed / total) * 100) : 100;
  return (
    <div className="onboarding-progress" aria-label={`基础启用进度 ${completed}/${total}`}>
      <div><span>基础启用</span><strong>{completed} / {total}</strong></div>
      <Progress percent={percent} showInfo={false} strokeColor={ready ? "#16856a" : "#2563eb"} trailColor="#e7ebf0" />
      <small>{ready ? "阻断项已完成，系统可以交付使用" : "完成阻断项后即可交付使用"}</small>
    </div>
  );
}
