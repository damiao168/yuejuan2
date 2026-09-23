import { CalibrationDrawer } from "../../../calibration";
import { GoldPaperManagerDrawer, GoldPaperNominationDrawer, type GoldPaperNominationCandidate } from "../../../gold-papers";
import { DraftConflictModal } from "./DraftConflictModal";
import type { DraftConflictResolution } from "../draftConflict";
import type { DraftFallbackSnapshot } from "../gradingWorkbench.types";

export interface WorkbenchOverlaysProps {
  goldPaperManagerOpen: boolean;
  initialExamId: string;
  onCloseGoldPaperManager: () => void;
  calibrationQuestionId: string;
  calibrationExamId: string;
  currentUserId: string;
  canManageTasks: boolean;
  onCloseCalibration: () => void;
  onCalibrationQualified: () => void;
  goldPaperCandidate: GoldPaperNominationCandidate | null;
  onCloseGoldPaperNomination: () => void;
  onGoldPaperCreated: () => void;
  draftConflict: DraftConflictResolution | null;
  resolvingConflict: boolean;
  onClearDraftConflict: () => void;
  onApplyDraftConflict: (snapshot: DraftFallbackSnapshot) => void;
}

export function WorkbenchOverlays({
  goldPaperManagerOpen,
  initialExamId,
  onCloseGoldPaperManager,
  calibrationQuestionId,
  calibrationExamId,
  currentUserId,
  canManageTasks,
  onCloseCalibration,
  onCalibrationQualified,
  goldPaperCandidate,
  onCloseGoldPaperNomination,
  onGoldPaperCreated,
  draftConflict,
  resolvingConflict,
  onClearDraftConflict,
  onApplyDraftConflict
}: WorkbenchOverlaysProps) {
  return (
    <>
      <GoldPaperManagerDrawer open={goldPaperManagerOpen} examId={initialExamId || undefined} onClose={onCloseGoldPaperManager} />
      <CalibrationDrawer
        open={Boolean(calibrationQuestionId)}
        examId={calibrationExamId}
        questionId={calibrationQuestionId}
        graderId={currentUserId}
        canManagePolicy={canManageTasks}
        onClose={onCloseCalibration}
        onQualified={onCalibrationQualified}
      />
      <GoldPaperNominationDrawer candidate={goldPaperCandidate} onClose={onCloseGoldPaperNomination} onCreated={onGoldPaperCreated} />
      <DraftConflictModal
        conflict={draftConflict}
        loading={resolvingConflict}
        onCancel={onClearDraftConflict}
        onApply={onApplyDraftConflict}
      />
    </>
  );
}
