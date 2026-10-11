import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ColumnType } from "antd/es/table";
import { describe, expect, it, vi } from "vitest";
import type { RosterEntry, SubmissionGrade } from "../../api/scores";
import { createRosterColumns, createScoreColumns } from "./scoreColumns";
import type { ScoreIdentityMaps } from "./hooks/useScoreWorkspaceData";

const entry: RosterEntry = {
  key: "student:1", student_id: "student-1", student_name: "测试学生", student_no: "123",
  status: "unmatched", resolution_code: "missing_submission", expected_page_count: 2, actual_page_count: 1
};
const identities: ScoreIdentityMaps = {
  students: { "student-1": { id: "student-1", tenant_id: "tenant", school_id: "school", class_id: "class-1", student_no: "123", name: "测试学生", status: "active" } },
  classes: { "class-1": { id: "class-1", tenant_id: "tenant", school_id: "school", grade_id: "grade", name: "测试班级", code: "1", status: "active" } }
};

function renderCell<T>(column: ColumnType<T>, value: unknown, record: T) {
  return renderToStaticMarkup(<>{column.render?.(value, record, 0) as ReactNode}</>);
}

describe("score table presentation", () => {
  it.each([
    [false, false, entry],
    [true, true, entry],
    [true, false, { ...entry, student_id: undefined }],
    [true, false, { ...entry, key: "submission:1" }]
  ] as const)("does not offer an attendance action outside an editable roster row", (canWrite, publishedOrLocked, record) => {
    const columns = createRosterColumns({ canWrite, publishedOrLocked, openAttendanceEditor: vi.fn() });
    expect(renderCell(columns[5] as ColumnType<RosterEntry>, undefined, record)).toBe("-");
  });

  it("retains absence restoration, missing-paper marking and other-row guidance", () => {
    const columns = createRosterColumns({ canWrite: true, publishedOrLocked: false, openAttendanceEditor: vi.fn() });
    const action = columns[5] as ColumnType<RosterEntry>;
    expect(renderCell<RosterEntry>(action, undefined, { ...entry, status: "absent" })).toContain("恢复应考");
    expect(renderCell(action, undefined, entry)).toContain("标记缺考");
    expect(renderCell(action, undefined, { ...entry, resolution_code: "missing_pages" })).toContain("请先处理答卷");
  });

  it("只读名册可按答卷查看权限定位处理，但不能借阅卷权限打开采集对象", () => {
    const options = { canWrite: false, publishedOrLocked: true, openAttendanceEditor: vi.fn(), openSubmission: vi.fn(), canOpenSubmission: (record: RosterEntry) => record.resolution_code === "grading_incomplete" };
    const action = createRosterColumns(options)[5] as ColumnType<RosterEntry>;
    expect(renderCell(action, undefined, { ...entry, resolution_code: "grading_incomplete" })).toContain("查看阅卷任务");
    expect(renderCell(action, undefined, { ...entry, resolution_code: "missing_pages" })).not.toContain("定位答卷处理");
    expect(renderCell(action, undefined, entry)).not.toContain("核查漏扫与匹配");
  });

  it("keeps restricted identity columns anonymous even when an identity map was loaded", () => {
    const columns = createScoreColumns({ canReadStudentNames: false, identities });
    const record = { student_id: "student-1" } as SubmissionGrade;
    for (const column of columns.slice(1, 3)) {
      const html = renderCell(column as ColumnType<SubmissionGrade>, record.student_id, record);
      expect(html).toContain("权限受限");
      expect(html).not.toContain("测试学生");
      expect(html).not.toContain("测试班级");
    }
  });

  it("distinguishes known, unmatched and unlinked identities", () => {
    const columns = createScoreColumns({ canReadStudentNames: true, identities });
    const name = columns[1] as ColumnType<SubmissionGrade>;
    const record = { student_id: "student-1" } as SubmissionGrade;
    expect(renderCell(name, "student-1", record)).toBe("测试学生");
    expect(renderCell(name, "missing", record)).toBe("未匹配");
    expect(renderCell(name, undefined, record)).toBe("未关联");
    expect(renderCell(columns[2] as ColumnType<SubmissionGrade>, "student-1", record)).toBe("测试班级");
  });

  it("retains missing-item and nonfinite-score fallbacks", () => {
    const columns = createScoreColumns({ canReadStudentNames: false, identities });
    const record = { total_score: Number.NaN, max_score: 100 } as SubmissionGrade;
    expect(renderCell(columns[3] as ColumnType<SubmissionGrade>, undefined, record)).toBe("<strong>- / 100</strong>");
    expect(renderCell(columns[4] as ColumnType<SubmissionGrade>, undefined, record)).toContain("无题目分");
  });
});
