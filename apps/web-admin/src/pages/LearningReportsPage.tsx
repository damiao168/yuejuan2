import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, App, Button, Segmented, Select, Space, Tag, type TableColumnsType } from "antd";
import { Download, RefreshCw } from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip as ChartTooltip,
  XAxis,
  YAxis
} from "recharts";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { listAllExams, type Exam } from "../api/exams";
import {
  exportLearningReport,
  getGradingQualityReport,
  getReportOverview,
  listClassReports,
  listQuestionReports,
  type ClassComparison,
  type ClassReport,
  type GradingQualityReport,
  type OverviewReport,
  type QuestionAnalysis,
} from "../api/reports";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { examSubjectLabel } from "../constants/examStatus";
import { ResponsiveTable } from "../components/ResponsiveTable";
import {
  aggregateKnowledge,
  emptyReason,
  flattenErrors,
  formatMetric,
  formatPercent,
  formatScore,
  metricDetail,
  percentValue,
  questionTypeLabels,
  type ErrorRow
} from "./learningReports.model";

interface LearningReportsPageProps {
  canRead: boolean;
  canExport: boolean;
  initialExamId?: string;
}

type ClassMetric = "average" | "passRate";
interface ClassComparisonChartEntry {
  name: string;
  average: number;
  median: number;
  passRate: number;
  excellentRate: number;
  studentCount: number;
}

const chartBlue = "#1677ff";
const chartGreen = "#52c41a";
const chartAmber = "#faad14";
const chartRed = "#ff4d4f";
const chartCyan = "#13c2c2";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

function ChartPanel({
  title,
  description,
  empty,
  emptyTitle,
  children,
  className = ""
}: Readonly<{
  title: string;
  description: string;
  empty: boolean;
  emptyTitle: string;
  children: ReactNode;
  className?: string;
}>) {
  return (
    <section className={`reports-panel ${className}`}>
      <div className="panel-head">
        <div>
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
      </div>
      {empty ? <EmptyState title={emptyTitle} description="暂无数据，成绩发布后自动生成。" /> : <div className="reports-chart">{children}</div>}
    </section>
  );
}

function renderClassComparisonChart(classMetric: ClassMetric, classComparisonData: ClassComparisonChartEntry[], setClassMetric: (metric: ClassMetric) => void) {
  return <ChartPanel className="reports-view-classes" title="班级对比" description={classMetric === "average" ? "按平均分比较，使用同一分数刻度。" : "按及格率比较，使用同一百分比刻度。"} empty={classComparisonData.length === 0} emptyTitle="暂无班级对比">
    <Segmented value={classMetric} options={[{ value: "average", label: "平均分" }, { value: "passRate", label: "及格率" }]} onChange={(next) => setClassMetric(next as ClassMetric)} />
    <ResponsiveContainer width="100%" height={260}>
      <BarChart data={classComparisonData}>
        <CartesianGrid strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="name" />
        <YAxis domain={classMetric === "passRate" ? [0, 100] : undefined} label={{ value: classMetric === "average" ? "分数" : "百分比", angle: -90, position: "insideLeft" }} />
        <ChartTooltip />
        <Bar dataKey={classMetric} name={classMetric === "average" ? "平均分" : "及格率%"} fill={classMetric === "average" ? chartBlue : chartGreen} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  </ChartPanel>;
}

function LearningReportNotices({ canRead, error, empty, onRetry }: Readonly<{
  canRead: boolean;
  error: string | null;
  empty: OverviewReport["empty"];
  onRetry: () => Promise<void>;
}>) {
  // 权限、请求失败和空报告是独立提示；抽出展示后仍可同时出现，不改变父层请求与重试流程。
  return <>
    {!canRead ? <Alert type="error" showIcon message="无报告查看权限" description="当前账号没有查看学情报告的权限，请联系管理员开通。" /> : null}
    {error ? <ErrorState message={error} onRetry={onRetry} /> : null}
    {empty?.empty ? <Alert type="info" showIcon message="报告为空" description={emptyReason(empty.reason)} /> : null}
  </>;
}

export function LearningReportsPage({ canRead, canExport, initialExamId = "" }: Readonly<LearningReportsPageProps>) {
  const { message, modal } = App.useApp();
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState(initialExamId);
  const [overview, setOverview] = useState<OverviewReport | null>(null);
  const [classReports, setClassReports] = useState<ClassReport[]>([]);
  const [questions, setQuestions] = useState<QuestionAnalysis[]>([]);
  const [quality, setQuality] = useState<GradingQualityReport | null>(null);
  const [loadingExams, setLoadingExams] = useState(true);
  const [loadingReports, setLoadingReports] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [lastExport, setLastExport] = useState<{ filename?: string; watermark?: string } | null>(null);
  const [view, setView] = useState<"overview" | "questions" | "classes" | "quality">("overview");
  const [classMetric, setClassMetric] = useState<ClassMetric>("average");
  const [optionQuestionId, setOptionQuestionId] = useState("first-four");
  const reportRequestRef = useRef(0);

  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const isEmptyReport = Boolean(overview?.empty?.empty);
  const canExportReport = canRead && canExport && Boolean(selectedExamId) && Boolean(overview) && !isEmptyReport;

  const classComparisonData = useMemo(() => {
    const source: ClassComparison[] =
      overview?.class_comparisons?.length
        ? overview.class_comparisons
        : classReports.map((item) => ({
            class_id: item.class_id,
            class_name: item.class_name,
            student_count: item.student_count,
            average: item.stats.average,
            median: item.stats.median,
            pass_rate: item.stats.pass_rate,
            excellent_rate: item.stats.excellent_rate
          }));
    return source.map((item) => ({
      name: item.class_name || item.class_id,
      average: item.average,
      median: item.median,
      passRate: percentValue(item.pass_rate),
      excellentRate: percentValue(item.excellent_rate),
      studentCount: item.student_count
    }));
  }, [classReports, overview]);

  const distributionData = useMemo(
    () =>
      (overview?.stats.distribution ?? []).map((item) => ({
        name: item.label,
        count: item.count,
        range: `${item.min}-${item.max}`
      })),
    [overview]
  );

  const questionScoreData = useMemo(
    () =>
      questions.map((item) => ({
        name: item.question_no,
        scoreRate: percentValue(item.score_rate),
        correctRate: percentValue(item.correct_rate),
        discrimination: item.discrimination
      })),
    [questions]
  );

  const knowledgeData = useMemo(() => aggregateKnowledge(classReports), [classReports]);
  const errorRows = useMemo(() => flattenErrors(questions, classReports), [classReports, questions]);
  const allObjectiveQuestions = useMemo(() => questions.filter((item) => (item.option_distribution?.length ?? 0) > 0), [questions]);
  const objectiveQuestions = useMemo(() => {
    if (optionQuestionId === "all") return allObjectiveQuestions;
    if (optionQuestionId === "first-four") return allObjectiveQuestions.slice(0, 4);
    return allObjectiveQuestions.filter(item => item.question_id === optionQuestionId);
  }, [allObjectiveQuestions, optionQuestionId]);
  useEffect(() => { setOptionQuestionId("first-four"); }, [selectedExamId]);

  // 样本不足时隐藏该指标；缺少统计依据不能显示成真实的 0%，否则会误导学习判断。
  const qualityData = useMemo(() => {
    if (!quality) {
      return [];
    }
    return [
      { name: "AI采纳", value: quality.ai_adoption_rate.available ? percentValue(quality.ai_adoption_rate.value) : 0, available: quality.ai_adoption_rate.available },
      {
        name: "人工改分",
        value: quality.human_modification_rate.available ? percentValue(quality.human_modification_rate.value) : 0,
        available: quality.human_modification_rate.available
      },
      { name: "识别失败", value: quality.ocr_failure_rate.available ? percentValue(quality.ocr_failure_rate.value) : 0, available: quality.ocr_failure_rate.available }
    ].filter((item) => item.available);
  }, [quality]);

  const questionColumns = useMemo<TableColumnsType<QuestionAnalysis>>(
    () => [
      { title: "题号", dataIndex: "question_no", width: 80 },
      {
        title: "题型",
        dataIndex: "question_type",
        width: 110,
        render: (value: string) => <span title={value}>{questionTypeLabels[value] ?? "其他题型"}</span>
      },
      { title: "得分率", dataIndex: "score_rate", width: 90, render: (value: number) => formatPercent(value) },
      { title: "区分度", dataIndex: "discrimination", width: 90, render: (value: number) => formatScore(value) },
      {
        title: "知识点",
        dataIndex: "knowledge_points",
        render: (value: string[]) => (value?.length ? value.map((item) => <Tag key={item}>{item}</Tag>) : <span className="muted">未标注</span>)
      }
    ],
    []
  );

  const classColumns = useMemo<TableColumnsType<ClassReport>>(
    () => [
      { title: "班级", dataIndex: "class_name" },
      { title: "人数", dataIndex: "student_count", width: 80 },
      { title: "平均分", dataIndex: ["stats", "average"], width: 90, render: (value: number) => formatScore(value) },
      { title: "中位数", dataIndex: ["stats", "median"], width: 90, render: (value: number) => formatScore(value) },
      { title: "及格率", dataIndex: ["stats", "pass_rate"], width: 90, render: (value: number) => formatPercent(value) },
      { title: "优秀率", dataIndex: ["stats", "excellent_rate"], width: 90, render: (value: number) => formatPercent(value) }
    ],
    []
  );

  const errorColumns = useMemo<TableColumnsType<ErrorRow>>(
    () => [
      { title: "题号", dataIndex: "question_no", width: 80 },
      { title: "来源", dataIndex: "source", width: 110 },
      { title: "错误线索", dataIndex: "text" },
      { title: "次数", dataIndex: "count", width: 80 },
      { title: "得分率", dataIndex: "score_rate", width: 90, render: (value?: number) => formatPercent(value) }
    ],
    []
  );

  const loadExamList = useCallback(async () => {
    setLoadingExams(true);
    setError(null);
    try {
      const result = await listAllExams();
      setExams(result.exams);
      setSelectedExamId((current) => {
        if (initialExamId && result.exams.some((exam) => exam.id === initialExamId)) return initialExamId;
        return result.exams.some((exam) => exam.id === current) ? current : result.exams[0]?.id || "";
      });
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoadingExams(false);
    }
  }, [initialExamId]);

  const loadReports = useCallback(
    async (examId: string) => {
      const requestId = ++reportRequestRef.current;
      if (!examId) {
        setOverview(null);
        setClassReports([]);
        setQuestions([]);
        setQuality(null);
        setLoadingReports(false);
        return;
      }
      setLoadingReports(true);
      setError(null);
      try {
        const [overviewResult, classResult, questionResult, qualityResult] = await Promise.all([
          getReportOverview(examId),
          listClassReports(examId),
          listQuestionReports(examId),
          getGradingQualityReport(examId)
        ]);
        if (requestId !== reportRequestRef.current) return;
        setOverview(overviewResult.overview);
        setClassReports(classResult.classes);
        setQuestions(questionResult.questions);
        setQuality(qualityResult.grading_quality);
      } catch (currentError) {
        if (requestId !== reportRequestRef.current) return;
        setError(formatError(currentError));
        setOverview(null);
        setClassReports([]);
        setQuestions([]);
        setQuality(null);
      } finally {
        if (requestId === reportRequestRef.current) setLoadingReports(false);
      }
    },
    []
  );

  useEffect(() => {
    void loadExamList();
  }, [loadExamList]);

  useEffect(() => {
    if (initialExamId) setSelectedExamId(initialExamId);
  }, [initialExamId]);

  useEffect(() => {
    void loadReports(selectedExamId);
  }, [loadReports, selectedExamId]);

  const refresh = async () => {
    await loadExamList();
    if (selectedExamId) {
      await loadReports(selectedExamId);
    }
  };

  const exportReport = () => {
    if (!selectedExamId) {
      message.error("请先选择考试");
      return;
    }
    modal.confirm({
      title: "导出学情报告",
      content: "将导出该考试的学情报告（CSV 表格文件）。导出文件带追溯水印，导出操作会被系统记录。请确认仅提供给有权查看的人员。",
      okText: "确认导出",
      cancelText: "取消",
      onOk: async () => {
        setExporting(true);
        try {
          const result = await exportLearningReport(selectedExamId);
          const filename = result.filename ?? `exam-${selectedExamId}-report.csv`;
          setLastExport({ filename, watermark: result.watermark });
          saveBlob(result.blob, filename);
          message.success(`学情报告已导出（文件：${filename}）`);
        } catch (currentError) {
          message.error(formatError(currentError));
        } finally {
          setExporting(false);
        }
      }
    });
  };

  const stats = overview?.stats;
  const kpis = [
    { label: "参考人数", value: overview ? String(overview.student_count) : "-" },
    { label: "平均分", value: formatScore(stats?.average) },
    { label: "最高分", value: formatScore(stats?.highest) },
    { label: "最低分", value: formatScore(stats?.lowest) },
    { label: "及格率", value: formatPercent(stats?.pass_rate) },
    { label: "优秀率", value: formatPercent(stats?.excellent_rate) }
  ];

  return (
    <div className="reports-shell">
      <section className="reports-topbar">
        <div>
          <Space align="center" wrap>
            <h1>学情报告</h1>
          </Space>
          <p>先看整体成绩，再定位低分题、薄弱知识点和班级差异。</p>
        </div>
        <Space wrap>
          <Select
            className="reports-exam-select"
            placeholder="选择考试"
            value={selectedExamId || undefined}
            options={exams.map((exam) => ({ value: exam.id, label: `${exam.name} · ${examSubjectLabel(exam.subject)}` }))}
            loading={loadingExams}
            onChange={setSelectedExamId}
          />
          <Button icon={<RefreshCw size={16} />} onClick={refresh} loading={loadingExams || loadingReports}>
            刷新
          </Button>
          <Button type="primary" icon={<Download size={16} />} disabled={!canExportReport} loading={exporting} onClick={exportReport}>
            导出报告
          </Button>
        </Space>
      </section>

      {lastExport?.watermark ? <p className="muted reports-watermark-note">最近导出水印编号：{lastExport.watermark}</p> : null}

      <LearningReportNotices canRead={canRead} error={error} empty={overview?.empty} onRetry={refresh} />

      <div className="reports-view-nav"><Segmented aria-label="报告视图" value={view} options={[{ value: "overview", label: "总览" }, { value: "questions", label: "题目" }, { value: "classes", label: "班级" }, { value: "quality", label: "阅卷质量" }]} onChange={(next) => setView(next as typeof view)} /></div>
      {view === "overview" ? <section className="reports-kpi-strip">
        {kpis.map((item) => (
          <div key={item.label}>
            <span>{item.label}</span>
            <strong>{item.value}</strong>
          </div>
        ))}
      </section> : null}

      {loadingReports ? (
        <LoadingState label="正在读取学情报告" />
      ) : (
        <section className="reports-grid" data-view={view}>
          <ChartPanel className="reports-wide reports-view-overview" title="分数分布" description={selectedExam ? selectedExam.name : "未选择考试"} empty={distributionData.length === 0} emptyTitle="暂无分数分布">
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={distributionData}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="name" />
                <YAxis allowDecimals={false} />
                <ChartTooltip />
                <Bar dataKey="count" name="人数" fill={chartBlue} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </ChartPanel>

          {renderClassComparisonChart(classMetric, classComparisonData, setClassMetric)}

          <ChartPanel className="reports-view-overview reports-view-questions" title="题目得分率" description="得分率越高，整体作答越容易；正确率按获得该题满分的答卷统计。" empty={questionScoreData.length === 0} emptyTitle="暂无题目得分率">
            <ResponsiveContainer width="100%" height={260}>
              <LineChart data={questionScoreData}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="name" />
                <YAxis domain={[0, 100]} />
                <ChartTooltip />
                <Line type="monotone" dataKey="scoreRate" name="得分率%" stroke={chartBlue} strokeWidth={2} dot={{ r: 3 }} />
                <Line type="monotone" dataKey="correctRate" name="正确率%" stroke={chartGreen} strokeWidth={2} dot={{ r: 3 }} />
              </LineChart>
            </ResponsiveContainer>
          </ChartPanel>

          <ChartPanel className="reports-view-overview" title="知识点掌握率" description="各班薄弱知识点汇总，掌握率最低的排在前面。" empty={knowledgeData.length === 0} emptyTitle="暂无知识点数据">
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={knowledgeData.map((item) => ({ name: item.knowledge_point, mastery: percentValue(item.mastery_rate), questions: item.question_count }))}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="name" interval={0} tick={{ fontSize: 11 }} />
                <YAxis domain={[0, 100]} />
                <ChartTooltip />
                <Bar dataKey="mastery" name="掌握率%" fill={chartCyan} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </ChartPanel>

          <section className="reports-panel reports-wide grading-quality-report-panel reports-view-quality">
            <div className="panel-head">
              <div>
                <h2>阅卷质量分析</h2>
                <p>基于本次阅卷过程统计，数据不足的指标不显示。</p>
              </div>
            </div>
            {!quality ? (
              <EmptyState title="暂无阅卷质量" description="暂无阅卷质量数据。" />
            ) : (
              <>
                <div className="reports-quality-strip">
                  <div>
                    <span>AI 采纳率</span>
                    <strong>{formatMetric(quality.ai_adoption_rate)}</strong>
                    <small>{metricDetail(quality.ai_adoption_rate)}</small>
                  </div>
                  <div>
                    <span>人工改分率</span>
                    <strong>{formatMetric(quality.human_modification_rate)}</strong>
                    <small>{metricDetail(quality.human_modification_rate)}</small>
                  </div>
                  <div>
                    <span>平均双评分差</span>
                    <strong>{formatMetric(quality.average_double_mark_diff, "score")}</strong>
                    <small>{metricDetail(quality.average_double_mark_diff)}</small>
                  </div>
                  <div>
                    <span>仲裁数量</span>
                    <strong>{quality.arbitration_count}</strong>
                    <small>共 {quality.double_mark_session_count} 份双评试卷</small>
                  </div>
                  <div>
                    <span>识别失败率</span>
                    <strong>{formatMetric(quality.ocr_failure_rate)}</strong>
                    <small>{metricDetail(quality.ocr_failure_rate)}</small>
                  </div>
                </div>
                {qualityData.length === 0 ? (
                  <EmptyState title="暂无法绘制质量图表" description="阅卷数据样本不足，指标暂不展示。" />
                ) : (
                  <div className="reports-chart compact">
                    <ResponsiveContainer width="100%" height={180}>
                      <BarChart data={qualityData}>
                        <CartesianGrid strokeDasharray="3 3" vertical={false} />
                        <XAxis dataKey="name" />
                        <YAxis domain={[0, 100]} />
                        <ChartTooltip />
                        <Bar dataKey="value" name="比例%" fill={chartAmber} radius={[4, 4, 0, 0]} />
                      </BarChart>
                    </ResponsiveContainer>
                  </div>
                )}
              </>
            )}
          </section>

          <ChartPanel className="reports-view-questions" title="题目区分度" description="区分度为总分高低两组的本题得分率之差，范围 -1 至 1；负值表示低分组在本题得分率更高，需结合题目与样本复核。" empty={questionScoreData.length === 0} emptyTitle="暂无题目质量">
            <ResponsiveContainer width="100%" height={260}>
              <LineChart data={questionScoreData}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="name" />
                <YAxis domain={[-1, 1]} />
                <ChartTooltip />
                <Line type="monotone" dataKey="discrimination" name="区分度" stroke={chartRed} strokeWidth={2} dot={{ r: 3 }} />
              </LineChart>
            </ResponsiveContainer>
          </ChartPanel>

          <section className="reports-panel reports-view-questions">
            <div className="panel-head">
              <div>
                <h2>客观题选项分布</h2>
                <p>统计客观题各选项的作答人数。当前显示 {objectiveQuestions.length} / {allObjectiveQuestions.length} 题。</p>
              </div>
              <Select aria-label="选项分布题目范围" showSearch optionFilterProp="label" style={{ minWidth: 220 }} value={optionQuestionId} onChange={setOptionQuestionId} options={[{ value: "first-four", label: "前 4 道客观题" }, { value: "all", label: `全部 ${allObjectiveQuestions.length} 道客观题` }, ...allObjectiveQuestions.map(item => ({ value: item.question_id, label: `第 ${item.question_no} 题` }))]} />
            </div>
            {objectiveQuestions.length === 0 ? (
              <EmptyState title="暂无选项分布" description="该考试没有客观题或选项作答数据。" />
            ) : (
              <div className="reports-option-list">
                {objectiveQuestions.map((question) => {
                  const total = (question.option_distribution ?? []).reduce((sum, item) => sum + item.count, 0);
                  return (
                    <div className="reports-option-block" key={question.question_id}>
                      <div>
                        <strong>{question.question_no}</strong>
                        <span title={question.question_type}>{questionTypeLabels[question.question_type] ?? "其他题型"}</span>
                      </div>
                      {(question.option_distribution ?? []).map((item) => (
                        <div className="reports-option-row" key={`${question.question_id}-${item.option}`}>
                          <span>{item.option}</span>
                          <div>
                            <i style={{ width: `${total > 0 ? (item.count / total) * 100 : 0}%` }} />
                          </div>
                          <em>{item.count}</em>
                        </div>
                      ))}
                    </div>
                  );
                })}
              </div>
            )}
          </section>

          <section className="reports-panel reports-wide reports-view-questions">
            <div className="panel-head">
              <div>
                <h2>题目明细</h2>
                <p>{questions.length} 道题目分析</p>
              </div>
            </div>
            <ResponsiveTable
              className="dense-data-table"
              rowKey="question_id"
              size="small"
              columns={questionColumns}
              dataSource={questions}
              pagination={{ pageSize: 6 }}
              locale={{ emptyText: <EmptyState title="暂无题目明细" description="暂无题目分析数据。" /> }}
            />
          </section>

          <section className="reports-panel reports-view-classes">
            <div className="panel-head">
              <div>
                <h2>班级明细</h2>
                <p>{classReports.length} 个班级报告</p>
              </div>
            </div>
            <ResponsiveTable
              className="dense-data-table"
              rowKey="class_id"
              size="small"
              columns={classColumns}
              dataSource={classReports}
              pagination={false}
              locale={{ emptyText: <EmptyState title="暂无班级明细" description="暂无班级报告数据。" /> }}
            />
          </section>

          <section className="reports-panel reports-view-overview">
            <div className="panel-head">
              <div>
                <h2>高频错误</h2>
                <p>来自题目错误线索或班级高频错题。</p>
              </div>
            </div>
            <ResponsiveTable
              className="dense-data-table"
              rowKey="key"
              size="small"
              columns={errorColumns}
              dataSource={errorRows}
              pagination={false}
              locale={{ emptyText: <EmptyState title="暂无高频错误" description="暂无高频错误数据。" /> }}
            />
          </section>
        </section>
      )}
    </div>
  );
}
