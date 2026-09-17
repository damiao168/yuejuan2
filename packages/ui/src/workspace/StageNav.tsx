import type { CSSProperties } from "react";
import type { WorkspaceStage } from "./types";

/** Compact navigation for the configured examination lifecycle stages. */
export function StageNav({ stages, onNavigate }: { stages: WorkspaceStage[]; onNavigate: (route: string) => void }) {
  return (
    <nav className="eg-workspace-stages" aria-label="考试流程" style={{ "--eg-stage-count": Math.max(stages.length, 1) } as CSSProperties}>
      {stages.map((stage, index) => (
        <button className={`eg-workspace-stage is-${stage.state}`} type="button" key={stage.key} onClick={() => onNavigate(stage.action_route)} aria-current={stage.state === "current" ? "step" : undefined}>
          <span>{stage.state === "completed" ? "✓" : index + 1}</span>
          <div><strong>{stage.label}</strong>{stage.summary ? <small title={stage.summary}>{stage.summary}</small> : null}</div>
        </button>
      ))}
    </nav>
  );
}
