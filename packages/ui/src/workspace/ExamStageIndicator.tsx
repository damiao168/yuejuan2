import type { WorkspaceStage } from "./types";
import { StatusBadge, type StatusBadgeTone } from "./StatusBadge";

function toneFor(state: WorkspaceStage["state"]): StatusBadgeTone {
  if (state === "completed") return "success";
  if (state === "current") return "processing";
  return "neutral";
}

/** 供页头和列表展示的小型状态信息，不作为第二套导航。 */
export function ExamStageIndicator({ stage }: Readonly<{ stage?: WorkspaceStage }>) {
  if (!stage) return <StatusBadge>阶段待确认</StatusBadge>;
  return <StatusBadge tone={toneFor(stage.state)}>{stage.label}</StatusBadge>;
}
