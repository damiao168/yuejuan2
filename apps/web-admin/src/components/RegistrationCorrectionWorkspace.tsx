import { Alert, App, Button, Input, InputNumber, Space, Switch } from "antd";
import { ArrowLeft, Check, Eye, RotateCcw, Undo2, ZoomIn, ZoomOut, } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { applyRegistrationCorrection, createRegistrationCorrection, getRegistrationCorrection, getRegistrationCorrectionContext, previewRegistrationCorrection, undoRegistrationCorrection, type RegistrationCorrection } from "../api/capture";
import { clamp, correctionErrorText, correctionStatusText, errorText, imageURL, initialPoints, labels, PointCanvas, previewFileAssetId } from "./RegistrationCorrectionSupport";

export function RegistrationCorrectionWorkspace({
  runId,
  canManage,
  showCoordinates = false,
  onClose,
  onChanged,
}: Readonly<{
  runId: string;
  canManage: boolean;
  showCoordinates?: boolean;
  onClose: () => void;
  onChanged: () => Promise<void>;
}>) {
  const { message } = App.useApp();
  // 答卷导入页要求信息常显；其他入口保持原有的精确坐标折叠方式。
  const CoordinatesSection = showCoordinates ? "section" : "details";
  const [sourceURL, setSourceURL] = useState<string>();
  const [templateURL, setTemplateURL] = useState<string>();
  const [previewEvidence, setPreviewEvidence] = useState<{ url: string; signature: string; correctionId: string; revision: number; assetId: string; rendered: boolean }>();
  const [previewError, setPreviewError] = useState("");
  const [correctionSignature, setCorrectionSignature] = useState("");
  const [context, setContext] =
    useState<
      Awaited<ReturnType<typeof getRegistrationCorrectionContext>>["context"]
    >();
  const [sourcePoints, setSourcePoints] = useState(initialPoints);
  const [templatePoints, setTemplatePoints] = useState(initialPoints);
  const [advanced, setAdvanced] = useState(false);
  const [zoom, setZoom] = useState(0.8);
  const [correction, setCorrection] = useState<RegistrationCorrection>();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [materialRetry, setMaterialRetry] = useState(0);
  const [materialLoading, setMaterialLoading] = useState(true);
  const [contextError, setContextError] = useState("");
  const [sourceError, setSourceError] = useState("");
  const [templateError, setTemplateError] = useState("");
  const materialContext = useRef<typeof context>(undefined);
  // 同一挂载也可能换页面或点位；代次隔离旧请求，签名把图像和服务端冻结点位绑定。
  const previewRequest = useRef(0);
  const signature = JSON.stringify({ runId, pageRevision: context?.page_revision, sourcePoints, templatePoints, advanced });
  const validPreview = Boolean(previewEvidence?.rendered && previewEvidence.signature === signature &&
    previewEvidence.correctionId === correction?.id && previewEvidence.revision === correction?.revision &&
    previewEvidence.assetId === correction?.preview_registered_file_asset_id);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      previewRequest.current += 1;
    };
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换校正任务后必须丢弃旧图、旧点位和旧预览，runId 是这组状态的身份键。
  useEffect(() => {
    previewRequest.current += 1;
    materialContext.current = undefined;
    setContext(undefined);
    setSourceURL(undefined);
    setTemplateURL(undefined);
    setCorrection(undefined);
    setReason("");
    setPreviewEvidence(undefined);
    setPreviewError("");
    setCorrectionSignature("");
    setSourcePoints(initialPoints);
    setTemplatePoints(initialPoints);
    setAdvanced(false);
    setBusy(false);
  }, [runId]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: materialRetry 只重读材料并作废旧预览；同一任务的点位仍保留供核对。
  useEffect(() => {
    let active = true;
    const urls: string[] = [];
    // 重读只更新材料，不重置同一页的点位；读取期间旧预览不能继续应用。
    previewRequest.current += 1;
    setPreviewEvidence(undefined);
    setCorrectionSignature("");
    setSourceURL(undefined);
    setTemplateURL(undefined);
    setMaterialLoading(true);
    setContextError("");
    setSourceError("");
    setTemplateError("");
    void (async () => {
      try {
        const result = await getRegistrationCorrectionContext(runId);
        if (!active) return;
        if (result.context.registration_run_id !== runId) throw new Error("材料与当前答卷页不一致，请返回批次重新选择。");
        const previous = materialContext.current;
        if (previous && (previous.page_revision !== result.context.page_revision ||
          previous.source_file_asset_id !== result.context.source_file_asset_id ||
          previous.template_file_asset_id !== result.context.template_file_asset_id ||
          previous.page_no !== result.context.page_no)) {
          setCorrection(undefined);
          setPreviewError("答卷或模板版本已更新，已保留当前点位；请核对新图像并重新生成校正预览。");
        }
        materialContext.current = result.context;
        setContext(result.context);
        const readImage = async (kind: "source" | "template", fileId: string, pageNo?: number) => {
          try {
            const url = await imageURL(fileId, pageNo);
            if (!active) { URL.revokeObjectURL(url); return; }
            urls.push(url);
            (kind === "source" ? setSourceURL : setTemplateURL)(url);
          } catch (error) {
            if (active) (kind === "source" ? setSourceError : setTemplateError)(errorText(error));
          }
        };
        await Promise.all([readImage("source", result.context.source_file_asset_id), readImage("template", result.context.template_file_asset_id, result.context.page_no)]);
      } catch (error) {
        if (active) { setContextError(errorText(error)); setContext(undefined); }
      } finally {
        if (active) setMaterialLoading(false);
      }
    })();
    return () => {
      active = false;
      urls.forEach(URL.revokeObjectURL);
    };
  }, [runId, materialRetry]);
  useEffect(
    () => () => {
      if (previewEvidence?.url) URL.revokeObjectURL(previewEvidence.url);
    },
    [previewEvidence?.url],
  );
  function invalidatePreview() {
    previewRequest.current += 1;
    setPreviewEvidence(undefined);
    setPreviewError("");
    setCorrectionSignature("");
    if (correction?.status !== "applied") setCorrection(undefined);
  }
  async function loadPreviewImage(current: RegistrationCorrection, frozenSignature: string, requestId: number) {
    const recordSignature = JSON.stringify({ runId: current.base_registration_run_id, pageRevision: current.source_page_revision,
      sourcePoints: current.source_points, templatePoints: current.template_points, advanced: current.advanced_anchor_mode });
    if (recordSignature !== frozenSignature) throw new Error("校正记录与当前页面或点位不一致，请重新生成预览。");
    const assetId = previewFileAssetId(current);
    const url = await imageURL(assetId);
    if (!mountedRef.current || requestId !== previewRequest.current) {
      URL.revokeObjectURL(url);
      return;
    }
    setPreviewEvidence({ url, signature: frozenSignature, correctionId: current.id, revision: current.revision, assetId, rendered: false });
  }
  async function preview() {
    if (context?.registration_run_id !== runId || !sourceURL || !templateURL || materialLoading || busy || !canManage) return;
    const requestId = ++previewRequest.current;
    const frozenSignature = signature;
    const isCurrent = () => mountedRef.current && requestId === previewRequest.current;
    setPreviewEvidence(undefined);
    setCorrection(undefined);
    setCorrectionSignature(frozenSignature);
    setPreviewError("");
    setBusy(true);
    try {
      // 预览先冻结本次点位和页面版本；后续应用使用返回的校正记录及其版本。
      const created = await createRegistrationCorrection(runId, {
        page_revision: context.page_revision,
        source_points: sourcePoints,
        template_points: templatePoints,
        advanced_anchor_mode: advanced,
      });
      if (!isCurrent()) return;
      let current = (
        await previewRegistrationCorrection(
          created.correction.id,
          created.correction.revision,
        )
      ).correction;
      if (!isCurrent()) return;
      setCorrection(current);
      for (
        let i = 0;
        i < 60 && ["queued", "draft"].includes(current.status);
        i++
      ) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        if (!isCurrent()) return;
        current = (await getRegistrationCorrection(current.id)).correction;
        if (!isCurrent()) return;
        setCorrection(current);
      }
      await loadPreviewImage(current, frozenSignature, requestId);
      if (!isCurrent()) return;
      message.success("校正预览已生成");
    } catch (error) {
      if (!isCurrent()) return;
      setPreviewError(errorText(error));
      message.error(errorText(error));
    } finally {
      if (isCurrent()) setBusy(false);
    }
  }
  async function retryPreviewImage() {
    if (!correction || correctionSignature !== signature || busy) return;
    const requestId = ++previewRequest.current;
    setPreviewEvidence(undefined);
    setPreviewError("");
    setBusy(true);
    try {
      // 重试只读取这份冻结记录的图像，不再创建另一份点位记录。
      await loadPreviewImage(correction, correctionSignature, requestId);
    } catch (error) {
      if (requestId === previewRequest.current && mountedRef.current) setPreviewError(errorText(error));
    } finally {
      if (requestId === previewRequest.current && mountedRef.current) setBusy(false);
    }
  }
  async function apply() {
    if (!correction || !reason.trim() || !canManage || busy || !validPreview || correction.status !== "preview_ready") return;
    const requestId = ++previewRequest.current;
    const isCurrent = () => mountedRef.current && requestId === previewRequest.current;
    setBusy(true);
    try {
      const result = await applyRegistrationCorrection(
        correction.id,
        correction.revision,
        reason.trim(),
      );
      if (!isCurrent()) return;
      setCorrection(result.correction);
      setReason("");
      // 批次回读失败不撤销服务端已保存的校正，本地仍保留返回的状态。
      await onChanged();
      if (!isCurrent()) return;
      message.success("人工校正已应用");
    } catch (error) {
      if (isCurrent()) message.error(errorText(error));
    } finally {
      if (isCurrent()) setBusy(false);
    }
  }
  async function undo() {
    if (!correction || !reason.trim() || !canManage || busy || correction.status !== "applied") return;
    const requestId = ++previewRequest.current;
    const isCurrent = () => mountedRef.current && requestId === previewRequest.current;
    setBusy(true);
    try {
      const result = await undoRegistrationCorrection(
        correction.id,
        correction.revision,
        reason.trim(),
      );
      if (!isCurrent()) return;
      setCorrection(result.correction);
      setReason("");
      await onChanged();
      if (!isCurrent()) return;
      message.success("已撤销校正，恢复原始处理结果");
    } catch (error) {
      if (isCurrent()) message.error(errorText(error));
    } finally {
      if (isCurrent()) setBusy(false);
    }
  }
  function updatePoint(
    target: "source" | "template",
    index: number,
    axis: "x" | "y",
    value: number | null,
  ) {
    invalidatePreview();
    const setter = target === "source" ? setSourcePoints : setTemplatePoints;
    setter((points) =>
      points.map((point, current) =>
        current === index ? { ...point, [axis]: clamp(value ?? 0) } : point,
      ),
    );
  }
  return (
    <div className="correction-workspace">
      <header className="correction-workspace-head">
        <Button icon={<ArrowLeft size={16} />} onClick={onClose}>
          返回批次
        </Button>
        <div>
          <span>人工版面对齐</span>
          <h1>第 {context?.page_no ?? "-"} 页边界校正</h1>
        </div>
        <Space>
          <Button loading={materialLoading} disabled={busy} onClick={() => setMaterialRetry((value) => value + 1)}>重新读取校正材料</Button>
          <Button
            icon={<ZoomOut size={16} />}
            disabled={zoom <= 0.6}
            onClick={() => setZoom((value) => Math.max(0.6, value - 0.1))}
            aria-label="缩小"
          />
          <strong>{Math.round(zoom * 100)}%</strong>
          <Button
            icon={<ZoomIn size={16} />}
            disabled={zoom >= 1.2}
            onClick={() => setZoom((value) => Math.min(1.2, value + 0.1))}
            aria-label="放大"
          />
        </Space>
      </header>
      <Alert
        type="warning"
        className="correction-mobile-note"
        message="精确边界校正需要在电脑上操作，当前设备可查看状态和预览结果。"
        showIcon
      />
      {contextError ? <Alert type="error" showIcon message="校正材料读取失败" description={`${contextError} 当前点位已保留；重新读取后再预览。`} action={<Button onClick={() => setMaterialRetry((value) => value + 1)}>重新读取校正材料</Button>} /> : null}
      <div className="correction-layout">
        <PointCanvas
          title="学生答卷页"
          url={sourceURL}
          points={sourcePoints}
          editable={canManage && !busy && correction?.status !== "applied"}
          zoom={zoom}
          loading={materialLoading}
          error={sourceError || contextError}
          onRetry={() => setMaterialRetry((value) => value + 1)}
          onImageError={() => { invalidatePreview(); setSourceURL(undefined); setSourceError("答卷图像无法显示，请重新读取。"); }}
          onChange={(points) => { invalidatePreview(); setSourcePoints(points); }}
        />
        <PointCanvas
          title="答题卡模板"
          url={templateURL}
          points={templatePoints}
          editable={canManage && !busy && advanced && correction?.status !== "applied"}
          zoom={zoom}
          loading={materialLoading}
          error={templateError || contextError}
          onRetry={() => setMaterialRetry((value) => value + 1)}
          onImageError={() => { invalidatePreview(); setTemplateURL(undefined); setTemplateError("模板图像无法显示，请重新读取。"); }}
          onChange={(points) => { invalidatePreview(); setTemplatePoints(points); }}
        />
        <aside className="correction-inspector">
          <div className="correction-inspector-head">
            <strong>对应点</strong>
            <Button
              icon={<RotateCcw size={15} />}
              onClick={() => {
                invalidatePreview();
                setSourcePoints(initialPoints);
                setTemplatePoints(initialPoints);
              }}
              disabled={!canManage || busy || correction?.status === "applied"}
            >
              重置
            </Button>
          </div>
          <label className="correction-mode">
            <span>同时调整模板参考点</span>
            <Switch
              checked={advanced}
              onChange={(value) => { invalidatePreview(); setAdvanced(value); }}
              disabled={!canManage || busy || correction?.status === "applied"}
            />
          </label>
          <p className="correction-mode-hint">
            仅当模板本身印刷或位置有偏移时开启；一般只需调整左侧答卷的四个点。
          </p>
          <CoordinatesSection className="correction-exact-coordinates">{showCoordinates ? <h3>精确坐标</h3> : <summary>精确坐标</summary>}<p>以页面左上角为原点；横向、纵向均为0–1比例，1表示整页宽或高。</p>
          {labels.map((label, index) => (
            <div className="correction-point-row" key={label}>
              <strong>
                <i className={`point-swatch point-${index + 1}`} />
                {index + 1} {label}
              </strong>
              <span>答卷</span>
              <InputNumber
                aria-label={`答卷${label}横向位置`}
                min={0}
                max={1}
                step={0.001}
                precision={3}
                value={sourcePoints[index].x}
                disabled={!canManage || busy || correction?.status === "applied"}
                onChange={(value) => updatePoint("source", index, "x", value)}
              />
              <InputNumber
                aria-label={`答卷${label}纵向位置`}
                min={0}
                max={1}
                step={0.001}
                precision={3}
                value={sourcePoints[index].y}
                disabled={!canManage || busy || correction?.status === "applied"}
                onChange={(value) => updatePoint("source", index, "y", value)}
              />
              {advanced ? (
                <>
                  <span>模板</span>
                  <InputNumber
                    aria-label={`模板${label}横向位置`}
                    min={0}
                    max={1}
                    step={0.001}
                    precision={3}
                    value={templatePoints[index].x}
                    disabled={!canManage || busy || correction?.status === "applied"}
                    onChange={(value) =>
                      updatePoint("template", index, "x", value)
                    }
                  />
                  <InputNumber
                    aria-label={`模板${label}纵向位置`}
                    min={0}
                    max={1}
                    step={0.001}
                    precision={3}
                    value={templatePoints[index].y}
                    disabled={!canManage || busy || correction?.status === "applied"}
                    onChange={(value) =>
                      updatePoint("template", index, "y", value)
                    }
                  />
                </>
              ) : null}
            </div>
          ))}
          </CoordinatesSection>
          <div
            className={`correction-validation ${correction?.status ?? "draft"}`}
          >
            <strong>
              {correctionStatusText(correction?.status)}
            </strong>
            {correction?.coverage ? (
              <span>校正后覆盖页面 {Math.round(correction.coverage * 100)}%</span>
            ) : (
              <span>预览仅供查看，点击『应用校正』后才会生效</span>
            )}
            {correction?.error_code ? (
              <span title={correction.error_code}>
                {correctionErrorText(correction.error_code)}
              </span>
            ) : null}
          </div>
          {previewEvidence?.signature === signature ? (
            <div className="correction-preview">
              <span>校正结果</span>
              <img key={previewEvidence.url} src={previewEvidence.url} alt="人工校正预览"
                onLoad={() => setPreviewEvidence((current) => current?.url === previewEvidence.url ? { ...current, rendered: true } : current)}
                onError={() => { setPreviewEvidence(undefined); setPreviewError("校正图像无法显示，请重新加载后核对。"); }} />
            </div>
          ) : null}
          {previewError ? <Alert type="error" showIcon message={previewError} action={correction?.status === "preview_ready" && correctionSignature === signature ? <Button disabled={busy} onClick={() => void retryPreviewImage()}>重新加载校正预览</Button> : undefined} /> : null}
          <label className="correction-reason-field">
            <span>操作原因（必填，记入操作记录）</span>
            <Input.TextArea
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              rows={2}
              placeholder="例如：扫描歪斜，人工重新对齐边界"
              maxLength={300}
              disabled={!canManage || busy}
            />
          </label>
          <Space wrap>
            <Button
              icon={<Eye size={16} />}
              onClick={() => void preview()}
              loading={busy}
              disabled={!canManage || !sourceURL || !templateURL || materialLoading || busy || correction?.status === "applied"}
            >
              预览校正
            </Button>
            <Button
              type="primary"
              icon={<Check size={16} />}
              onClick={() => void apply()}
              loading={busy}
              disabled={
                !canManage ||
                busy || !validPreview ||
                correction?.status !== "preview_ready" ||
                !reason.trim()
              }
            >
              应用校正
            </Button>
            <Button
              icon={<Undo2 size={16} />}
              onClick={() => void undo()}
              loading={busy}
              disabled={
                !canManage || correction?.status !== "applied" || !reason.trim()
              }
            >
              撤销应用
            </Button>
          </Space>
        </aside>
      </div>
    </div>
  );
}
