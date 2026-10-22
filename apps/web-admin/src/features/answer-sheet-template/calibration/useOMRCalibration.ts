import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  approveOMRCalibration,
  createOMRCalibration,
  discardOMRCalibration,
  downloadOMRCalibrationCaseImage,
  getOMRCalibration,
  labelOMRCalibrationCase,
  listOMRCalibrations,
  revokeOMRCalibration,
  type AnswerSheetTemplate,
  type OMRCalibrationCase,
  type OMRCalibrationDetail,
  type OMRCalibrationSession
} from "../../../api/configuration";
import { LatestRequestController } from "../../shared/latestRequest";

interface NotificationPort {
  error(message: string): void;
  warning(message: string): void;
  success(message: string): void;
}

export interface OMRCalibrationControllerOptions {
  template?: AnswerSheetTemplate;
  enabled: boolean;
  message: NotificationPort;
  formatError(error: unknown): string;
}

type CalibrationAction = "approve" | "revoke" | "discard";

function performCalibrationAction(action: CalibrationAction, sessionId: string, reason: string) {
  switch (action) {
    case "approve": return approveOMRCalibration(sessionId, reason);
    case "revoke": return revokeOMRCalibration(sessionId, reason);
    default: return discardOMRCalibration(sessionId, reason);
  }
}

function calibrationActionSuccess(action: CalibrationAction) {
  switch (action) {
    case "approve": return "校准已批准，此后整套模板的高把握识别结果可自动确认";
    case "revoke": return "校准已撤销，未完成的识别任务将转入人工复核";
    default: return "草稿已弃用，标注记录会保留备查";
  }
}

export function useOMRCalibration({ template, enabled, message, formatError }: OMRCalibrationControllerOptions) {
  const imageObjectUrlRef = useRef<string | undefined>(undefined);
  const listRequestsRef = useRef(new LatestRequestController());
  const detailRequestsRef = useRef(new LatestRequestController());
  const [calibrations, setCalibrations] = useState<OMRCalibrationSession[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [detail, setDetail] = useState<OMRCalibrationDetail>();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [caseId, setCaseId] = useState("");
  const [expectedOptions, setExpectedOptions] = useState<string[]>([]);
  const [imageUrl, setImageUrl] = useState<string>();
  const [imageLoading, setImageLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [action, setAction] = useState<CalibrationAction>();
  const [actionReason, setActionReason] = useState("");

  const selectedCase = useMemo<OMRCalibrationCase | undefined>(
    () => detail?.cases.find((item) => item.id === caseId) ?? detail?.cases[0],
    [caseId, detail]
  );

  const showDetail = useCallback((next: OMRCalibrationDetail) => {
    setDetail(next);
    setExpectedOptions([]);
    setDrawerOpen(true);
    setCaseId((current) => next.cases.some((item) => item.id === current)
      ? current
      : next.cases.find((item) => item.matches === undefined)?.id ?? next.cases[0]?.id ?? "");
  }, []);

  const reload = useCallback(async (templateId = template?.id) => {
    if (!templateId) return;
    const request = listRequestsRef.current.begin(templateId);
    setLoading(true);
    setError(undefined);
    try {
      const response = await listOMRCalibrations(templateId);
      if (request.isCurrent()) setCalibrations(response.calibrations);
    } catch (loadError) {
      if (request.isCurrent()) {
        setCalibrations([]);
        setError(formatError(loadError));
      }
    } finally {
      if (request.isCurrent()) setLoading(false);
    }
  }, [formatError, template?.id]);

  useEffect(() => {
    // 模板或校准权限变化时丢弃旧详情动作；服务端可能已执行，不能把迟到结果回写到新上下文。
    listRequestsRef.current.invalidate();
    detailRequestsRef.current.invalidate();
    setDetail(undefined);
    setDrawerOpen(false);
    setCaseId("");
    setBusy(false);
    setAction(undefined);
    setActionReason("");
    setExpectedOptions([]);
    const invalidateRequests = () => {
      listRequestsRef.current.invalidate();
      detailRequestsRef.current.invalidate();
    };
    if (!template?.id || !enabled) {
      setCalibrations([]);
      setError(undefined);
      setLoading(false);
    } else {
      void reload(template.id);
    }
    return invalidateRequests;
  }, [enabled, reload, template?.id]);

  const selectedAnswerSegmentId = selectedCase?.answer_segment_id;
  useEffect(() => {
    let active = true;
    if (imageObjectUrlRef.current) URL.revokeObjectURL(imageObjectUrlRef.current);
    imageObjectUrlRef.current = undefined;
    setImageUrl(undefined);
    if (!selectedAnswerSegmentId) {
      setImageLoading(false);
      return () => { active = false; };
    }
    setImageLoading(true);
    void downloadOMRCalibrationCaseImage(selectedAnswerSegmentId)
      .then((download) => {
        if (!active) return;
        const url = URL.createObjectURL(download.blob);
        imageObjectUrlRef.current = url;
        setImageUrl(url);
      })
      .catch((imageError) => {
        if (active) message.error(`无法加载样本图片：${formatError(imageError)}`);
      })
      .finally(() => { if (active) setImageLoading(false); });
    return () => {
      active = false;
      if (imageObjectUrlRef.current) URL.revokeObjectURL(imageObjectUrlRef.current);
      imageObjectUrlRef.current = undefined;
    };
  }, [formatError, message, selectedAnswerSegmentId]);

  // 每份样本重新盲标，不能把上份选项作为下一份的默认答案。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 每份校准样本必须重新盲标；不能把上一份选项直接带到下一份。
  useEffect(() => setExpectedOptions([]), [selectedCase?.id]);

  const open = useCallback(async (calibrationId: string) => {
    if (!enabled || !template?.id) return;
    const request = detailRequestsRef.current.begin(template.id);
    setBusy(true);
    try {
      const response = await getOMRCalibration(calibrationId);
      if (request.isCurrent()) showDetail(response.calibration);
    } catch (openError) {
      if (request.isCurrent()) message.error(formatError(openError));
    } finally {
      if (request.isCurrent()) setBusy(false);
    }
  }, [enabled, formatError, message, showDetail, template?.id]);

  const start = useCallback(async () => {
    if (!template) {
      message.error("请先选择已锁定的答题卡模板");
      return;
    }
    if (!enabled) return;
    const request = detailRequestsRef.current.begin(template.id);
    setBusy(true);
    try {
      const response = await createOMRCalibration(template.id);
      if (!request.isCurrent()) return;
      showDetail(response.calibration);
      await reload(template.id);
      if (request.isCurrent()) message.success("整套模板的分层校准样本已生成，请逐份盲标实际填涂结果");
    } catch (createError) {
      if (request.isCurrent()) message.error(formatError(createError));
    } finally {
      if (request.isCurrent()) setBusy(false);
    }
  }, [enabled, formatError, message, reload, showDetail, template]);

  const labelCase = useCallback(async (options: string[]) => {
    if (!detail || !selectedCase || detail.session.status !== "draft" || !enabled || detail.session.template_id !== template?.id) return;
    const request = detailRequestsRef.current.begin(detail.session.template_id);
    setBusy(true);
    try {
      const response = await labelOMRCalibrationCase(detail.session.id, selectedCase.id, options);
      if (!request.isCurrent()) return;
      showDetail(response.calibration);
      setCaseId(response.calibration.cases.find((item) => item.matches === undefined)?.id ?? selectedCase.id);
      await reload(detail.session.template_id);
      // 刷新列表也可能跨越模板切换；通知和 busy 恢复只能属于仍有效的详情请求。
      if (!request.isCurrent()) return;
      if (response.calibration.session.summary.eligible_mismatch_count > 0) {
        message.warning("高置信候选出现人工核对错误，本次校准不能批准自动确认");
      } else {
        message.success("已完成盲标（提交后不可修改）");
      }
    } catch (labelError) {
      if (request.isCurrent()) message.error(formatError(labelError));
    } finally {
      if (request.isCurrent()) setBusy(false);
    }
  }, [detail, enabled, formatError, message, reload, selectedCase, showDetail, template?.id]);

  const submitAction = useCallback(async () => {
    if (!detail || !action || !enabled || detail.session.template_id !== template?.id) return;
    const reason = actionReason.trim();
    if (reason.length < 10) {
      message.error("请填写操作原因（至少 10 个字），将记入操作记录");
      return;
    }
    const request = detailRequestsRef.current.begin(detail.session.template_id);
    setBusy(true);
    try {
      const response = await performCalibrationAction(action, detail.session.id, reason);
      if (!request.isCurrent()) return;
      showDetail(response.calibration);
      await reload(detail.session.template_id);
      if (!request.isCurrent()) return;
      setAction(undefined);
      message.success(calibrationActionSuccess(action));
    } catch (actionError) {
      if (request.isCurrent()) message.error(formatError(actionError));
    } finally {
      if (request.isCurrent()) setBusy(false);
    }
  }, [action, actionReason, detail, enabled, formatError, message, reload, showDetail, template?.id]);

  return {
    calibrations, loading, error, detail, drawerOpen, setDrawerOpen,
    selectedCase, caseId, setCaseId, expectedOptions, setExpectedOptions,
    imageUrl, imageLoading, busy, action, setAction, actionReason, setActionReason,
    reload, open, start, labelCase, submitAction
  };
}
