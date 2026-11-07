import { describe, expect, it } from "vitest";
import type {
  DashboardActiveExam,
  DashboardGradingQuestionProgress,
  DashboardOrganizationStatistics,
  DashboardReviewTeacher,
  DashboardStatistics
} from "../../api/dashboard";
import {
  buildExamProgress,
  buildGradingProgress,
  buildSchoolDashboardModel,
  buildStatusConclusion,
  buildWorkItems,
  gradingEtaText,
  gradingWindowLabel,
  type WorkItemSource
} from "./schoolDashboard.model";

function stats(overrides: Partial<DashboardStatistics> = {}): DashboardStatistics {
  return {
    active_exam_count: 0, collecting_exam_count: 0, pending_review_question_count: 0, pending_review_submission_count: 0,
    pending_arbitration_count: 0, pending_arbitration_submission_count: 0, failed_submission_count: 0,
    unmatched_submission_count: 0, quality_issue_submission_count: 0, finalized_exam_count: 0, ...overrides
  };
}

function exam(id: string, overrides: Partial<DashboardActiveExam> = {}): DashboardActiveExam {
  return {
    id, name: `考试${id}`, subject: "math", status: "collecting", submission_count: 0, failed_count: 0,
    quality_issue_count: 0, unmatched_count: 0, created_at: "2026-10-01T00:00:00Z", ...overrides
  };
}

function question(examId: string, questionId: string, questionNo: string, overrides: Partial<DashboardGradingQuestionProgress> = {}): DashboardGradingQuestionProgress {
  return { exam_id: examId, question_id: questionId, question_no: questionNo, task_count: 10, done_count: 5, remaining_count: 5, recent_done_count: 0, ...overrides };
}

const org: DashboardOrganizationStatistics = {
  active_student_count: 0, grade_count: 0, class_count: 0, teacher_count: 0, grader_count: 0, empty_class_count: 0, unassigned_teacher_count: 0
};
const teacher: DashboardReviewTeacher = {
  user_id: "t", username: "teacher", display_name: "王老师", teaching_subjects: [], assigned_count: 0,
  in_progress_count: 0, returned_count: 0, submitted_count: 0, remaining_count: 0, is_online: false
};

function source(overrides: Partial<WorkItemSource> = {}): WorkItemSource {
  return { statistics: stats(), exams: [], canManageOrganization: false, ...overrides };
}

describe("需要我处理", () => {
  it("按契约顺序排列，数量为 0 或未知的行不出现", () => {
    const rows = buildWorkItems(source({
      statistics: stats({ failed_submission_count: 1, unmatched_submission_count: 2, quality_issue_submission_count: 3, pending_arbitration_count: 4, finalized_exam_count: 1 }),
      exams: [exam("a", { failed_count: 1, unmatched_count: 2, quality_issue_count: 3, pending_arbitration_count: 4, status: "finalized" })],
      appealStatistics: { pending_assignment_count: 5, pending_decision_count: 6 },
      organizationStatistics: { ...org, empty_class_count: 7, unassigned_teacher_count: 8 },
      canManageOrganization: true
    }));
    expect(rows.map(row => row.label)).toEqual([
      "答卷处理失败", "答卷未匹配学生", "图像质量需要处理", "待人工复核",
      "成绩复核申请待分派", "成绩复核待裁决", "成绩待发布", "班级没有学生", "教师未设置任教学科"
    ]);
    expect(rows.map(row => row.level)).toEqual(["blocking", "blocking", "blocking", "blocking", "waiting", "waiting", "releasable", "foundation", "foundation"]);
    expect(buildWorkItems(source({ appealStatistics: { pending_assignment_count: 0, pending_decision_count: 0 } }))).toEqual([]);
  });

  it("只有图像质量问题的考试也进入待办，单场时直达本场答卷页", () => {
    const rows = buildWorkItems(source({
      statistics: stats({ quality_issue_submission_count: 2 }),
      exams: [exam("q/1", { name: "期中数学", quality_issue_count: 2 }), exam("clean")]
    }));
    expect(rows).toEqual([expect.objectContaining({
      label: "图像质量需要处理", count: 2, unit: "份", examLabel: "期中数学", path: "/exams/q%2F1/capture", action: "查看质量问题", level: "blocking"
    })]);
  });

  it("涉及多场时显示数量最多的一场并使用跨考试入口；数量相同时取列表中靠前的一场", () => {
    const multi = buildWorkItems(source({
      statistics: stats({ failed_submission_count: 9 }),
      exams: [exam("a", { name: "甲", failed_count: 2 }), exam("b", { name: "乙", failed_count: 5 }), exam("c", { name: "丙", failed_count: 2 })]
    }));
    expect(multi[0]).toMatchObject({ examLabel: "乙 等 3 场", path: "/capture?issue=failed", count: 9 });
    const tie = buildWorkItems(source({
      statistics: stats({ unmatched_submission_count: 2 }),
      exams: [exam("a", { name: "甲", unmatched_count: 1 }), exam("b", { name: "乙", unmatched_count: 1 })]
    }));
    expect(tie[0]).toMatchObject({ examLabel: "甲 等 2 场", path: "/capture?issue=unmatched" });
  });

  it("待人工复核总用复核入口；旧后端没有每场字段时考试列为“—”", () => {
    const withExam = buildWorkItems(source({ statistics: stats({ pending_arbitration_count: 3 }), exams: [exam("a", { name: "甲", pending_arbitration_count: 3 })] }));
    expect(withExam[0]).toMatchObject({ examLabel: "甲", path: "/arbitration?status=pending", unit: "项" });
    const legacy = buildWorkItems(source({ statistics: stats({ pending_arbitration_count: 3 }), exams: [exam("a", { name: "甲" })] }));
    expect(legacy[0]).toMatchObject({ examLabel: "—", path: "/arbitration?status=pending", count: 3 });
  });

  it("成绩待发布按场计数，单场进本场成绩页，多场进待发布列表", () => {
    const single = buildWorkItems(source({ statistics: stats({ finalized_exam_count: 1 }), exams: [exam("a", { name: "甲", status: "finalized" }), exam("b", { status: "grading" })] }));
    expect(single[0]).toMatchObject({ examLabel: "甲", path: "/exams/a/scores", unit: "场" });
    const multi = buildWorkItems(source({ statistics: stats({ finalized_exam_count: 2 }), exams: [exam("a", { name: "甲", status: "finalized" }), exam("b", { name: "乙", status: "finalized" })] }));
    expect(multi[0]).toMatchObject({ examLabel: "甲 等 2 场", path: "/scores?status=finalized" });
  });

  it("成绩复核行只在返回 appeal_statistics 时出现，考试列为“—”", () => {
    expect(buildWorkItems(source())).toEqual([]);
    const rows = buildWorkItems(source({ appealStatistics: { pending_assignment_count: 0, pending_decision_count: 2 } }));
    expect(rows).toEqual([expect.objectContaining({ label: "成绩复核待裁决", count: 2, unit: "条", examLabel: "—", path: "/appeals", action: "去裁决" })]);
  });

  it("基础数据行只对持有 org:manage 的用户出现", () => {
    const organizationStatistics = { ...org, empty_class_count: 2, unassigned_teacher_count: 1 };
    expect(buildWorkItems(source({ organizationStatistics, canManageOrganization: false }))).toEqual([]);
    expect(buildWorkItems(source({ organizationStatistics, canManageOrganization: true })).map(row => [row.label, row.path, row.unit])).toEqual([
      ["班级没有学生", "/members/students", "个班"], ["教师未设置任教学科", "/members/teachers", "名"]
    ]);
  });
});

describe("状态结论", () => {
  const blockingThree = buildWorkItems(source({
    statistics: stats({ failed_submission_count: 1, quality_issue_submission_count: 2, pending_arbitration_count: 3, finalized_exam_count: 1 }),
    exams: [exam("a", { failed_count: 1, quality_issue_count: 2, pending_arbitration_count: 3, status: "finalized" })]
  }));

  it("有阻断行时按行数计数，取前两行短语并在超过两行时加“等”", () => {
    expect(buildStatusConclusion(blockingThree, { hasWarnings: false })).toEqual({
      tone: "danger", text: "3 件事会卡住出成绩：1 份答卷处理失败，2 份图像质量需要处理等"
    });
    const two = buildWorkItems(source({ statistics: stats({ failed_submission_count: 1, unmatched_submission_count: 1 }) }));
    expect(buildStatusConclusion(two, { hasWarnings: false }).text).toBe("2 件事会卡住出成绩：1 份答卷处理失败，1 份答卷未匹配学生");
  });

  it("没有阻断行时，等待和可发布行合并为琥珀色结论", () => {
    const rows = buildWorkItems(source({ statistics: stats({ finalized_exam_count: 1 }), appealStatistics: { pending_assignment_count: 2, pending_decision_count: 0 } }));
    expect(buildStatusConclusion(rows, { hasWarnings: false })).toEqual({ tone: "warning", text: "有 2 件事等你处理：2 条成绩复核申请待分派，1 场成绩待发布" });
  });

  it("基础数据行不计入结论；没有事项时为绿色，并说明正在阅卷的场数", () => {
    const foundationOnly = buildWorkItems(source({ organizationStatistics: { ...org, empty_class_count: 4 }, canManageOrganization: true }));
    expect(foundationOnly).toHaveLength(1);
    expect(buildStatusConclusion(foundationOnly, { hasWarnings: false })).toEqual({ tone: "success", text: "目前没有卡住出成绩的事" });
    expect(buildStatusConclusion([], { hasWarnings: false, gradingExamCount: 2 }).text).toBe("目前没有卡住出成绩的事，2 场考试正在阅卷");
  });

  it("有告警时追加第二行，原本为绿色的结论改为中性色，红色保持不变", () => {
    expect(buildStatusConclusion([], { hasWarnings: true })).toEqual({
      tone: "neutral", text: "目前没有卡住出成绩的事", note: "部分统计暂时不可用，结论可能不完整。"
    });
    expect(buildStatusConclusion(blockingThree, { hasWarnings: true })).toMatchObject({ tone: "danger", note: "部分统计暂时不可用，结论可能不完整。" });
  });

  it("旧后端没有 grading_exam_count 时按 active_exams 的阅卷中、复核中计数", () => {
    const exams = [exam("a", { status: "grading" }), exam("b", { status: "reviewing" }), exam("c", { status: "collecting" })];
    const legacy = buildSchoolDashboardModel({ ...source({ exams }), hasWarnings: false });
    expect(legacy.status.text).toBe("目前没有卡住出成绩的事，2 场考试正在阅卷");
    const current = buildSchoolDashboardModel({ ...source({ exams, statistics: stats({ grading_exam_count: 1 }) }), hasWarnings: false });
    expect(current.status.text).toBe("目前没有卡住出成绩的事，1 场考试正在阅卷");
  });
});

describe("考试进度", () => {
  it("准备中的考试只计数，不进表格；已发布、已归档不显示；未知状态按进行中排在最后", () => {
    const view = buildExamProgress([
      exam("draft", { status: "draft" }), exam("configured", { status: "configured" }), exam("ready", { status: "ready" }),
      exam("confirmed", { status: "preparation_confirmed" }), exam("published", { status: "published" }), exam("archived", { status: "archived" }),
      exam("mystery", { status: "scanning", created_at: "2026-10-30T00:00:00Z" }), exam("collecting", { status: "collecting" })
    ]);
    expect(view.preparingCount).toBe(4);
    expect(view.rows.map(row => row.exam.id)).toEqual(["collecting", "mystery"]);
    expect(view.rows[1]).toMatchObject({ stepIndex: null, progressText: "—" });
    expect(view.empty).toBeUndefined();
  });

  it("有异常或待人工复核的在前，再按离发布近的在前，再按创建时间倒序，最多 6 行", () => {
    const view = buildExamProgress([
      exam("collect-old", { status: "collecting", created_at: "2026-10-01T00:00:00Z" }),
      exam("collect-new", { status: "collecting", created_at: "2026-10-05T00:00:00Z" }),
      exam("grading", { status: "grading" }),
      exam("finalized", { status: "finalized" }),
      exam("reviewing", { status: "reviewing" }),
      exam("issue", { status: "collecting", unmatched_count: 1 }),
      exam("arbitration", { status: "grading", pending_arbitration_count: 2 }),
      exam("processing", { status: "processing", created_at: "2026-09-01T00:00:00Z" })
    ]);
    expect(view.rows.map(row => row.exam.id)).toEqual(["arbitration", "issue", "finalized", "reviewing", "grading", "collect-new"]);
    expect(view.rows[1]).toMatchObject({ stepIndex: 1, issueCount: 1, needsAttention: true });
  });

  it("当前进度按阶段生成，缺少名册或任务数时不显示分母", () => {
    const text = (overrides: Partial<DashboardActiveExam>) => buildExamProgress([exam("x", overrides)]).rows[0].progressText;
    expect(text({ status: "collecting", submission_count: 12, roster_count: 40, roster_submitted_count: 11 })).toBe("已收 11/40 人");
    expect(text({ status: "collecting", submission_count: 12 })).toBe("已收 12 份");
    expect(text({ status: "collecting", submission_count: 12, roster_count: 0, roster_submitted_count: 0 })).toBe("已收 12 份");
    expect(text({ status: "processing", submission_count: 3, roster_count: null, roster_submitted_count: null })).toBe("已收 3 份");
    expect(text({ status: "grading", review_task_count: 120, review_done_count: 30 })).toBe("已阅 30/120 项");
    expect(text({ status: "reviewing", review_task_count: 120, review_done_count: 120, pending_arbitration_count: 4 })).toBe("已阅 120/120 项，待复核 4 项");
    expect(text({ status: "grading", review_task_count: 0, review_done_count: 0 })).toBe("暂无阅卷任务");
    expect(text({ status: "grading" })).toBe("暂无阅卷任务");
    expect(text({ status: "finalized" })).toBe("阅卷完成，待发布");
  });

  it("空状态区分只有准备中考试和一场都没有", () => {
    expect(buildExamProgress([exam("d", { status: "draft" })])).toMatchObject({ rows: [], preparingCount: 1, empty: { title: "暂无采集中或阅卷中的考试" } });
    expect(buildExamProgress([]).empty).toEqual({ title: "暂无进行中的考试", description: "创建考试后，可从这里直接继续下一步。" });
  });
});

describe("阅卷进度", () => {
  it("预计剩余时间的阈值", () => {
    expect(gradingEtaText(0, 0, 3600)).toBe("已完成");
    expect(gradingEtaText(50, 9, 3600)).toBe("样本不足");
    expect(gradingEtaText(9, 10, 3600)).toBe("不到 1 小时");
    expect(gradingEtaText(10, 10, 3600)).toBe("约 1 小时");
    expect(gradingEtaText(25, 10, 3600)).toBe("约 3 小时");
    expect(gradingEtaText(240, 10, 3600)).toBe("约 24 小时");
    expect(gradingEtaText(241, 10, 3600)).toBe("超过 1 天");
    // 30 分钟时窗内已阅 10 项，速度为每小时 20 项。
    expect(gradingEtaText(30, 10, 1800)).toBe("约 2 小时");
    expect(gradingEtaText(30, 10, 0)).toBe("—");
  });

  it("时窗标签由 window_seconds 生成", () => {
    expect(gradingWindowLabel(3600)).toBe("近 1 小时");
    expect(gradingWindowLabel(1800)).toBe("近 30 分钟");
    expect(gradingWindowLabel(0)).toBeUndefined();
  });

  it("按剩余降序，同数按考试名、题号排列，并补上考试名", () => {
    const view = buildGradingProgress({ window_seconds: 3600, questions: [
      question("a", "q10", "10", { remaining_count: 3 }), question("a", "q2", "2", { remaining_count: 3 }),
      question("b", "q1", "1", { remaining_count: 8, recent_done_count: 12 }), question("missing", "q1", "1", { remaining_count: 0 })
    ] }, [exam("a", { name: "期中数学" }), exam("b", { name: "月考语文" })]);
    expect(view.visible).toBe(true);
    expect(view.windowLabel).toBe("近 1 小时");
    expect(view.questions?.map(row => `${row.examName}/${row.questionNo}`)).toEqual(["月考语文/1", "期中数学/2", "期中数学/10", "—/1"]);
    expect(view.questions?.[0].etaText).toBe("不到 1 小时");
  });

  it("没有剩余任务时整个区块隐藏；来源失败时不显示按题，但教师仍有剩余时区块保留", () => {
    const done = { window_seconds: 3600, questions: [question("a", "q1", "1", { remaining_count: 0, done_count: 10 })] };
    expect(buildGradingProgress(done, [], [teacher]).visible).toBe(false);
    expect(buildGradingProgress(null, [], [{ ...teacher, remaining_count: 2 }])).toEqual({ visible: true, questions: undefined, windowLabel: undefined });
    expect(buildGradingProgress(undefined, [], undefined).visible).toBe(false);
    expect(buildGradingProgress({ window_seconds: 3600, questions: null as unknown as [] }, []).questions).toEqual([]);
  });
});
