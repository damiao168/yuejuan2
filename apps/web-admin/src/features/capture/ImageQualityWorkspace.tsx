import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Empty, Progress, Segmented, Select, Skeleton, Space, Switch } from "antd";
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
  type QualityPageFilter,
} from "./imageQualityModel";

interface ImageQualityWorkspaceProps {
  pages: CapturePage[];
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
  failed: "拒绝",
  unchecked: "待检测",
};

const decisionLabels: Record<string, string> = {
  PASS: "A · 直接通过",
  PASS_WITH_ENHANCEMENT: "B · 增强后通过",
  LOW_QUALITY: "C · 低质量复核",
  REJECT: "D · 拒绝使用",
  PENDING: "等待检测",
};

const issueLabels: Record<string, string> = {
  low_effective_resolution: "有效分辨率不足",
  low_sharpness: "局部字迹不清晰",
  blank_page: "页面疑似空白",
  incomplete_page_border: "页面边界不完整",
  perspective_risk: "透视变形风险",
  residual_skew: "倾斜超出自动修正范围",
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

export function ImageQualityWorkspace({ pages, canManage, onOverride, onRefresh }: ImageQualityWorkspaceProps) {
  const { message } = App.useApp();
  const eligiblePages = useMemo(() => pages.filter((page) => page.submission_page_id && page.status !== "deleted"), [pages]);
  const [filter, setFilter] = useState<QualityPageFilter>("all");
  const filteredPages = useMemo(() => filterQualityPages(eligiblePages, filter), [eligiblePages, filter]);
  const counts = useMemo(() => qualityPageCounts(eligiblePages), [eligiblePages]);
  const [selectedPageId, setSelectedPageId] = useState("");
  const selectedPage = eligiblePages.find((page) => page.id === selectedPageId) ?? filteredPages[0];
  const [runs, setRuns] = useState<ImageQualityRun[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const selectedRun = runs.find((run) => run.id === selectedRunId) ?? runs[0];
  const [runsLoading, setRunsLoading] = useState(false);
  const [runsError, setRunsError] = useState("");
  const [actioning, setActioning] = useState(false);
  const [previewMode, setPreviewMode] = useState<"source" | "normalized">("source");
  const [previewUrl, setPreviewUrl] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [showFocusMap, setShowFocusMap] = useState(true);
  // 质检历史与图片预览各自计代次，切页后的迟到响应不能覆盖新选择。
  const runRequest = useRef(0);
  const previewRequest = useRef(0);

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
  }, [loadRuns, selectedPage]);

  useEffect(() => {
    const requestId = ++previewRequest.current;
    const assetId = previewMode === "normalized" ? selectedRun?.normalized_file_asset_id : selectedPage?.decoded_file_asset_id;
    if (!assetId) {
      setPreviewUrl((current) => {
        if (current) URL.revokeObjectURL(current);
        return "";
      });
      setPreviewLoading(false);
      return;
    }
    setPreviewLoading(true);
    void downloadFileBlob(assetId)
      .then((result) => {
        if (requestId !== previewRequest.current) return;
        const url = URL.createObjectURL(result.blob);
        setPreviewUrl((current) => {
          if (current) URL.revokeObjectURL(current);
          return url;
        });
      })
      .catch((error) => {
        if (requestId === previewRequest.current) message.error(getUserErrorMessage(error, "页面预览加载失败"));
      })
      .finally(() => {
        if (requestId === previewRequest.current) setPreviewLoading(false);
      });
  }, [message, previewMode, selectedPage?.decoded_file_asset_id, selectedRun?.normalized_file_asset_id]);

  useEffect(
    () => () => {
      previewRequest.current += 1;
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    },
    [previewUrl],
  );

  const dimensions = useMemo(() => buildQualityDimensionRows(selectedRun), [selectedRun]);
  const focusPatches = useMemo(() => readFocusPatches(selectedRun), [selectedRun]);
  const gates = useMemo(() => readArray(selectedRun?.quality_report.hard_gates), [selectedRun]);
  const decision = qualityDecision(selectedRun);
  const score = qualityScore(selectedRun);
  const badFocusPatchRatio = selectedRun ? optionalMetric(selectedRun, "bad_focus_patch_ratio") : undefined;
  const effectiveShortEdge = selectedRun ? optionalMetric(selectedRun, "effective_short_edge_px") : undefined;

  async function rerunSelectedSubmission() {
    if (!selectedPage?.submission_id) return;
    setActioning(true);
    try {
      const result = await runImageQualityCheck(selectedPage.submission_id);
      await Promise.all([loadRuns(selectedPage), onRefresh()]);
      message.success(`已提交 ${result.runs.length} 页重新检测；原质检记录继续保留`);
    } catch (error) {
      message.error(getUserErrorMessage(error, "重新检测提交失败"));
    } finally {
      setActioning(false);
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
          <div className="image-quality-page-list" aria-label="批次页面质检状态">
            <div className="quality-section-label">页面</div>
            {filteredPages.map((page) => {
              const status = qualityStatusOf(page);
              return (
                <button
                  type="button"
                  key={page.id}
                  className={page.id === selectedPage?.id ? "image-quality-page-row active" : "image-quality-page-row"}
                  onClick={() => {
                    setSelectedPageId(page.id);
                    setPreviewMode("source");
                  }}
                >
                  <span>
                    <strong>第 {page.assigned_page_no ?? page.sequence_no} 页</strong>
                    <small>扫描序号 {page.sequence_no}</small>
                  </span>
                  <StatusTag tone={qualityTone(status)}>{pageStatusLabels[status] ?? "检测中"}</StatusTag>
                </button>
              );
            })}
          </div>

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
              {previewLoading ? <Skeleton.Image active /> : previewUrl ? (
                <div className="image-quality-document-frame">
                  <img src={previewUrl} alt={`第 ${selectedPage?.assigned_page_no ?? selectedPage?.sequence_no} 页答题卡`} />
                  {showFocusMap && previewMode === "source" ? (
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
              ) : <Empty image={<ImageIcon size={34} />} description="暂无可预览图像" />}
            </div>
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
              <Progress type="circle" size={72} percent={score} format={(value) => `${value ?? 0}`} strokeColor={score >= 78 ? "#1677ff" : score >= 50 ? "#d48806" : "#cf1322"} />
            </div>

            <Space wrap className="quality-actions">
              <Button icon={<RefreshCw size={15} />} loading={actioning} disabled={!canManage || !selectedPage?.submission_id} onClick={() => void rerunSelectedSubmission()}>
                重检本份答卷
              </Button>
              {selectedPage && ["review", "failed"].includes(qualityStatusOf(selectedPage)) ? (
                <Button type="primary" icon={<ShieldAlert size={15} />} disabled={!canManage} onClick={() => onOverride(selectedPage)}>人工放行</Button>
              ) : null}
            </Space>

            {runsLoading ? <Skeleton active paragraph={{ rows: 8 }} /> : runsError ? (
              <div className="quality-inline-error"><span>{runsError}</span><Button size="small" onClick={() => void loadRuns(selectedPage)}>重试</Button></div>
            ) : selectedRun ? (
              <>
                <div className="quality-run-select">
                  <span>检测记录</span>
                  <Select
                    size="small"
                    value={selectedRun.id}
                    onChange={setSelectedRunId}
                    options={runs.map((run, index) => ({ value: run.id, label: `${index === 0 ? "当前 · " : "历史 · "}${formatTime(run.completed_at ?? run.created_at)}` }))}
                  />
                </div>
                <div className="quality-dimensions">
                  <div className="quality-section-label">七项指标</div>
                  {dimensions.length ? dimensions.map((dimension) => (
                    <div className="quality-dimension-row" key={dimension.key}>
                      <span>{dimension.label}</span>
                      <Progress percent={dimension.score} showInfo={false} size="small" status={dimension.status === "failed" ? "exception" : "normal"} />
                      <strong>{Math.round(dimension.score)}</strong>
                    </div>
                  )) : <div className="quality-legacy-note">旧版检测记录未包含七项明细，请重新检测。</div>}
                </div>
                <IssueList issues={selectedRun.quality_issues} />
                <div className="quality-gates">
                  <div className="quality-section-label">Hard Gate</div>
                  {gates.map((raw, index) => {
                    const gate = isRecord(raw) ? raw : {};
                    const code = typeof gate.code === "string" ? gate.code : `gate-${index}`;
                    const status = typeof gate.status === "string" ? gate.status : "not_evaluated";
                    return <div key={code}><span>{gateLabels[code] ?? code}</span><StatusTag tone={gateTone(status)}>{gateStatusLabel(status)}</StatusTag></div>;
                  })}
                </div>
                <div className="quality-technical-meta">
                  <span>算法 {selectedRun.profile_name}/{selectedRun.profile_version}</span>
                  <span>耗时 {selectedRun.duration_ms ?? 0} ms</span>
                  {badFocusPatchRatio !== undefined ? <span>局部坏块 {formatPercent(badFocusPatchRatio)}</span> : null}
                  {effectiveShortEdge !== undefined ? <span>有效短边 {Math.round(effectiveShortEdge)} px</span> : null}
                </div>
              </>
            ) : (
              <Empty image={<ScanSearch size={32} />} description="该页面尚无真实质检记录" />
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function IssueList({ issues }: { issues: ImageQualityIssue[] }) {
  if (!issues.length) return <div className="quality-pass-note"><span>未触发质量问题</span><small>页面可继续进入后续处理门禁</small></div>;
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
  if (issue.observed === undefined) return "已触发质量门禁";
  const observed = typeof issue.observed === "number" ? Math.round(issue.observed * 1000) / 1000 : String(issue.observed);
  return `${issue.metric ?? "观测值"}：${observed}`;
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
