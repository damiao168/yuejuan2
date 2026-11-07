import { describe, expect, it } from "vitest";
import type { Exam, ManagementExam } from "../../../api/exams";
import { canEditManagementDefaultClasses, filterManagementExams, isManagementExamArchived, managementDefaultClassesHelp, managementEditPayload, managementExamKey, managementExamRoute, managementExamWorkspaceRoute, managementGradingComplete, managementProgressFilter, managementProgressLabel, managementStatusOptions, type ManagementFilters } from "./managementPresentation";

function subject(id: string, status: string, classIds = ["class-a"]): Exam {
  return { id, tenant_id: "tenant", school_id: "school", name: id, subject: id, exam_type: "midterm_exam", total_score: 150, status,
    grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual_publish", created_by: "admin", class_ids: classIds, revision: id === "math" ? 3 : 4 };
}

function exam(): ManagementExam {
  return { id: "session/1", kind: "session", school_id: "school", grade_id: "grade", name: "期中考试", exam_type: "midterm_exam", revision: 7,
    created_by: "admin", class_ids: ["class-a", "class-b"], default_class_ids: ["class-a"], inherited_subject_ids: ["math"],
    exams: [subject("math", "grading"), subject("physics", "reviewing", ["class-b"])],
    progress: { status: "ongoing", label: "进行中 · 1 科阅卷、1 科复核", total_count: 2, ready_count: 2, published_count: 0, counts: { grading: 1, reviewing: 1 } },
    scope_complete: true, can_manage: true, can_edit: true, can_archive: false, can_restore: false };
}

const filters: ManagementFilters = { search: "", schoolId: "", gradeId: "", status: "" };

describe("整场考试展示与编辑范围", () => {
  it("独立单科按当前阶段直达，多科只授权一科仍保留整场入口", () => {
    const target = exam();
    expect(managementExamWorkspaceRoute({ ...target, kind: "standalone", exams: [subject("math", "grading")] })).toBe("/exams/math/grading");
    expect(managementExamWorkspaceRoute({ ...target, kind: "standalone", exams: [subject("math", "published")] })).toBe("/exams/math/scores");
    const partialSession = { ...target, scope_complete: false, exams: [subject("math", "grading")] };
    expect(managementExamWorkspaceRoute(partialSession)).toBe("/exams/overview/session/session%2F1");
  });
  it("筛选保留原整场对象及全部学科，不从第一科或匹配科目生成操作目标", () => {
    const target = exam();
    const result = filterManagementExams([target], { ...filters, status: "active", gradeId: "grade", search: "期中" }, new Map());
    expect(result).toEqual([target]);
    expect(result[0]).toBe(target);
    expect(result[0].exams).toHaveLength(2);
    expect(filterManagementExams([target], { ...filters, status: "completed" }, new Map())).toEqual([]);
    expect(managementExamRoute(result[0])).toBe("/exams/overview/session/session%2F1");
  });

  it("不同实体类型即使同 ID 也不混为一场，单科考试仍以整场入口查看", () => {
    const target = exam();
    expect(managementExamKey(target)).not.toBe(managementExamKey({ ...target, kind: "standalone" }));
    expect(managementExamRoute({ ...target, kind: "standalone" })).toContain("/standalone/");
  });

  it("名称修改只提交公共名称和全部学科版本，不覆盖独立班级及阅卷设置", () => {
    const target = exam();
    const payload = managementEditPayload(target, { name: "新名称", class_ids: ["class-a"] });
    expect(payload).toEqual({ name: "新名称", expected_revision: 7, expected_subject_revisions: { math: 3, physics: 4 } });
    expect(target.exams[1].class_ids).toEqual(["class-b"]);
  });

  it("默认班级按集合比较，只有实际变更才提交并保留影响范围版本", () => {
    const target = { ...exam(), default_class_ids: ["class-a", "class-c"] };
    expect(managementEditPayload(target, { name: target.name, class_ids: ["class-c", "class-a"] }).class_ids).toBeUndefined();
    expect(managementEditPayload(target, { name: target.name, class_ids: ["class-d"] }).class_ids).toEqual(["class-d"]);
  });

  it.each([true, false])("班级 ID 固定规范排序且不改输入，默认范围存在 %s", (hasDefaultClasses) => {
    // 两种重音写法在部分语言排序中同序，但仍是不同 ID；反向输入不能误报范围变化。
    const ids = ["class-2", "class-10", "class-a", "class-Z", "\u00e9", "e\u0301", "\uFF3A", "\uD855\uDE51"];
    const target: ManagementExam = { ...exam(), class_ids: [...ids], default_class_ids: [...ids] };
    // 当前契约要求数组；删除字段模拟旧响应，继续验证缺省时回退到 class_ids。
    if (!hasDefaultClasses) Reflect.deleteProperty(target, "default_class_ids");
    const selected = [...ids].reverse();
    const payload = managementEditPayload(target, { name: target.name, class_ids: selected });
    expect(payload).toEqual({ expected_revision: 7, expected_subject_revisions: { math: 3, physics: 4 } });
    expect(target.class_ids).toEqual(ids);
    expect(target.default_class_ids).toEqual(hasDefaultClasses ? ids : undefined);
    expect(selected).toEqual([...ids].reverse());
    const changed = selected.filter((id) => id !== "e\u0301");
    expect(managementEditPayload(target, { name: target.name, class_ids: changed }).class_ids).toBe(changed);
  });

  it("班级规范比较保留重复 ID 的差异，不在保存前悄悄去重", () => {
    const target = { ...exam(), default_class_ids: ["class-a", "class-a"] };
    const selected = ["class-a"];
    expect(managementEditPayload(target, { name: target.name, class_ids: selected }).class_ids).toBe(selected);
  });

  it("独立范围学科进入阅卷不冻结草稿学科默认班级", () => {
    const target = exam();
    target.exams[0].status = "draft";
    expect(canEditManagementDefaultClasses(target)).toBe(true);
    target.exams[0].status = "ready";
    expect(canEditManagementDefaultClasses(target)).toBe(false);
  });

  it.each([
    [false, false, "全部学科使用独立参考范围，请进入对应学科修改。"],
    [false, true, "全部学科使用独立参考范围，请进入对应学科修改。"],
    [true, false, "受影响学科进入后续阶段后，参考范围不能修改；冻结名册也会阻止保存。"],
    [true, true, "仅更新继承默认范围的学科，冻结名册会阻止保存。"],
  ] as const)("默认班级提示在影响学科 %s、可编辑 %s 时保留原文字", (hasAffectedSubjects, classesEditable, text) => {
    expect(managementDefaultClassesHelp(hasAffectedSubjects, classesEditable)).toBe(text);
  });

  it("简化列表始终排除整场及历史归档，不改变原考试对象", () => {
    const target = exam();
    const archived = { ...target, archived_at: "2026-10-04T00:00:00Z" };
    expect(filterManagementExams([target, archived], filters, new Map())).toEqual([target]);
    expect(isManagementExamArchived({ ...target, legacy_archived: true, archived_subject_ids: ["math", "physics"] })).toBe(true);
    expect(isManagementExamArchived({ ...target, legacy_archived: true, archived_subject_ids: ["physics"] })).toBe(false);
  });

  it("进度只提供三项，旧细分书签归入对应大类", () => {
    expect(managementStatusOptions.map(item => item.label)).toEqual(["全部进度", "正在进行", "阅卷结束"]);
    for (const status of ["draft", "configured", "ready", "collecting", "grading", "reviewing", "partially_published", "ongoing"]) {
      expect(managementProgressFilter(status)).toBe("active");
    }
    for (const status of ["finalized", "published", "completed"]) expect(managementProgressFilter(status)).toBe("completed");
    expect(managementProgressFilter("archived")).toBe("");
  });

  it("阅卷结束使用逐科去重完成数，部分已发布和复核中不能混入", () => {
    const target = exam();
    const incomplete = { ...target, progress: { ...target.progress, status: "partially_published", published_count: 1, counts: { finalized: 1, reviewing: 1 }, grading_complete_count: 1 } };
    const complete = { ...target, progress: { ...target.progress, status: "partially_published", published_count: 1, counts: { finalized: 1, published: 1 }, grading_complete_count: 2 } };
    expect(managementGradingComplete(incomplete)).toBe(false);
    expect(managementProgressLabel(incomplete)).toBe("正在进行");
    expect(managementProgressLabel(complete)).toBe("阅卷结束");
    expect(filterManagementExams([incomplete, complete], { ...filters, status: "completed" }, new Map())).toEqual([complete]);
    expect(filterManagementExams([incomplete, complete], { ...filters, status: "active" }, new Map())).toEqual([incomplete]);
    expect(complete.progress.status).toBe("partially_published");
  });

  it("兼容旧响应时不把未证实的发布状态或重叠计数解释为结束", () => {
    const target = exam();
    expect(managementGradingComplete({ ...target, progress: { ...target.progress, published_count: 1, counts: { finalized: 1, reviewing: 1 } } })).toBe(false);
    expect(managementGradingComplete({ ...target, progress: { ...target.progress, counts: { published: 2 } } })).toBe(false);
    expect(managementGradingComplete({ ...target, progress: { ...target.progress, counts: { finalized: 2 } } })).toBe(false);
    expect(managementGradingComplete({ ...target, progress: { ...target.progress, published_count: 2, counts: { draft: 2 } } })).toBe(true);
    expect(managementGradingComplete({ ...target, progress: { ...target.progress, total_count: 0, grading_complete_count: 0 } })).toBe(false);
    expect(managementProgressLabel({ ...target, progress: { ...target.progress, status: "unavailable" } })).toBe("进度待核查");
  });
});
