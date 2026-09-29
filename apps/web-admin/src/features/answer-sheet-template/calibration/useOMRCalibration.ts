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

export function useOMRCalibration({ template, enabled, message, formatError }: OMRCalibrationControllerOptions) {
  const imageObjectUrlRef = useRef<string | undefined>(undefined);
  const listRequestsRef = useRef(new LatestRequestController());
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
  const [action, setAction] = useState<"approve" | "revoke" | "discard">();
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
    listRequestsRef.current.invalidate();
    setDetail(undefined);
    setDrawerOpen(false);
    setCaseId("");
    if (!template?.id || !enabled) {
      setCalibrations([]);
      setError(undefined);
      setLoading(false);
      return;
    }
    void reload(template.id);
  }, [enabled, reload, template?.id]);

  useEffect(() => {
    let active = true;
    if (imageObjectUrlRef.current) URL.revokeObjectURL(imageObjectUrlRef.current);
    imageObjectUrlRef.current = undefined;
    setImageUrl(undefined);
    if (!selectedCase) {
      setImageLoading(false);
      return () => { active = false; };
    }
    setImageLoading(true);
    void downloadOMRCalibrationCaseImage(selectedCase.answer_segment_id)
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
  }, [formatError, message, selectedCase?.answer_segment_id]);

  // 每份样本重新盲标，不能把上份选项作为下一份的默认答案。
  useEffect(() => setExpectedOptions([]), [selectedCase?.id]);

  const open = useCallback(async (calibrationId: string) => {
    setBusy(true);
    try {
      showDetail((await getOMRCalibration(calibrationId)).calibration);
    } catch (openError) {
      message.error(formatError(openError));
    } finally {
      setBusy(false);
    }
  }, [formatError, message, showDetail]);

  const start = useCallback(async () => {
    if (!template) {
      message.error("请先选择已锁定的答题卡模板");
      return;
    }
    setBusy(true);
    try {
      const response = await createOMRCalibration(template.id);
      showDetail(response.calibration);
      await reload(template.id);
      message.success("整套模板的分层校准样本已生成，请逐份盲标实际填涂结果");
    } catch (createError) {
      message.error(formatError(createError));
    } finally {
      setBusy(false);
    }
  }, [formatError, message, reload, showDetail, template]);

  const labelCase = useCallback(async (options: string[]) => {
    if (!detail || !selectedCase || detail.session.status !== "draft") return;
    setBusy(true);
    try {
      const response = await labelOMRCalibrationCase(detail.session.id, selectedCase.id, options);
      showDetail(response.calibration);
      setCaseId(response.calibration.cases.find((item) => item.matches === undefined)?.id ?? selectedCase.id);
      await reload(detail.session.template_id);
      if (response.calibration.session.summary.eligible_mismatch_count > 0) {
        message.warning("高置信候选出现人工核对错误，本次校准不能批准自动确认");
      } else {
        message.success("已完成盲标（提交后不可修改）");
      }
    } catch (labelError) {
      message.error(formatError(labelError));
    } finally {
      setBusy(false);
    }
  }, [detail, formatError, message, reload, selectedCase, showDetail]);

  const submitAction = useCallback(async () => {
    if (!detail || !action) return;
    const reason = actionReason.trim();
    if (reason.length < 10) {
      message.error("请填写操作原因（至少 10 个字），将记入操作记录");
      return;
    }
    setBusy(true);
    try {
      const response = action === "approve"
        ? await approveOMRCalibration(detail.session.id, reason)
        : action === "revoke"
          ? await revokeOMRCalibration(detail.session.id, reason)
          : await discardOMRCalibration(detail.session.id, reason);
      showDetail(response.calibration);
      await reload(detail.session.template_id);
      setAction(undefined);
      message.success(action === "approve"
        ? "校准已批准，此后整套模板的高把握识别结果可自动确认"
        : action === "revoke"
          ? "校准已撤销，未完成的识别任务将转入人工复核"
          : "草稿已弃用，标注记录会保留备查");
    } catch (actionError) {
      message.error(formatError(actionError));
    } finally {
      setBusy(false);
    }
  }, [action, actionReason, detail, formatError, message, reload, showDetail]);

  return {
    calibrations, loading, error, detail, drawerOpen, setDrawerOpen,
    selectedCase, caseId, setCaseId, expectedOptions, setExpectedOptions,
    imageUrl, imageLoading, busy, action, setAction, actionReason, setActionReason,
    reload, open, start, labelCase, submitAction
  };
}
