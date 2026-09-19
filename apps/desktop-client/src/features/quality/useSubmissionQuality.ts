import { useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { runSubmissionQualityCheck } from "../../api/submissions";
import { getUserErrorMessage } from "../../api/userError";
import type { LocalLogEntry, SubmissionQualityResult } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useSubmissionQuality(client: DesktopApiClient, scanSubmissionId: string, logEvent: LogEvent) {
  const [qualityResult, setQualityResult] = useState<SubmissionQualityResult | null>(null);
  const [qualityError, setQualityError] = useState<string | null>(null);
  const [isCheckingQuality, setIsCheckingQuality] = useState(false);

  const handleRunQualityCheck = async () => {
    const submissionId = scanSubmissionId.trim();
    if (!submissionId) {
      setQualityError("缺少 submission_id，无法运行服务端质量门禁");
      return;
    }
    setQualityError(null);
    setIsCheckingQuality(true);
    try {
      const result = await runSubmissionQualityCheck(client, submissionId);
      setQualityResult(result.result);
      await logEvent("info", "submission quality check completed", JSON.stringify(result.result));
    } catch (error) {
      const message = getUserErrorMessage(error, "服务端质量门禁失败");
      setQualityError(message);
      await logEvent("warning", "submission quality check failed", message);
    } finally {
      setIsCheckingQuality(false);
    }
  };

  return { qualityResult, qualityError, isCheckingQuality, handleRunQualityCheck };
}
