interface CaptureSummary {
  total: number;
  pending: number;
  processing: number;
  completed: number;
  failed: number;
}

export function SubmissionCaptureSummary({ summary }: { summary: CaptureSummary }) {
  return (
      <section className="capture-summary-grid">
        <div className="metric-tile">
          <span>答题卡总数</span>
          <strong>{summary.total}</strong>
          <small>当前考试</small>
        </div>
        <div className="metric-tile">
          <span>待处理</span>
          <strong>{summary.pending}</strong>
          <small>等待检查或切题</small>
        </div>
        <div className="metric-tile">
          <span>处理中</span>
          <strong>{summary.processing}</strong>
          <small>识别排队或执行中</small>
        </div>
        <div className="metric-tile">
          <span>已完成</span>
          <strong>{summary.completed}</strong>
          <small>已识别并生成题目区域</small>
        </div>
        <div className="metric-tile">
          <span>失败</span>
          <strong>{summary.failed}</strong>
          <small>可在列表直接重试</small>
        </div>
      </section>  );
}
