import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { TableProps } from "antd";
import type { ColumnType } from "antd/es/table";
import { describe, expect, it, vi } from "vitest";
import type { Exam, ManagementExam } from "../../../api/exams";
import { ManagementSubjectTable } from "./ManagementExamDetails";

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
  it("准备情况保留草稿、后续阶段与未知状态的原文字", () => {
    const cases = [
      ["draft", "尚未确认准备完成"], ["configured", "尚未确认准备完成"],
      ...["ready", "collecting", "grading", "reviewing", "finalized", "published"].map((status) => [status, "已进入准备完成或后续阶段"]),
      ["unknown", "暂无法获取"],
    ];
    const record = exam(cases.map(([status]) => subject(status)));
    const preparation = column(table(record), "准备情况");
    for (const [status, label] of cases) {
      expect(preparation.render!(undefined, subject(status), 0)).toBe(label);
    }
    const html = renderToStaticMarkup(<ManagementSubjectTable exam={record} />);
    expect(html).toContain("尚未确认准备完成");
    expect(html).toContain("已进入准备完成或后续阶段");
    expect(html).toContain("暂无法获取");
  });

  it("事实不可用时历史归档优先，其余学科不能推断准备完成", () => {
    const record = exam([subject("archived"), subject("ready")], false);
    const preparation = column(table(record), "准备情况");
    expect(preparation.render!(undefined, record.exams[0], 0)).toBe("历史归档，原阶段未知");
    expect(preparation.render!(undefined, record.exams[1], 1)).toBe("准备状态暂不可用");
    const html = renderToStaticMarkup(<ManagementSubjectTable exam={record} />);
    expect(html).toContain("历史归档，原阶段未知");
    expect(html).toContain("准备状态暂不可用");
    expect(html).not.toContain("已进入准备完成或后续阶段");
  });

  it.each([
    ["draft", true, "查看学科"], ["archived", false, "查看学科"],
    ["configured", false, "准备配置"], ["ready", false, "导入与核对答题卡"], ["grading", false, "继续阅卷"], ["published", false, "查看成绩"],
  ] as const)("%s 学科的归档标记 %s 保留按钮文字与原对象回调", (status, archived, label) => {
    const target = subject(status);
    const record = exam([target]);
    if (archived) record.archived_at = "2026-10-05T00:00:00Z";
    const open = vi.fn();
    const button = column(table(record, open), "操作").render!(undefined, target, 0) as ReactElement<{ onClick: () => void }>;
    expect(renderToStaticMarkup(button)).toContain(label);
    expect(open).not.toHaveBeenCalled();
    button.props.onClick();
    expect(open.mock.calls[0][0]).toBe(target);
  });
});
