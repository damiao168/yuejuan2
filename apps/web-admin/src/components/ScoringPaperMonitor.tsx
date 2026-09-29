import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Empty, Progress, Spin } from "antd";
import { CheckCircle2, CircleAlert, Clock3, FileStack, RefreshCw } from "lucide-react";
import { downloadFileBlob } from "../api/files";
import type { ScoringRun, ScoringRunItem } from "../api/review";
import { StatusTag } from "./StatusTag";

interface ScoringPaperMonitorProps {
  run?: ScoringRun;
  items: ScoringRunItem[];
  loading?: boolean;
  onRefresh: () => void;
}

interface CandidateGroup {
  code: string;
  items: ScoringRunItem[];
  processed: number;
  confirmed: number;
  review: number;
  failed: number;
  scored: number;
  totalScore: number;
  maxScore: number;
}

const stateLabel: Record<string, string> = {
  confirmed: "已评分",
  review: "待确认",
  failed: "失败",
  processing: "处理中",
  queued: "排队中",
  pending: "等待处理",
  cancelled: "已取消",
  cancelling: "正在取消"
};

const activityDetailLabels: Record<string, string> = {
  answer_low_confidence: "识别把握不足，转人工",
  low_confidence: "识别把握不足，转人工",
  rule_not_auto_confirmed: "评分细则未确认，转人工",
  auto_grade_confirmation_failed: "自动评分未通过确认，转人工",
  auto_grade_engine_unavailable: "自动评分暂不可用，转人工",
  omr_ambiguous: "填涂无法判定，转人工",
  omr_multiple: "检测到多处填涂，转人工",
  omr_blank: "未检测到填涂，转人工",
  ocr_failed: "卷面识别失败",
  ocr_timeout: "识别超时",
  timeout: "处理超时",
  retryable_error: "处理出错，正在自动重试",
  terminal_error: "处理失败，可重试",
  dead_letter: "多次失败，需人工处理",
  queued: "排队等待处理",
  leased: "正在处理",
  running: "正在处理",
  succeeded: "处理完成",
  pending: "等待教师确认",
  assigned: "等待教师确认",
  in_progress: "教师确认中",
  returned: "已退回，等待重新确认"
};

function activityDetail(item: ScoringRunItem): { label: string; raw?: string } {
  const raw = item.reason_code || item.error_code || item.runtime_status || item.review_status || "";
  const mapped = raw ? activityDetailLabels[raw.toLowerCase()] : undefined;
  if (mapped) {
    return { label: mapped, raw };
  }
  if (item.state === "failed") {
    return { label: "处理失败，可重试", raw: raw || undefined };
  }
  if (item.state === "review") {
    return { label: "等待教师确认", raw: raw || undefined };
  }
  return { label: "正在处理", raw: raw || undefined };
}

function groupCandidates(items: ScoringRunItem[]) {
  const grouped = new Map<string, ScoringRunItem[]>();
  for (const item of items) {
    const current = grouped.get(item.anonymous_code) ?? [];
    current.push(item);
    grouped.set(item.anonymous_code, current);
  }
  return [...grouped.entries()]
    .map(([code, candidateItems]): CandidateGroup => ({
      code,
      items: candidateItems,
      processed: candidateItems.filter((item) => item.state !== "queued" && item.state !== "processing").length,
      confirmed: candidateItems.filter((item) => item.state === "confirmed").length,
      review: candidateItems.filter((item) => item.state === "review").length,
      failed: candidateItems.filter((item) => item.state === "failed").length,
      scored: candidateItems.filter((item) => typeof item.score === "number").length,
      totalScore: candidateItems.reduce((sum, item) => sum + (item.score ?? 0), 0),
      maxScore: candidateItems.reduce((sum, item) => sum + (item.max_score ?? 0), 0)
    }))
    .sort((left, right) => left.code.localeCompare(right.code));
}

function scoreText(item: ScoringRunItem) {
  if (typeof item.score !== "number") return "";
  const score = Number.isInteger(item.score) ? item.score : item.score.toFixed(1);
  const max = typeof item.max_score === "number"
    ? (Number.isInteger(item.max_score) ? item.max_score : item.max_score.toFixed(1))
    : "";
  return `${item.grade_source === "ai_suggestion" ? "≈" : ""}${score}${max === "" ? "" : `/${max}`}`;
}

export function ScoringPaperMonitor({ run, items, loading, onRefresh }: ScoringPaperMonitorProps) {
  const candidates = useMemo(() => groupCandidates(items), [items]);
  const [selectedCode, setSelectedCode] = useState("");
  const [pageUrls, setPageUrls] = useState<Record<string, string>>({});
  const [pageLoading, setPageLoading] = useState(false);
  const [pageError, setPageError] = useState("");

  useEffect(() => {
    if (!candidates.length) {
      setSelectedCode("");
      return;
    }
    if (!candidates.some((candidate) => candidate.code === selectedCode)) {
      setSelectedCode(candidates[0].code);
    }
  }, [candidates, selectedCode]);

  const selected = candidates.find((candidate) => candidate.code === selectedCode) ?? candidates[0];
  const pages = useMemo(() => {
    const unique = new Map<string, { pageNo: number; assetId: string }>();
    for (const item of selected?.items ?? []) {
      if (item.page_file_asset_id) {
        unique.set(item.page_file_asset_id, { pageNo: item.page_no, assetId: item.page_file_asset_id });
      }
    }
    return [...unique.values()].sort((left, right) => left.pageNo - right.pageNo);
  }, [selected]);
  const pageKey = pages.map((page) => page.assetId).join("|");

  useEffect(() => {
    let disposed = false;
    const urls: string[] = [];
    setPageUrls({});
    setPageError("");
    if (!pages.length) {
      setPageLoading(false);
      return;
    }
    setPageLoading(true);
    void Promise.allSettled(pages.map(async (page) => {
      const file = await downloadFileBlob(page.assetId);
      if (disposed) return null;
      const url = URL.createObjectURL(file.blob);
      urls.push(url);
      return [page.assetId, url] as const;
    }))
      .then((results) => {
        if (disposed) return;
        const entries = results.flatMap((result) =>
          result.status === "fulfilled" && result.value ? [result.value] : []
        );
        const failedCount = results.length - entries.length;
        setPageUrls(Object.fromEntries(entries));
        if (failedCount > 0) {
          setPageError(failedCount === results.length
            ? "整卷影像暂时无法加载，请刷新后重试。"
            : `${failedCount} 页影像暂时无法加载，其余页面已正常显示。`);
        }
      })
      .finally(() => {
        if (!disposed) setPageLoading(false);
      });
    return () => {
      disposed = true;
      urls.forEach((url) => URL.revokeObjectURL(url));
    };
  }, [pageKey]);

  // 此进度统计离开排队/处理中的项目，包含待人工和失败，不能作为成绩已确认比例。
  const processed = items.filter((item) => item.state !== "queued" && item.state !== "processing").length;
  const progress = items.length ? Math.round((processed / items.length) * 100) : 0;
  const activeItems = (selected?.items ?? [])
    .filter((item) => item.state !== "confirmed")
    .sort((left, right) => left.page_no - right.page_no || left.question_no.localeCompare(right.question_no))
    .slice(0, 8);

  if (!items.length) {
    return <div className="scoring-paper-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="评分启动后，这里会逐卷显示处理进度与红笔得分" /></div>;
  }

  return (
    <div className="scoring-paper-monitor">
      <header className="scoring-monitor-header">
        <div>
          <span className="scoring-monitor-kicker">逐卷处理记录</span>
          <h3>{run?.status === "needs_review" ? "自动识别完成，等待教师确认" : run?.status === "completed" ? "本批答卷已处理完成" : "正在逐卷识别与评分"}</h3>
          <p>红色批注标记已确认得分；“≈”表示 AI 建议分，需要教师确认。</p>
        </div>
        <div className="scoring-monitor-progress">
          <div><strong>{processed}</strong><span>/ {items.length} 题已识别 · {run?.review_count ?? 0} 题待确认</span></div>
          <Progress percent={progress} showInfo={false} strokeColor="#1677ff" trailColor="#e8edf5" />
          <Button icon={<RefreshCw size={15} />} loading={loading} onClick={onRefresh}>刷新</Button>
        </div>
      </header>

      <div className="scoring-monitor-body">
        <aside className="scoring-candidate-rail">
          <div className="scoring-rail-title"><FileStack size={16} /> 答卷队列 <span>{candidates.length}</span></div>
          {candidates.map((candidate, index) => {
            return (
              <button
                type="button"
                key={candidate.code}
                className={`scoring-candidate-item ${candidate.code === selected?.code ? "active" : ""}`}
                onClick={() => setSelectedCode(candidate.code)}
              >
                <span className="scoring-candidate-index">{String(index + 1).padStart(2, "0")}</span>
                <span className="scoring-candidate-copy">
                  <strong>{candidate.code}</strong>
                  <small>{candidate.processed}/{candidate.items.length} 已识别 · {candidate.review} 待确认</small>
                </span>
                {candidate.failed > 0
                  ? <CircleAlert size={16} className="danger" />
                  : candidate.confirmed === candidate.items.length
                    ? <CheckCircle2 size={16} className="success" />
                    : <Clock3 size={16} className="processing" />}
              </button>
            );
          })}
        </aside>

        <section className="scoring-paper-stage">
          <div className="scoring-paper-stage-head">
            <div>
              <strong>{selected?.code}</strong>
              <span>{pages.length} 页整卷 · {selected?.scored ?? 0} 题已有分数</span>
            </div>
            <div className="scoring-total-score">
              <span>当前合计</span>
              <strong>{selected?.totalScore ?? 0}</strong>
              <small>{selected?.maxScore ? `/ ${selected.maxScore}` : "分"}</small>
            </div>
          </div>
          <div className="scoring-paper-scroll">
            {pageLoading ? <div className="scoring-page-loading"><Spin /><span>正在装载整卷影像</span></div> : null}
            {pageError ? <Alert type="warning" showIcon message={pageError} /> : null}
            {pages.map((page) => {
              const pageItems = selected?.items.filter((item) => item.page_file_asset_id === page.assetId) ?? [];
              return (
                <figure className="scoring-paper-page" key={page.assetId}>
                  <figcaption>第 {page.pageNo} 页</figcaption>
                  {pageUrls[page.assetId] ? <div className="scoring-paper-image-wrap">
                    <img src={pageUrls[page.assetId]} alt={`${selected?.code} 第 ${page.pageNo} 页`} />
                    {pageItems.filter((item) => typeof item.score === "number").map((item) => {
                      const box = item.normalized_bbox;
                      if (!box) return null;
                      return <span
                        key={item.answer_segment_id}
                        className={`scoring-score-mark ${item.grade_source === "ai_suggestion" ? "suggestion" : ""}`}
                        title={`${item.question_no} · ${stateLabel[item.state] ?? "未知状态"}`}
                        style={{
                          left: `${Math.min(98, (box.x + box.width) * 100)}%`,
                          top: `${Math.max(1, box.y * 100)}%`
                        }}
                      >{item.question_no} {scoreText(item)}</span>;
                    })}
                  </div> : null}
                </figure>
              );
            })}
          </div>
        </section>

        <aside className="scoring-activity-rail">
          <div className="scoring-activity-summary">
            <span>这张答卷</span>
            <div><strong>{selected?.confirmed ?? 0}</strong><small>已确认</small></div>
            <div><strong>{selected?.review ?? 0}</strong><small>待确认</small></div>
            <div><strong className={selected?.failed ? "danger" : ""}>{selected?.failed ?? 0}</strong><small>失败</small></div>
          </div>
          <div className="scoring-activity-list">
            <h4>待处理题目</h4>
            {activeItems.length ? activeItems.map((item) => {
              const detail = activityDetail(item);
              return (
                <div className="scoring-activity-item" key={item.answer_segment_id}>
                  <span className={`scoring-activity-dot ${item.state}`} />
                  <div><strong>{item.question_no} · {stateLabel[item.state] ?? "处理中"}</strong><small title={detail.raw}>{detail.label}</small></div>
                  <StatusTag tone={item.state === "failed" ? "danger" : item.state === "review" ? "warning" : "processing"}>{`第 ${item.page_no} 页`}</StatusTag>
                </div>
              );
            }) : <div className="scoring-activity-done"><CheckCircle2 size={24} /><span>当前答卷已全部确认</span></div>}
          </div>
        </aside>
      </div>
    </div>
  );
}
