import Papa from "papaparse";
import type { SchoolClass, StudentImportError } from "../../../api/org";

interface StudentImportRecord {
  student_no?: string;
  name?: string;
  class_code?: string;
  school_id?: string;
  class_id?: string;
}

export interface PreparedStudentImport {
  csv: string;
  errors: StudentImportError[];
  sourceRows: number[];
}

function clean(value: string | undefined) {
  return value?.trim() ?? "";
}

export function prepareStudentImport(csv: string, classes: SchoolClass[]): PreparedStudentImport {
  const parsed = Papa.parse<StudentImportRecord>(csv, {
    header: true,
    skipEmptyLines: "greedy",
    transformHeader: (value) => value.replace(/^\uFEFF/, "").trim().toLowerCase()
  });
  const headers = new Set(parsed.meta.fields ?? []);
  const errors: StudentImportError[] = parsed.errors.map((error) => ({
    row: (error.row ?? 0) + 2,
    message: "该行 CSV 格式有误，请检查列数和引号"
  }));
  const malformedRows = new Set(errors.map((error) => error.row));

  if (!headers.has("student_no") || !headers.has("name")) {
    return { csv: "", errors: [{ row: 1, message: "表头必须包含 student_no 和 name" }, ...errors], sourceRows: [] };
  }

  const hasClassCode = headers.has("class_code");
  const hasClassIDs = headers.has("school_id") && headers.has("class_id");
  if (!hasClassCode && !hasClassIDs) {
    return { csv: "", errors: [{ row: 1, message: "表头必须包含 class_code，或同时包含 school_id 和 class_id" }, ...errors], sourceRows: [] };
  }

  const classesByCode = new Map<string, SchoolClass[]>();
  for (const schoolClass of classes) {
    const code = schoolClass.code.trim().toLowerCase();
    classesByCode.set(code, [...(classesByCode.get(code) ?? []), schoolClass]);
  }

  const normalizedRows: string[][] = [];
  const sourceRows: number[] = [];
  parsed.data.forEach((record, index) => {
    const row = index + 2;
    if (malformedRows.has(row)) return;
    const studentNo = clean(record.student_no);
    const name = clean(record.name);
    if (!studentNo || !name) {
      errors.push({ row, message: "学号和姓名不能为空" });
      return;
    }

    let schoolID = hasClassIDs ? clean(record.school_id) : "";
    let classID = hasClassIDs ? clean(record.class_id) : "";
    if (!schoolID || !classID) {
      const classCode = clean(record.class_code);
      if (!classCode) {
        errors.push({ row, message: "班级代码不能为空" });
        return;
      }
      const matches = classesByCode.get(classCode.toLowerCase()) ?? [];
      if (matches.length === 0) {
        errors.push({ row, message: `班级代码 ${classCode} 不存在` });
        return;
      }
      if (matches.length > 1) {
        errors.push({ row, message: `班级代码 ${classCode} 对应多个班级，请联系管理员确保班级代码唯一` });
        return;
      }
      schoolID = matches[0].school_id;
      classID = matches[0].id;
    }

    normalizedRows.push([studentNo, name, schoolID, classID]);
    sourceRows.push(row);
  });

  return {
    csv: normalizedRows.length
      ? Papa.unparse([["student_no", "name", "school_id", "class_id"], ...normalizedRows])
      : "",
    errors,
    sourceRows
  };
}

export function remapStudentImportError(error: StudentImportError, sourceRows: number[]): StudentImportError {
  // 本地预检会剔除坏行；服务端返回的是精简 CSV 行号，需映射回用户原始文件。
  return {
    ...error,
    row: error.row > 1 ? (sourceRows[error.row - 2] ?? error.row) : error.row
  };
}
