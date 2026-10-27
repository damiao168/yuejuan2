import { useRef, useState } from "react";
import type { DraftConflictResolution } from "../draftConflict";
import { createInitialDraft } from "../gradingWorkbench.model";
import type { DraftSaveStatus, ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import { TaskDraftSaveQueue } from "./taskDraftSaveQueue";

// 状态和 refs 属于同一工作台实例；队列与动作 hooks 继续共用这些对象，不建立第二套保存或冲突状态。
export function useWorkbenchTaskState(selectedTaskId: string, currentUserId: string) {
  const [ctx, setCtx] = useState<WorkbenchContext | null>(null);
  const [contextLoading, setContextLoading] = useState(false);
  const [contextError, setContextError] = useState<string | null>(null);
  const [draft, setDraft] = useState<ScoreDraft>(() => createInitialDraft(null));
  const activeTask = useRef(selectedTaskId);
  activeTask.current = selectedTaskId;
  const activeUserId = useRef(currentUserId);
  activeUserId.current = currentUserId;
  const liveDraft = useRef(draft);
  liveDraft.current = draft;
  const [online, setOnline] = useState(() => typeof navigator === "undefined" || navigator.onLine);
  const contextRequestRef = useRef(0);
  const [draftRevision, setDraftRevision] = useState(0);
  const [draftSaveStatus, setDraftSaveStatus] = useState<DraftSaveStatus>("idle");
  const [draftHydrated, setDraftHydrated] = useState(false);
  const lastSavedDraft = useRef("");
  const draftSaves = useRef(new TaskDraftSaveQueue());
  const conflictedDrafts = useRef(new Set<string>());
  const [draftConflict, setDraftConflict] = useState<DraftConflictResolution | null>(null);
  const [resolvingConflict, setResolvingConflict] = useState(false);
  const conflictServerContext = useRef<WorkbenchContext | null>(null);

  return {
    ctx, setCtx, contextLoading, setContextLoading, contextError, setContextError,
    draft, setDraft, activeTask, activeUserId, liveDraft, online, setOnline,
    contextRequestRef, draftRevision, setDraftRevision, draftSaveStatus, setDraftSaveStatus,
    draftHydrated, setDraftHydrated, lastSavedDraft, draftSaves, conflictedDrafts,
    draftConflict, setDraftConflict, resolvingConflict, setResolvingConflict, conflictServerContext
  };
}

export type WorkbenchTaskState = ReturnType<typeof useWorkbenchTaskState>;
