import Papa from "papaparse";

export type StudentImportSheet = { name: string; csv: string };
export function studentSheetToCSV(rows: readonly (readonly unknown[])[]) {
  if (rows.length > 10001) throw new Error("每张表最多导入10000名学生，请分批处理");
  if (rows.some(row => row.length > 100)) throw new Error("名单列数超过100列，请仅保留本次需要的内容");
  const text = (value: unknown) => {
    if (value == null) return "";
    if (value instanceof Date) return value.toISOString().slice(0, 10);
    if (["string", "number", "boolean"].includes(typeof value)) return String(value);
    throw new Error("名单包含无法读取的单元格，请检查文件");
  };
  const first = rows[0];
  if (!first) return "";
  const width = Math.max(...rows.map(row => row.length));
  const header = Array.from({ length: width }, (_, index) => text(first[index]).trim() || `未命名列${index + 1}`);
  if (new Set(header).size !== header.length) throw new Error("表头存在重名列，请先改为不同名称再导入");
  // 数字解析由读取器保持原字符串，CSV转换不再猜学号类型；空白行也保留，以便错误行号对应原表。
  return Papa.unparse([header, ...rows.slice(1).map(row => Array.from({ length: width }, (_, index) => text(row[index])))]);
}
