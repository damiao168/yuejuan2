import type { ReviewTaskContext } from '@edugrade/sdk';
import type { AnswerDocument } from '../AnswerDocumentViewer';
import type { DesktopApiClient } from '../../api/client';
import type { AuthUser, LocalLogEntry, OfflineTaskPackage } from '../../types';

export type OfflineTaskPackageWithAnswer = OfflineTaskPackage & {
  /** 任务上下文提供的原始作答或识别文字。 */
  answerText?: string;
  /** 任务上下文按阅卷权限生成的同源图片地址，不是文件管理接口的 asset id。 */
  answerImageUrl?: string;
  /** 识别候选只供核对，不能把历史候选或候选载荷当成人工总分。 */
  aiCandidates?: ReviewTaskContext["ai_candidates"];
  aiSecondOpinion?: ReviewTaskContext["ai_second_opinion"];
};

export type OfflineAnswerPreview = AnswerDocument & { taskId: string; requestId: number };

export interface OfflineWorkbenchProps {
  durableScopeKey: string;
  client: DesktopApiClient;
  token: string | null;
  user: AuthUser | null;
  isOnline: boolean;
  onLog: (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
  registerLeaveGuard?: (guard: OfflineLeaveGuard | null) => void;
  initialTaskId?: string;
}

export interface OfflineLeaveGuard {
  requestLeave: () => Promise<boolean>;
  shouldBlockClose: () => boolean;
}
