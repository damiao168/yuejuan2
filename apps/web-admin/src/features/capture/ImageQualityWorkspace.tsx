import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Empty, Progress, Segmented, Select, Skeleton, Space, Switch } from "antd";
import { Image as ImageIcon, RefreshCw, ScanSearch, ShieldAlert } from "lucide-react";
import {
  listPageQualityRuns,
  runImageQualityCheck,
  type CapturePage,
  type ImageQualityIssue,
  type ImageQualityRun,
} from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";
import { downloadFileBlob } from "../../api/files";
import { StatusTag } from "../../components/StatusTag";
import {
  buildQualityDimensionRows,
  filterQualityPages,
  isRecord,
  numeric,
  qualityDecision,
  qualityPageCounts,
  qualityScore,
  qualityStatusOf,
  type QualityDimensionRow,
  type QualityPageFilter,
} from "./imageQualityModel";

interface ImageQualityWorkspaceProps {
  pages: CapturePage[];
  initialPageId?: string;
  canManage: boolean;
  onOverride: (page: CapturePage) => void;
  onRefresh: () => Promise<void>;
}

interface FocusPatch {
  row: number;
  column: number;
  x: number;
  y: number;
  width: number;
  height: number;
  focus_score: number;
  status: "good" | "warning" | "bad";
}

const pageStatusLabels: Record<string, string> = {
  passed: "合格",
  review: "需复核",
  failed: "质量不合格",
  unchecked: "待检测",
};

const decisionLabels: Record<string, string> = {
  PASS: "质检通过",
  PASS_WITH_ENHANCEMENT: "已优化，可继续处理",
  LOW_QUALITY: "图像质量需确认",
  REJECT: "图像质量不合格",
  PENDING: "等待检测",
};

const issueLabels: Record<string, string> = {
  low_effective_resolution: "有效分辨率不足",
  low_sharpness: "局部字迹不清晰",
  blank_page: "页面疑似空白",
  incomplete_page_border: "页面边界不完整",
  perspective_risk: "透视变形风险",
  residual_skew: "倾斜超出自动修正范围",
  large_rotation_risk: "旧版横版方向提示",
  bad_exposure: "曝光异常",
  shadow_risk: "阴影影响识别",
  low_local_contrast: "局部对比度不足",
  glare_risk: "答案区疑似反光",
  occlusion_risk: "页面疑似被遮挡",
  noise_or_compression: "噪声或压缩失真",
};

const gateLabels: Record<string, string> = {
  page_decode: "文件可解码",
  low_effective_resolution: "最低有效分辨率",
  severe_blur: "严重模糊",
  incomplete_page: "页面主体完整",
  required_roi_completeness: "必需答题区完整",
  critical_roi_occlusion: "关键答题区无遮挡",
};

export function ImageQualityWorkspace({ pages, initialPageId = "", canManage, onOverride, onRefresh }: Readonly<ImageQualityWorkspaceProps>) {
  const { message } = App.useApp();
  const eligiblePages = useMemo(() => pages.filter((page) => page.submission_page_id && page.status !== "deleted"), [pages]);
  const [filter, setFilter] = useState<QualityPageFilter>("all");
  const filteredPages = useMemo(() => filterQualityPages(eligiblePages, filter), [eligiblePages, filter]);
  const counts = useMemo(() => qualityPageCounts(eligiblePages), [eligiblePages]);
  const [selectedPageId, setSelectedPageId] = useState(initialPageId);
  useEffect(() => { if (initialPageId && pages.some(page => page.id === initialPageId)) setSelectedPageId(initialPageId); }, [initialPageId, pages]);
  const selectedPage = eligiblePages.find((page) => page.id === selectedPageId) ?? filteredPages[0];
  const [runs, setRuns] = useState<ImageQualityRun[]>([]);
  const [runsPageId, setRunsPageId] = useState("");
  const [selectedRunId, setSelectedRunId] = useState("");
  const currentRuns = runsPageId === selectedPage?.id ? runs : [];
  const selectedRun = currentRuns.find((run) => run.id === selectedRunId) ?? currentRuns[0];
  const [runsLoading, setRunsLoading] = useState(false);
  const [runsError, setRunsError] = useState("");
  const [actioning, setActioning] = useState(false);
  const [previewMode, setPreviewMode] = useState<"source" | "normalized">("source");
  const [preview, setPreview] = useState<{ key: string; url: string }>();
  const [sourceEvidence, setSourceEvidence] = useState("");
  const [previewError, setPreviewError] = useState("");
  const [previewRetry, setPreviewRetry] = useState(0);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [showFocusMap, setShowFocusMap] = useState(true);
  // 质检历史与图片预览各自计代次，切页后的迟到响应不能覆盖新选择。
  const runRequest = useRef(0);
  const currentPage = useRef(selectedPage?.id);
  currentPage.current = selectedPage?.id;
  const actionRequest = useRef(0);
  const actionBusy = useRef(false);
  const previewRequest = useRef(0);
  const assetId = previewMode === "normalized" ? selectedRun?.normalized_file_asset_id : selectedPage?.decoded_file_asset_id;
  const previewKey = `${selectedPage?.id ?? ""}:${previewMode}:${assetId ?? ""}`;
  const sourceKey = `${selectedPage?.id ?? ""}:source:${selectedPage?.decoded_file_asset_id ?? ""}`;
  const currentPreview = preview?.key === previewKey ? preview : undefined;

  useEffect(() => {
    if (filteredPages.length === 0) {
      setSelectedPageId("");
      return;
    }
    if (!filteredPages.some((page) => page.id === selectedPageId)) {
      setSelectedPageId(filteredPages[0].id);
    }
  }, [filteredPages, selectedPageId]);

  const loadRuns = useCallback(async (page?: CapturePage) => {
    const requestId = ++runRequest.current;
    setRuns([]);
    setRunsPageId(page?.id ?? "");
    if (!page?.submission_page_id) {
      setRuns([]);
      setRunsLoading(false);
      return;
    }
    setRunsLoading(true);
    setRunsError("");
    try {
      const result = await listPageQualityRuns(page.submission_page_id);
      if (requestId !== runRequest.current) return;
      setRuns(result.runs);
      setSelectedRunId(result.runs[0]?.id ?? "");
    } catch (error) {
      if (requestId !== runRequest.current) return;
      setRuns([]);
      setRunsError(getUserErrorMessage(error, "质检记录加载失败"));
    } finally {
      if (requestId === runRequest.current) setRunsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadRuns(selectedPage);
    actionRequest.current += 1;
    actionBusy.current = false;
    setActioning(false);
    return () => { runRequest.current += 1; actionRequest.current += 1; };
  }, [loadRuns, selectedPage]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: previewRetry 由重试按钮触发；同一文件也需要重新下载，旧请求会被代次淘汰。
  useEffect(() => {
    const requestId = ++previewRequest.current;
    // 新页、新文件或新预览模式都先移除旧图；失败时不能把旧证据标成当前页。
    setPreview(undefined);
    setPreviewError("");
    if (previewMode === "source") setSourceEvidence("");
    if (!assetId) {
      setPreviewLoading(false);
      return;
    }
    setPreviewLoading(true);
    void downloadFileBlob(assetId)
      .then((result) => {
        if (requestId !== previewRequest.current) return;
        const url = URL.createObjectURL(result.blob);
        setPreview({ key: previewKey, url });
      })
      .catch((error) => {
        if (requestId === previewRequest.current) {
          setPreviewError(getUserErrorMessage(error, "页面预览加载失败"));
          if (previewMode === "source") setSourceEvidence("");
        }
      })
      .finally(() => {
        if (requestId === previewRequest.current) setPreviewLoading(false);
      });
    // 请求失效与 URL 回收分开；成功更新 URL 不能废弃本次请求的 finally。
    return () => {
      previewRequest.current += 1;
    };
  }, [assetId, previewKey, previewMode, previewRetry]);

  useEffect(
    () => () => {
      if (preview?.url) URL.revokeObjectURL(preview.url);
    },
    [preview],
  );

  const dimensions = useMemo(() => buildQualityDimensionRows(selectedRun), [selectedRun]);
  const focusPatches = useMemo(() => readFocusPatches(selectedRun), [selectedRun]);
  const gates = useMemo(() => readArray(selectedRun?.quality_report.hard_gates), [selectedRun]);
  const decision = qualityDecision(selectedRun);
  const score = qualityScore(selectedRun);
  const badFocusPatchRatio = selectedRun ? optionalMetric(selectedRun, "bad_focus_patch_ratio") : undefined;
  const effectiveShortEdge = selectedRun ? optionalMetric(selectedRun, "effective_short_edge_px") : undefined;

  async function rerunSelectedSubmission() {
    if (!canManage || !selectedPage?.submission_id || actionBusy.current || currentPage.current !== selectedPage.id) return;
    const request = ++actionRequest.current;
    const isCurrent = () => request === actionRequest.current && currentPage.current === selectedPage.id;
    actionBusy.current = true;
    setActioning(true);
    try {
      const result = await runImageQualityCheck(selectedPage.submission_id);
      // 补读会成为新的历史请求；换页后不能由旧重检重新发起A页读取并覆盖B页。
      if (!isCurrent()) return;
      await Promise.all([loadRuns(selectedPage), onRefresh()]);
      if (isCurrent()) message.success(`已提交 ${result.runs.length} 页重新检测；原质检记录继续保留`);
    } catch (error) {
      if (isCurrent()) message.error(getUserErrorMessage(error, "重新检测提交失败"));
    } finally {
      if (isCurrent()) { actionBusy.current = false; setActioning(false); }
    }
  }

  if (eligiblePages.length === 0) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="拆页完成后可在这里查看图像质检结果" />;
  }

  return (
    <section className="image-quality-workspace" aria-label="图像质量检测">
      <header className="image-quality-summary">
        <div>
          <span>批次页面</span>
          <strong>{counts.all}</strong>
        </div>
        <div>
          <span>合格</span>
          <strong>{counts.passed}</strong>
        </div>
        <div className={counts.attention ? "attention" : ""}>
          <span>需处理</span>
          <strong>{counts.attention}</strong>
        </div>
        <div>
          <span>待检测</span>
          <strong>{counts.pending}</strong>
        </div>
        <Segmented<QualityPageFilter>
          value={filter}
          onChange={setFilter}
          options={[
            { label: "全部", value: "all" },
            { label: "需处理", value: "attention" },
            { label: "合格", value: "passed" },
            { label: "待检测", value: "pending" },
          ]}
        />
      </header>

      {filteredPages.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前筛选条件下没有页面" />
      ) : (
        <div className="image-quality-layout">
          <QualityPageList
            pages={filteredPages}
            selectedPageId={selectedPage?.id}
            onSelect={(pageId) => {
              setSelectedPageId(pageId);
              setPreviewMode("source");
            }}
          />

          <div className="image-quality-canvas-column">
            <div className="image-quality-toolbar">
              <Segmented
                value={previewMode}
                onChange={(value) => setPreviewMode(value as "source" | "normalized")}
                options={[
                  { label: "原图", value: "source" },
                  { label: "标准化", value: "normalized", disabled: !selectedRun?.normalized_file_asset_id },
                ]}
              />
              <Space>
                <span className="focus-map-toggle">局部清晰度热区</span>
                <Switch size="small" checked={showFocusMap} onChange={setShowFocusMap} disabled={previewMode !== "source" || focusPatches.length === 0} />
              </Space>
            </div>
            <div className="image-quality-canvas">
              <QualityPreview
                loading={previewLoading}
                url={currentPreview?.url ?? ""}
                page={selectedPage}
                onLoad={() => {
                  if (currentPreview && previewMode === "source") setSourceEvidence(sourceKey);
                }}
                onError={() => {
                  setPreview(undefined);
                  setPreviewError("当前页面图像无法显示，请重新加载后核对。");
                  if (previewMode === "source") setSourceEvidence("");
                }}
                focusPatches={focusPatches}
                showFocusMap={showFocusMap && previewMode === "source"}
              />
            </div>
            {previewError ? <Alert type="error" showIcon message={previewError} action={<Button onClick={() => setPreviewRetry((value) => value + 1)}>重新加载当前页</Button>} /> : null}
            {focusPatches.length ? (
              <div className="focus-map-legend"><span className="good" />清晰 <span className="warning" />风险 <span className="bad" />模糊</div>
            ) : null}
          </div>

          <div className="image-quality-inspector">
            <div className="quality-inspector-heading">
              <div>
                <span>检测结论</span>
                <h3>{decisionLabels[decision] ?? decision}</h3>
              </div>
              <Progress type="circle" size={72} percent={score} format={(value) => `${value ?? 0}`} strokeColor={scoreStrokeColor(score)} />
            </div>

            <Space wrap className="quality-actions">
              <Button icon={<RefreshCw size={15} />} loading={actioning} disabled={!canManage || !selectedPage?.submission_id} onClick={() => void rerunSelectedSubmission()}>
                重检本份答卷
              </Button>
              {selectedPage && ["review", "failed"].includes(qualityStatusOf(selectedPage)) ? (
                <Button type="primary" icon={<ShieldAlert size={15} />} disabled={!canManage || !selectedPage.decoded_file_asset_id || sourceEvidence !== sourceKey} onClick={() => {
                  // 放行沿用已实际显示的当前原图，标准化图和下载成功本身不能代替人工核对。
                  if (sourceEvidence === sourceKey) onOverride(selectedPage);
                }}>人工放行</Button>
              ) : null}
            </Space>

            <QualityRunInspector
              loading={runsLoading}
              error={runsError}
              run={selectedRun}
              runs={currentRuns}
              dimensions={dimensions}
              gates={gates}
              badFocusPatchRatio={badFocusPatchRatio}
              effectiveShortEdge={effectiveShortEdge}
              onSelect={setSelectedRunId}
              onRetry={() => void loadRuns(selectedPage)}
            />
          </div>
        </div>
      )}
    </section>
  );
}

function QualityPageList({ pages, selectedPageId, onSelect }: Readonly<{
  pages: CapturePage[];
  selectedPageId?: string;
  onSelect: (pageId: string) => void;
}>) {
  return (
    <div className="image-quality-page-list" aria-label="批次页面质检状态">
      <div className="quality-section-label">页面</div>
      {pages.map((page) => {
        const status = qualityStatusOf(page);
        return (
          <button
            type="button"
            key={page.id}
            className={page.id === selectedPageId ? "image-quality-page-row active" : "image-quality-page-row"}
            onClick={() => onSelect(page.id)}
          >
            <span>
              <strong>扫描页 {page.sequence_no}</strong>
              <small>扫描序号 {page.sequence_no} · {cardPageLabel(page)}</small>
            </span>
            <StatusTag tone={qualityTone(status)}>{pageStatusLabels[status] ?? "检测中"}</StatusTag>
          </button>
        );
      })}
    </div>
  );
}

function QualityPreview({ loading, url, page, focusPatches, showFocusMap, onLoad, onError }: Readonly<{
  loading: boolean;
  url: string;
  page?: CapturePage;
  focusPatches: FocusPatch[];
  showFocusMap: boolean;
  onLoad: () => void;
  onError: () => void;
}>) {
  if (loading) return <Skeleton.Image active />;
  if (!url) return <Empty image={<ImageIcon size={34} />} description="暂无可预览图像" />;
  return (
    <div className="image-quality-document-frame">
      <img key={url} src={url} alt={`扫描页 ${page?.sequence_no ?? ""} 答题卡`} onLoad={onLoad} onError={onError} />
      {showFocusMap ? (
        <div className="focus-map-overlay" aria-label="局部清晰度热区">
          {focusPatches.map((patch) => (
            <span
              key={`${patch.row}-${patch.column}`}
              className={`focus-map-patch ${patch.status}`}
              title={`清晰度 ${Math.round(patch.focus_score * 100)}`}
              style={{ left: `${patch.x * 100}%`, top: `${patch.y * 100}%`, width: `${patch.width * 100}%`, height: `${patch.height * 100}%` }}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

interface QualityRunInspectorProps {
  loading: boolean;
  error: string;
  run?: ImageQualityRun;
  runs: ImageQualityRun[];
  dimensions: QualityDimensionRow[];
  gates: unknown[];
  badFocusPatchRatio?: number;
  effectiveShortEdge?: number;
  onSelect: (runId: string) => void;
  onRetry: () => void;
}

function QualityRunInspector({ loading, error, run, onRetry, ...details }: Readonly<QualityRunInspectorProps>) {
  // 切页时旧记录可能仍在 state 中；记录区先显示加载或错误，再展示检测明细。
  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (error) return <div className="quality-inline-error"><span>{error}</span><Button size="small" onClick={onRetry}>重试</Button></div>;
  if (!run) return <Empty image={<ScanSearch size={32} />} description="该页面尚无真实质检记录" />;
  return <QualityRunDetails run={run} {...details} />;
}

function QualityRunDetails({ run, runs, dimensions, gates, badFocusPatchRatio, effectiveShortEdge, onSelect }: Readonly<
  Omit<QualityRunInspectorProps, "loading" | "error" | "onRetry"> & { run: ImageQualityRun }
>) {
  return (
    <>
      <IssueList issues={run.quality_issues} />
      <div className="quality-run-select">
        <span>检测记录</span>
        <Select
          size="small"
          value={run.id}
          onChange={onSelect}
          options={runs.map((item, index) => ({ value: item.id, label: `${index === 0 ? "当前 · " : "历史 · "}${formatTime(item.completed_at ?? item.created_at)}` }))}
        />
      </div>
      <div className="quality-dimensions">
        <div className="quality-section-label">七项指标</div>
        {dimensions.length ? dimensions.map((dimension) => (
          <div className="quality-dimension-row" key={dimension.key}>
            <span>{dimension.label}</span>
            {dimension.status === "not_evaluated" ? <small>待模板配准判断</small> : <Progress percent={dimension.score} showInfo={false} size="small" status={dimension.status === "failed" ? "exception" : "normal"} />}
            <strong>{dimension.status === "not_evaluated" ? "—" : Math.round(dimension.score)}</strong>
          </div>
        )) : <div className="quality-legacy-note">旧版检测记录未包含七项明细，请重新检测。</div>}
      </div>
      <div className="quality-gates">
        <div className="quality-section-label">处理条件</div>
        {gates.map((raw, index) => {
          const gate = isRecord(raw) ? raw : {};
          const code = typeof gate.code === "string" ? gate.code : `gate-${index}`;
          const status = typeof gate.status === "string" ? gate.status : "not_evaluated";
          return <div key={code}><span>{gateLabels[code] ?? code}</span><StatusTag tone={gateTone(status)}>{gateStatusLabel(status)}</StatusTag></div>;
        })}
      </div>
      <div className="quality-technical-meta">
        <span>算法 {run.profile_name}/{run.profile_version}</span>
        <span>耗时 {run.duration_ms ?? 0} ms</span>
        {badFocusPatchRatio !== undefined ? <span>局部坏块 {formatPercent(badFocusPatchRatio)}</span> : null}
        {effectiveShortEdge !== undefined ? <span>有效短边 {Math.round(effectiveShortEdge)} px</span> : null}
      </div>
    </>
  );
}

function IssueList({ issues }: Readonly<{ issues: ImageQualityIssue[] }>) {
  if (!issues.length) return <div className="quality-pass-note"><span>未发现图像质量问题</span><small>后续会检查题目区域是否完整</small></div>;
  return (
    <div className="quality-issue-list">
      <div className="quality-section-label">触发问题</div>
      {issues.map((issue, index) => (
        <div key={`${issue.code}-${index}`}>
          <ShieldAlert size={16} />
          <span><strong>{issueLabels[issue.code] ?? issue.code}</strong><small>{formatObserved(issue)}</small></span>
        </div>
      ))}
    </div>
  );
}

function readFocusPatches(run?: ImageQualityRun): FocusPatch[] {
  const focus = run?.quality_report.focus;
  const rawMap = isRecord(focus) ? focus.map : undefined;
  if (!Array.isArray(rawMap)) return [];
  return rawMap.flatMap((raw) => {
    if (!isRecord(raw)) return [];
    const status = raw.status;
    if (status !== "good" && status !== "warning" && status !== "bad") return [];
    return [{
      row: numeric(raw.row), column: numeric(raw.column), x: numeric(raw.x), y: numeric(raw.y),
      width: numeric(raw.width), height: numeric(raw.height), focus_score: numeric(raw.focus_score), status,
    }];
  });
}

function readArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function optionalMetric(run: ImageQualityRun, key: string): number | undefined {
  const metrics = run.quality_report.metrics;
  if (!isRecord(metrics)) return undefined;
  const value = metrics[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function formatObserved(issue: ImageQualityIssue): string {
  const actions: Record<string, string> = { recapture: "请重新扫描或上传更清晰的原图", manual_review: "请查看原图确认是否影响作答内容" };
  const value = (raw: unknown): string => typeof raw === "number" ? String(Math.round(raw * 1000) / 1000)
    : typeof raw === "string" ? raw : JSON.stringify(raw) ?? "未知";
  const measured = issue.observed === undefined ? "" : `实测 ${value(issue.observed)}`;
  const threshold = issue.threshold === undefined ? "" : `规则阈值 ${value(issue.threshold)}`;
  return [measured, threshold, actions[issue.action]].filter(Boolean).join("；") || "请查看原图和检测详情";
}

function cardPageLabel(page: CapturePage): string {
  const identity = page.page_identity;
  // 单个 PNG 的文件内页码通常为 1，只有条码、人工指定或视觉匹配证据才能作为答题卡页码显示。
  const reliable = identity.barcode_status === "verified" || identity.barcode_status === "manual_override"
    || (identity.page_no_source === "visual_template_match" && identity.matched_page_no === page.assigned_page_no);
  return reliable && page.assigned_page_no ? `答题卡第 ${page.assigned_page_no} 页` : "答题卡页码待匹配";
}

function scoreStrokeColor(score: number): string {
  if (score >= 78) return "#1677ff";
  if (score >= 50) return "#d48806";
  return "#cf1322";
}

function formatPercent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function formatTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}

function qualityTone(status: string): "success" | "warning" | "danger" | "processing" | "neutral" {
  if (status === "passed") return "success";
  if (status === "review") return "warning";
  if (status === "failed") return "danger";
  return status === "unchecked" ? "neutral" : "processing";
}

function gateTone(status: string): "success" | "danger" | "neutral" {
  if (status === "passed") return "success";
  if (status === "failed") return "danger";
  return "neutral";
}

function gateStatusLabel(status: string): string {
  if (status === "passed") return "通过";
  if (status === "failed") return "阻断";
  return "待模板阶段评估";
}
