import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Descriptions,
  Dropdown,
  Drawer,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
  type MenuProps,
  type SelectProps,
  type TableColumnsType
} from "antd";
import { Archive, Eye, FileClock, LayoutDashboard, MoreHorizontal, Pencil, RefreshCw, Save, Search } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import type { SessionUser } from "../auth/session";
import {
  archiveExam,
  getExam,
  listExams,
  updateExam,
  updateExamStatus,
  type Exam,
  type ExamPayload
} from "../api/exams";
import { listClasses, listGrades, listSchools, type Grade, type School, type SchoolClass } from "../api/org";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { examStatusLabels, examStatusTone, examSubjectOptions } from "../constants/examStatus";
import { examTypeOptions } from "../constants/examCatalog";
import type { ProductExperience } from "../router/experience";
import { hashQueryParam } from "../router/query";
import { groupExams, selectedGroupExam, type ExamGroup } from "../features/exams/examGroups";

const statusFlow = ["draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized", "published", "archived"];

const advanceLabels: Record<string, string> = {
  draft: "开始配置",
  collecting: "结束采集，进入阅卷",
  grading: "进入复核",
  reviewing: "定稿成绩"
};

const subjectOptions = examSubjectOptions;

const gradingModeOptions = [
  { label: "仅客观题自动", value: "auto_objective_only" },
  { label: "AI 辅助 + 人工确认", value: "ai_assisted" },
  { label: "必须人工复核", value: "human_review_required" },
  { label: "双评", value: "double_mark" },
  { label: "盲双评", value: "blind_double_mark" }
];

const publishPolicyOptions = [
  { label: "管理员审批后发布", value: "after_admin_approval" },
  { label: "阅卷完成后手动发布", value: "manual_publish" },
  { label: "成绩确认后自动发布", value: "after_grade_confirmation" }
];

interface ExamFormValues {
  school_id: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  class_ids: string[];
}

interface Filters {
  search: string;
  schoolId: string;
  gradeId: string;
  status: string;
  examType: string;
}

function labelFrom(options: { label: string; value: string }[], value: string) {
  return options.find((item) => item.value === value)?.label ?? "其他类型";
}

// 准备确认、开始采集和成绩发布有独立校验流程，列表不直接跨过这三个状态边界。
function nextStatus(status: string) {
  if (status === "configured" || status === "ready" || status === "finalized") {
    return null;
  }
  const index = statusFlow.indexOf(status);
  return index >= 0 && index < statusFlow.length - 1 ? statusFlow[index + 1] : null;
}

function isLocked(status: string) {
  return status === "published" || status === "archived";
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error(`考试操作失败：${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatTime(value?: string) {
  if (!value) {
    return "暂无记录";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function ExamManagementPage({ mode, canManage, currentUser, onOpenWorkspace, onCreateExam }: { mode: ProductExperience; canManage: boolean; currentUser: SessionUser; onOpenWorkspace: (exam: Exam) => void; onCreateExam: () => void }) {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<ExamFormValues>();
  const watchedSchoolId = Form.useWatch("school_id", form);
  const watchedGradingMode = Form.useWatch("grading_mode", form);
  const [filters, setFilters] = useState<Filters>(() => {
    const status = hashQueryParam("status");
    return { search: "", schoolId: "", gradeId: "", status: status === "active" || statusFlow.includes(status) ? status : "", examType: "" };
  });
  const [exams, setExams] = useState<Exam[]>([]);
  const [schools, setSchools] = useState<School[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [drawer, setDrawer] = useState<{ mode: "edit"; exam: Exam } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailExam, setDetailExam] = useState<Exam | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [actioningId, setActioningId] = useState<string | null>(null);
  const canWrite = canManage;
  const teacherMode = mode === "teacher";

  const loadData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const allExams: Exam[] = [];
      let cursor: string | undefined;
      do {
        const result = await listExams({ school_id: teacherMode ? undefined : filters.schoolId || undefined, limit: 200, cursor });
        allExams.push(...result.exams);
        cursor = result.has_more ? result.next_cursor : undefined;
      } while (cursor);
      setExams(allExams);
      if (teacherMode) {
        setSchools([]);
        setGrades([]);
        setClasses([]);
      } else {
        const [schoolResult, gradeResult, classResult] = await Promise.all([
          listSchools(),
          listGrades(filters.schoolId || undefined),
          listClasses()
        ]);
        setSchools(schoolResult.schools);
        setGrades(gradeResult.grades);
        setClasses(classResult.classes);
      }
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoading(false);
    }
  }, [filters.schoolId, teacherMode]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  useEffect(() => {
    if (!drawer) {
      return;
    }
    form.setFieldsValue({
      school_id: drawer.exam.school_id,
      name: drawer.exam.name,
      subject: drawer.exam.subject,
      exam_type: drawer.exam.exam_type,
      total_score: drawer.exam.total_score,
      grading_mode: drawer.exam.grading_mode,
      appeal_enabled: drawer.exam.appeal_enabled,
      publish_policy: drawer.exam.publish_policy,
      class_ids: drawer.exam.class_ids
    });
  }, [drawer, form]);

  const schoolById = useMemo(() => new Map(schools.map((school) => [school.id, school])), [schools]);
  const gradeById = useMemo(() => new Map(grades.map((grade) => [grade.id, grade])), [grades]);
  const classById = useMemo(() => new Map(classes.map((schoolClass) => [schoolClass.id, schoolClass])), [classes]);

  const gradeOptions = useMemo(() => grades.map((grade) => ({ label: grade.name, value: grade.id })), [grades]);
  const schoolOptions = useMemo(() => schools.map((school) => ({ label: school.name, value: school.id })), [schools]);

  const classOptions = useMemo<SelectProps["options"]>(() => {
    const availableClasses = classes.filter((schoolClass) => !watchedSchoolId || schoolClass.school_id === watchedSchoolId);
    const grouped = grades
      .map((grade) => ({
        label: grade.name,
        options: availableClasses
          .filter((schoolClass) => schoolClass.grade_id === grade.id)
          .map((schoolClass) => ({ label: `${schoolClass.name} (${schoolClass.code})`, value: schoolClass.id }))
      }))
      .filter((group) => group.options.length > 0);
    const ungrouped = availableClasses
      .filter((schoolClass) => !gradeById.has(schoolClass.grade_id))
      .map((schoolClass) => ({ label: `${schoolClass.name} (${schoolClass.code})`, value: schoolClass.id }));
    return ungrouped.length > 0 ? [...grouped, { label: "未关联年级", options: ungrouped }] : grouped;
  }, [classes, gradeById, grades, watchedSchoolId]);

  const gradeNamesForExam = useCallback(
    (exam: Exam) => {
      const gradeNames = new Set<string>();
      for (const classId of exam.class_ids) {
        const schoolClass = classById.get(classId);
        const grade = schoolClass ? gradeById.get(schoolClass.grade_id) : undefined;
        if (grade) {
          gradeNames.add(grade.name);
        }
      }
      return gradeNames.size > 0 ? Array.from(gradeNames).join("、") : "未关联年级";
    },
    [classById, gradeById]
  );

  const filteredGroups = useMemo(() => {
    const keyword = filters.search.trim().toLowerCase();
    return groupExams(exams).filter((group) => {
      const keywordMatched = !keyword || group.name.toLowerCase().includes(keyword);
      const typeMatched = !filters.examType || group.examType === filters.examType;
      return keywordMatched && typeMatched && group.exams.some((exam) => {
        const statusMatched = !filters.status || (filters.status === "active"
          ? !["published", "archived"].includes(exam.status) : exam.status === filters.status);
        const gradeMatched = !filters.gradeId || group.gradeId === filters.gradeId
          || exam.class_ids.some((classId) => classById.get(classId)?.grade_id === filters.gradeId);
        return statusMatched && gradeMatched;
      });
    });
  }, [classById, exams, filters.examType, filters.gradeId, filters.search, filters.status]);

  const examForGroup = (group: ExamGroup) => selectedGroupExam(group, undefined, "", filters.status);

  const openDetail = async (exam: Exam) => {
    setDetailOpen(true);
    setDetailExam(null);
    setDetailError(null);
    setDetailLoading(true);
    try {
      const result = await getExam(exam.id);
      setDetailExam(result.exam);
    } catch (currentError) {
      setDetailError(formatError(currentError));
    } finally {
      setDetailLoading(false);
    }
  };

  const submitForm = async () => {
    const values = await form.validateFields();
    const payload: ExamPayload = {
      school_id: values.school_id || drawer?.exam.school_id || "",
      name: values.name || drawer?.exam.name || "",
      subject: values.subject || drawer?.exam.subject || "",
      exam_type: values.exam_type || drawer?.exam.exam_type || "",
      total_score: values.total_score,
      grading_mode: values.grading_mode,
      appeal_enabled: values.appeal_enabled,
      publish_policy: values.publish_policy,
      class_ids: values.class_ids
    };
    setSubmitting(true);
    try {
      if (drawer?.exam) {
        await updateExam(drawer.exam.id, { ...payload, expected_revision: drawer.exam.revision });
        message.success("考试已更新");
      }
      setDrawer(null);
      await loadData();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setSubmitting(false);
    }
  };

  const changeStatus = (exam: Exam, status: string) => {
    modal.confirm({
      title: exam.exam_session_id ? "确认推进学科状态" : "确认推进考试状态",
      content: `将“${exam.name}”推进到“${examStatusLabels[status] ?? "下一阶段"}”。`,
      okText: "确认",
      cancelText: "取消",
      onOk: async () => {
        setActioningId(exam.id);
        try {
          await updateExamStatus(exam.id, status, exam.revision);
          message.success("状态已更新");
          await loadData();
        } catch (currentError) {
          message.error(formatError(currentError));
        } finally {
          setActioningId(null);
        }
      }
    });
  };

  const archive = (exam: Exam) => {
    modal.confirm({
      title: exam.exam_session_id ? "确认归档学科" : "确认归档考试",
      content: `归档后“${exam.name}”将不能继续编辑核心配置。`,
      okText: "归档",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        setActioningId(exam.id);
        try {
          await archiveExam(exam.id, exam.revision);
          message.success("考试已归档");
          await loadData();
        } catch (currentError) {
          message.error(formatError(currentError));
        } finally {
          setActioningId(null);
        }
      }
    });
  };

  const columns: TableColumnsType<ExamGroup> = [
    {
      title: "考试名称",
      dataIndex: "name",
      width: 250,
      ellipsis: true,
      render: (value: string, group) => <Tooltip title={value}><button className="exam-name-link" type="button" onClick={() => onOpenWorkspace(examForGroup(group))}>{value}</button></Tooltip>
    },
    {
      title: "考试类型",
      dataIndex: "examType",
      width: 102,
      render: (value: string) => labelFrom(examTypeOptions, value)
    },
    { title: "年级", width: 95, ellipsis: true, render: (_, group) => group.gradeId ? gradeById.get(group.gradeId)?.name ?? gradeNamesForExam(examForGroup(group)) : gradeNamesForExam(examForGroup(group)) },
    { title: "班级", width: 95, render: (_, group) => {
      const classIds = [...new Set(group.exams.flatMap((exam) => exam.class_ids))];
      const names = classIds.map((id) => classById.get(id)?.name).filter(Boolean).join("、");
      return <Tooltip title={names || "暂无班级信息"}>{classIds.length} 个班级</Tooltip>;
    } },
    { title: "状态", width: 108, render: (_, group) => {
      const status = examForGroup(group).status;
      const sameStatus = group.exams.every((exam) => exam.status === status);
      return <StatusTag tone={sameStatus ? examStatusTone(status) : "processing"}>{sameStatus ? examStatusLabels[status] ?? "未知状态" : "配置进行中"}</StatusTag>;
    } },
    {
      title: "操作",
      width: 140,
      render: (_, group) => {
        const exam = examForGroup(group);
        const next = nextStatus(exam.status);
        const locked = isLocked(exam.status);
        const moreItems: MenuProps["items"] = [
          { key: "detail", label: exam.exam_session_id ? "查看学科详情" : "查看详情", icon: <Eye size={14} /> },
          ...(canWrite && next && next !== "archived"
            ? [{ key: "advance", label: advanceLabels[exam.status] ?? `推进到${examStatusLabels[next] ?? "下一阶段"}` }]
            : []),
          ...(canWrite ? [{ key: "edit", label: exam.exam_session_id ? "编辑学科设置" : "编辑考试", icon: <Pencil size={14} />, disabled: locked }] : []),
          ...(canWrite && exam.status !== "archived" ? [{ key: "archive", label: exam.exam_session_id ? "归档当前学科" : "归档考试", icon: <Archive size={14} />, danger: true }] : [])
        ];
        return (
          <Space className="table-actions exam-table-actions" size={6}>
            <Button size="small" icon={<LayoutDashboard size={14} />} onClick={() => onOpenWorkspace(exam)}>
              工作区
            </Button>
            <Dropdown
              trigger={["click"]}
              menu={{
                items: moreItems,
                onClick: ({ key }) => {
                  if (key === "detail") void openDetail(exam);
                  if (key === "advance" && next) changeStatus(exam, next);
                  if (key === "edit") setDrawer({ mode: "edit", exam });
                  if (key === "archive") archive(exam);
                }
              }}
            >
              <Button size="small" loading={actioningId === exam.id} aria-label={`${group.name}更多操作`} icon={<MoreHorizontal size={15} />} />
            </Dropdown>
          </Space>
        );
      }
    }
  ];

  const formDisabled = !canWrite || (drawer?.exam ? isLocked(drawer.exam.status) : false);

  return (
    <div className="page-stack">
      <section className="page-heading">
        <div>
          <h1>{teacherMode ? "我的考试" : "考试管理"}</h1>
          <p>{teacherMode ? "查看已授权考试，并进入试卷、阅卷、成绩和学情工作区。" : "创建考试、维护班级范围、推进考试状态和进入后续配置流程。"}</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} onClick={() => void loadData()} loading={loading}>
            刷新
          </Button>
          {canWrite ? <Button type="primary" onClick={onCreateExam}>
            新建考试
          </Button> : null}
        </Space>
      </section>

      <section className="workspace-section filter-panel">
        <div className="filter-grid exam-filter-primary">
          <Input
            prefix={<Search size={16} />}
            placeholder="搜索考试名称"
            value={filters.search}
            onChange={(event) => setFilters((current) => ({ ...current, search: event.target.value }))}
          />
          <Select
            placeholder="状态"
            allowClear
            value={filters.status || undefined}
            options={[
              { label: "进行中", value: "active" },
              ...statusFlow.map((status) => ({ label: examStatusLabels[status] ?? "未知状态", value: status }))
            ]}
            onChange={(value) => setFilters((current) => ({ ...current, status: value ?? "" }))}
          />
        </div>
        {!teacherMode ? <details className="advanced-filter-disclosure">
          <summary>更多筛选</summary>
          <div className="filter-grid">
            <Select placeholder="学校" allowClear value={filters.schoolId || undefined} options={schoolOptions} onChange={(value) => setFilters((current) => ({ ...current, schoolId: value ?? "", gradeId: "" }))} />
            <Select placeholder="年级" allowClear value={filters.gradeId || undefined} options={gradeOptions} onChange={(value) => setFilters((current) => ({ ...current, gradeId: value ?? "" }))} />
            <Select placeholder="考试类型" allowClear value={filters.examType || undefined} options={examTypeOptions} onChange={(value) => setFilters((current) => ({ ...current, examType: value ?? "" }))} />
          </div>
        </details> : null}
      </section>

      {loading ? (
        <section className="workspace-section">
          <LoadingState label="正在加载考试列表" />
        </section>
      ) : error ? (
        <ErrorState message={error} onRetry={() => void loadData()} />
      ) : (
        <section className="workspace-section exam-list-section">
          <div className="section-head">
            <div>
              <h2>{teacherMode ? "已授权考试" : "考试列表"}</h2>
            </div>
          </div>
          <div role="region" aria-label="考试列表表格" tabIndex={0}>
            <Table<ExamGroup>
              rowKey="id"
              dataSource={filteredGroups}
              columns={columns}
              pagination={{ pageSize: 10, showSizeChanger: false, showTotal: (total) => `共 ${total} 场考试` }}
              locale={{ emptyText: <EmptyState title="暂无考试" description={canWrite ? "没有符合条件的考试。试试调整筛选条件，或点击右上角“新建考试”。" : "没有符合条件的考试，请联系管理员为你授权。"} /> }}
              size="small"
              className="exam-management-table"
              tableLayout="fixed"
              scroll={{ x: 760 }}
            />
          </div>
        </section>
      )}

      <Drawer
        title={drawer?.exam.exam_session_id ? "编辑学科设置" : "编辑考试"}
        open={Boolean(drawer)}
        onClose={() => setDrawer(null)}
        width={720}
        destroyOnClose
        extra={
          <Space>
            <Button type="primary" icon={<Save size={16} />} loading={submitting} disabled={formDisabled} onClick={() => void submitForm()}>
              保存
            </Button>
          </Space>
        }
      >
        {formDisabled && drawer?.exam ? (
          <Alert type="info" showIcon message="当前考试状态不允许编辑核心配置" className="drawer-alert" />
        ) : null}
        {drawer?.exam.exam_session_id ? <p className="exam-edit-context">{drawer.exam.exam_session_name} · {labelFrom(subjectOptions, drawer.exam.subject)}</p> : null}
        <Form form={form} layout="vertical" disabled={formDisabled} preserve={false}>
          <div className="form-grid">
            <Form.Item hidden={Boolean(drawer?.exam.exam_session_id)} label="考试名称" name="name" rules={[{ required: true, message: "请输入考试名称" }]}>
              <Input placeholder="高二物理期末考试" />
            </Form.Item>
            <Form.Item hidden={Boolean(drawer?.exam.exam_session_id)} label="学校" name="school_id" rules={[{ required: true, message: "请选择学校" }]}>
              <Select options={schoolOptions} placeholder="选择学校" />
            </Form.Item>
            <Form.Item hidden={Boolean(drawer?.exam.exam_session_id)} label="学科" name="subject" rules={[{ required: true, message: "请选择学科" }]}>
              <Select options={subjectOptions} placeholder="选择学科" />
            </Form.Item>
            <Form.Item hidden={Boolean(drawer?.exam.exam_session_id)} label="考试类型" name="exam_type" rules={[{ required: true, message: "请选择考试类型" }]}>
              <Select options={examTypeOptions} placeholder="选择考试类型" />
            </Form.Item>
            <Form.Item label="总分" name="total_score" rules={[{ required: true, message: "请输入总分" }]}>
              <InputNumber min={1} max={1000} precision={1} className="full-width-control" />
            </Form.Item>
          </div>
          <Form.Item
            label="选择班级"
            name="class_ids"
            rules={[
              { required: true, message: "请选择至少一个班级" },
              { type: "array", min: 1, message: "请选择至少一个班级" }
            ]}
          >
            <Select mode="multiple" options={classOptions} placeholder="按年级选择班级" optionFilterProp="label" />
          </Form.Item>
          <details className="exam-create-advanced" open>
            <summary>阅卷与发布设置</summary>
            <div className="form-grid">
              <Form.Item
                label="阅卷模式"
                name="grading_mode"
                rules={[{ required: true, message: "请选择阅卷模式" }]}
                extra={watchedGradingMode === "double_mark" || watchedGradingMode === "blind_double_mark" ? "双评需在阅卷环节按题启用。" : undefined}
              >
                <Select options={gradingModeOptions} placeholder="选择阅卷模式" />
              </Form.Item>
              <Form.Item label="成绩发布策略" name="publish_policy" rules={[{ required: true, message: "请选择发布策略" }]}>
                <Select options={publishPolicyOptions} placeholder="选择发布策略" />
              </Form.Item>
              <Form.Item label="允许成绩申诉" name="appeal_enabled" valuePropName="checked">
                <Switch />
              </Form.Item>
            </div>
          </details>
        </Form>
      </Drawer>

      <Drawer title="考试详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={680}>
        {detailLoading ? (
          <LoadingState label="正在读取考试详情" />
        ) : detailError ? (
          <ErrorState message={detailError} />
        ) : detailExam ? (
          <div className="detail-stack">
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="考试名称">{detailExam.name}</Descriptions.Item>
              <Descriptions.Item label="学校">{schoolById.get(detailExam.school_id)?.name ?? "本校"}</Descriptions.Item>
              <Descriptions.Item label="学科">{labelFrom(subjectOptions, detailExam.subject)}</Descriptions.Item>
              <Descriptions.Item label="考试类型">{labelFrom(examTypeOptions, detailExam.exam_type)}</Descriptions.Item>
              <Descriptions.Item label="年级">{gradeNamesForExam(detailExam)}</Descriptions.Item>
              <Descriptions.Item label="班级数量">{detailExam.class_ids.length}</Descriptions.Item>
              <Descriptions.Item label="总分">{detailExam.total_score}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <StatusTag tone={examStatusTone(detailExam.status)}>{examStatusLabels[detailExam.status] ?? "未知状态"}</StatusTag>
              </Descriptions.Item>
              <Descriptions.Item label="阅卷模式">{labelFrom(gradingModeOptions, detailExam.grading_mode)}</Descriptions.Item>
              <Descriptions.Item label="允许申诉">{detailExam.appeal_enabled ? "是" : "否"}</Descriptions.Item>
              <Descriptions.Item label="成绩发布策略">{labelFrom(publishPolicyOptions, detailExam.publish_policy)}</Descriptions.Item>
              <Descriptions.Item label="创建人">{detailExam.created_by === currentUser.id ? currentUser.name : "本校管理员"}</Descriptions.Item>
              <Descriptions.Item label="创建时间">{formatTime(detailExam.created_at)}</Descriptions.Item>
            </Descriptions>

            {!teacherMode ? (
              <Button icon={<FileClock size={16} />} onClick={() => (window.location.hash = "/audit")}>
                查看操作日志
              </Button>
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
