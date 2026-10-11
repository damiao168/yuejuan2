import type { WorkspaceStage } from "./types";
import { StageNav } from "./StageNav";

export function WorkspaceStageRail({ stages, onNavigate }: Readonly<{ stages: WorkspaceStage[]; onNavigate: (route: string) => void }>) {
  return <StageNav stages={stages} onNavigate={onNavigate} />;
}
