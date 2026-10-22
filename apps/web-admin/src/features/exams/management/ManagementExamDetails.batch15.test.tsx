import type { MouseEvent, ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { TableProps } from "antd";
import type { ColumnType } from "antd/es/table";
import { describe, expect, it, vi } from "vitest";
import type { Exam, ManagementExam } from "../../../api/exams";
import { ManagementExamDetails, ManagementSubjectTable } from "./ManagementExamDetails";

function subject(status: string): Exam {
  return {
    id: `subject-${status}`, tenant_id: "tenant-15", school_id: "school-15", name: "数学", subject: "mathematics",
    exam_type: "midterm_exam", total_score: 150, status, grading_mode: "ai_assisted", appeal_enabled: true,
    publish_policy: "manual_publish", created_by: "teacher-15", class_ids: ["class-15"], revision: 4,
  };
}

function exam(exams: Exam[], factsAvailable = true): ManagementExam {
  return {
    id: "session-15", kind: "session", school_id: "school-15", grade_id: "grade-15", name: "期中考试", exam_type: "midterm_exam",
    revision: 3, created_by: "teacher-15", class_ids: ["class-15"], default_class_ids: ["class-15"], inherited_subject_ids: exams.map((item) => item.id), exams,
    progress: { status: "ongoing", label: "进行中", total_count: exams.length, ready_count: 0, published_count: 0, counts: {}, facts_available: factsAvailable },
    scope_complete: true, can_manage: true, can_edit: true, can_archive: false, can_restore: false,
  };
}

function table(record: ManagementExam, onOpenSubject?: (target: Exam) => void) {
  return ManagementSubjectTable({ exam: record, onOpenSubject }) as ReactElement<TableProps<Exam>>;
}

function column(target: ReactElement<TableProps<Exam>>, title: string) {
  return target.props.columns!.find((item) => item.title === title) as ColumnType<Exam>;
}

describe("考试学科表展示边界", () => {
  it("只保留当前阶段，删除重复准备情况和操作列", () => {
    const cases = [
      ["draft", "草稿"], ["configured", "配置中"], ["ready", "准备完成"], ["grading", "阅卷中"],
      ["published", "已发布"], ["archived", "已归档"], ["unknown", "暂无法获取进度"],
    ];
    const record = exam(cases.map(([status]) => subject(status)));
    expect(table(record, vi.fn()).props.columns?.map(item => item.title)).toEqual(["学科", "满分", "参考班级", "当前阶段"]);
    const stage = column(table(record), "当前阶段");
    for (const [status, label] of cases) {
      expect(renderToStaticMarkup(stage.render!(undefined, subject(status), 0) as ReactElement)).toContain(label);
    }
  });

  it("考试信息移除学校，保留年级和归档事实", () => {
    const record = { ...exam([subject("archived")], false), archived_at: "2026-10-05T00:00:00Z", archive_reason: "学年结束" };
    const html = renderToStaticMarkup(<ManagementExamDetails exam={record} layout="columns" gradeName="高一年级" />);
    expect(html).not.toContain(">学校<");
    expect(html).toContain("高一年级");
    expect(html).toContain("归档时间");
    expect(html).toContain("学年结束");
    expect(html).toContain("业务内容只读");
  });

  it.each([
    ["draft", true], ["archived", false], ["configured", false], ["ready", false], ["grading", false], ["published", false],
  ] as const)("%s 学科的归档标记 %s 下整行和名称都传递原学科对象", (status, archived) => {
    const target = subject(status);
    const record = exam([target]);
    if (archived) record.archived_at = "2026-10-05T00:00:00Z";
    const open = vi.fn();
    const element = table(record, open);
    const button = column(element, "学科").render!(target.subject, target, 0) as ReactElement<{ onClick: (event: MouseEvent<HTMLButtonElement>) => void }>;
    expect(button.type).toBe("button");
    expect(renderToStaticMarkup(button)).toContain("数学");
    expect(open).not.toHaveBeenCalled();
    element.props.onRow?.(target, 0).onClick?.({} as MouseEvent<HTMLTableRowElement>);
    const stopPropagation = vi.fn();
    button.props.onClick({ stopPropagation } as unknown as MouseEvent<HTMLButtonElement>);
    expect(stopPropagation).toHaveBeenCalledOnce();
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[0][0]).toBe(target);
    expect(open.mock.calls[1][0]).toBe(target);
  });

  it("没有学科导航回调的详情保持只读表格", () => {
    const target = subject("draft");
    const element = table(exam([target]));
    expect(element.props.onRow).toBeUndefined();
    expect(element.props.rowClassName).toBeUndefined();
    expect(column(element, "学科").render!(target.subject, target, 0)).toBe("数学");
  });
});
