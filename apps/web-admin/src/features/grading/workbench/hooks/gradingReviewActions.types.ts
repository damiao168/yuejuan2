import type { Dispatch, RefObject, SetStateAction } from "react";
import { type RubricPoint } from "../../../../api/papers";
import type { GoldPaperNominationCandidate } from "../../../gold-papers";
import type { DraftFallbackSnapshot, DraftSaveStatus, ScoreDraft, ViewerMode, WorkbenchContext, PrefetchedTaskBundle } from "../gradingWorkbench.types";
import type { DraftConflictResolution } from "../draftConflict";
import type { TaskDraftSaveQueue } from "./taskDraftSaveQueue";
import type { useExamScoring } from "./useExamScoring";
import type { useGradingScoreShortcuts } from "./useGradingScoreShortcuts";
import type { useMathWorkbenchEvidence } from "./useMathWorkbenchEvidence";
import type { AiGrade, ReviewTask } from "../../../../api/review";
import type { ReviewTaskContextScope } from "../../../../query/reviewTaskContext";

type StateSetter<T> = Dispatch<SetStateAction<T>>;
type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

interface WorkbenchNotifications {
  info: (text: string) => void;
  warning: (text: string) => void;
  error: (text: string) => void;
}

export interface UseGradingReviewActionsOptions {
  queryScope: ReviewTaskContextScope;
  isCurrentScope: () => boolean;
  ctx: WorkbenchContext | null;
  setCtx: StateSetter<WorkbenchContext | null>;
  draft: ScoreDraft;
  setDraft: StateSetter<ScoreDraft>;
  draftConflict: DraftConflictResolution | null;
  canSubmit: boolean;
  canReturn: boolean;
  canEditDraft: boolean;
  canAdoptAiScore: boolean;
  canViewOriginalImage: boolean;
  canManageTasks: boolean;
  hasMoreTasks: boolean;
  currentUserId: string;
  selectedTaskId: string;
  nextTask: ReviewTask | undefined;
  selectedGrade: AiGrade | undefined;
  subjectCode: string;
  maxScore: number;
  rubricPoints: RubricPoint[];
  viewerMode: ViewerMode;
  scale: number;
  rotation: number;
  offset: { x: number; y: number };
  autoFit: boolean;
  math: ReturnType<typeof useMathWorkbenchEvidence>;
  scoreShortcuts: ReturnType<typeof useGradingScoreShortcuts>;
  examScoring: ReturnType<typeof useExamScoring>;
  runAction: RunAction;
  refreshCurrent: () => Promise<void>;
  loadMoreTasks: () => Promise<ReviewTask[]>;
  refreshTaskAggregate: () => Promise<void>;
  restoreViewer: (viewer: DraftFallbackSnapshot["viewer"]) => void;
  invalidateViewerContent: () => void;
  setTasks: StateSetter<ReviewTask[]>;
  setSelectedTaskId: StateSetter<string>;
  setDraftHydrated: StateSetter<boolean>;
  setDraftSaveStatus: StateSetter<DraftSaveStatus>;
  setDraftRevision: StateSetter<number>;
  setDraftConflict: StateSetter<DraftConflictResolution | null>;
  setResolvingConflict: StateSetter<boolean>;
  setGoldPaperCandidate: StateSetter<GoldPaperNominationCandidate | null>;
  setMathRequesting: StateSetter<boolean>;
  contextRequestRef: RefObject<number>;
  activeTask: RefObject<string>;
  activeUserId: RefObject<string>;
  liveDraft: RefObject<ScoreDraft>;
  mathActionLock: RefObject<boolean>;
  conflictedDrafts: RefObject<Set<string>>;
  draftSaves: RefObject<TaskDraftSaveQueue>;
  lastSavedDraft: RefObject<string>;
  conflictServerContext: RefObject<WorkbenchContext | null>;
  prefetchedTaskRef: RefObject<Map<string, Promise<PrefetchedTaskBundle>>>;
  pendingNextRef: RefObject<string | null>;
  suppressAutoSelectRef: RefObject<boolean>;
  notify: WorkbenchNotifications;
}

