import Papa from "papaparse";
import type { SchoolClass, StudentImportError } from "../../../api/org";

export interface StudentImportMapping { studentNo: string; name: string; classCode: string; schoolID: string; classID: string }

export function studentImportColumns(csv: string) {
  const parsed = Papa.parse<Record<string, string>>(csv, { header: true, preview: 1, skipEmptyLines: "greedy", transformHeader: value => value.replace(/^\uFEFF/, "").trim() });
  const headers = parsed.meta.fields ?? [];
  const choose = (...names: string[]) => headers.find(header => names.includes(header.toLowerCase())) ?? "";
  return { headers, mapping: { studentNo: choose("student_no", "学号", "考号"), name: choose("name", "姓名"), classCode: choose("class_code", "班级代码", "班级"), schoolID: choose("school_id"), classID: choose("class_id") } };
}

export interface PreparedStudentImport {
  csv: string;
  errors: StudentImportError[];
  sourceRows: number[];
}

function clean(value: string | undefined) {
  return value?.trim() ?? "";
}

export function prepareStudentImport(csv: string, classes: SchoolClass[], mapping = studentImportColumns(csv).mapping, selectedClassID = ""): PreparedStudentImport {
  const parsed = Papa.parse<Record<string, string>>(csv, {
    header: true,
    skipEmptyLines: false,
    transformHeader: (value) => value.replace(/^\uFEFF/, "").trim()
  });
  const headers = new Set(parsed.meta.fields ?? []);
  const emptyRow = (record?: Record<string, string>) => !record || Object.values(record).every(value => !String(value ?? "").trim());
  const errors: StudentImportError[] = parsed.errors.filter(error => !emptyRow(parsed.data[error.row ?? 0])).map((error) => ({
    row: (error.row ?? 0) + 2,
    message: "该行 CSV 格式有误，请检查列数和引号"
  }));
  const malformedRows = new Set(errors.map((error) => error.row));

  if (!headers.has(mapping.studentNo) || !headers.has(mapping.name) || mapping.studentNo === mapping.name) {
    return { csv: "", errors: [{ row: 1, message: "请为学号和姓名选择不同的有效列" }, ...errors], sourceRows: [] };
  }

  const hasClassCode = headers.has(mapping.classCode);
  const hasClassIDs = headers.has(mapping.schoolID) && headers.has(mapping.classID);
  const selectedClass = classes.find(item => item.id === selectedClassID);
  if (selectedClassID && !selectedClass) return { csv: "", errors: [{ row: 1, message: "所选目标班级已不可用，请重新选择" }], sourceRows: [] };
  if (!hasClassCode && !hasClassIDs && !selectedClass) {
    return { csv: "", errors: [{ row: 1, message: "请选择班级代码列，或指定整份名单的目标班级" }, ...errors], sourceRows: [] };
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
    // 保留原表的空行位置；跳过提交但不压缩错误行号，方便教师回到 Excel 找到原行。
    if (emptyRow(record)) return;
    if (malformedRows.has(row)) return;
    const studentNo = clean(record[mapping.studentNo]);
    const name = clean(record[mapping.name]);
    if (!studentNo || !name) {
      errors.push({ row, message: "学号和姓名不能为空" });
      return;
    }

    let schoolID = hasClassIDs ? clean(record[mapping.schoolID]) : "";
    let classID = hasClassIDs ? clean(record[mapping.classID]) : "";
    if (!schoolID || !classID) {
      const classCode = clean(record[mapping.classCode]);
      if (!classCode && selectedClass) { schoolID = selectedClass.school_id; classID = selectedClass.id; }
      else {
      if (!classCode) {
        errors.push({ row, message: "班级代码不能为空" });
        return;
      }
      const matches = (classesByCode.get(classCode.toLowerCase()) ?? []).filter(item => !selectedClass || item.id === selectedClass.id);
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
    }
    // 显式选班是本次导入范围，不能让文件携带的内部编号绕过该选择；服务端仍独立验证权限。
    if (selectedClass && (schoolID !== selectedClass.school_id || classID !== selectedClass.id)) { errors.push({ row, message: "该行班级与所选目标班级不一致" }); return; }

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
