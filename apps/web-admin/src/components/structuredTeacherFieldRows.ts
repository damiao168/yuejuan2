import { useState } from "react";

type RowDescription = Readonly<{ signature: string; businessId?: string }>;
type RowIdentity = RowDescription & Readonly<{ key: string }>;
type AcceptedRows = Readonly<{ source: string; rows: RowIdentity[]; nextId: number }>;
type RowTable = AcceptedRows & Readonly<{ pending?: AcceptedRows }>;

function businessId(value: unknown): string | undefined {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return undefined;
  for (const field of ["id", "point_id", "rubric_point_id"]) {
    const id: unknown = Reflect.get(value, field);
    if (typeof id === "string" && id) return `${field}:${id}`;
    if (typeof id === "number") return `${field}:${id}`;
  }
  return undefined;
}

function describeRows(values: readonly unknown[]): RowDescription[] {
  return values.map(value => ({ signature: JSON.stringify(value) ?? "undefined", businessId: businessId(value) }));
}

function uniqueBusinessIndex(rows: readonly RowIdentity[], descriptions: readonly RowDescription[], description: RowDescription) {
  if (!description.businessId) return -1;
  if (descriptions.filter(row => row.businessId === description.businessId).length !== 1) return -1;
  if (rows.filter(row => row.businessId === description.businessId).length !== 1) return -1;
  return rows.findIndex(row => row.businessId === description.businessId);
}

function reconcileRows(previous: readonly RowIdentity[], descriptions: readonly RowDescription[], nextId: number): AcceptedRows {
  const remaining = [...previous];
  const rows = descriptions.map(description => {
    let match = uniqueBusinessIndex(remaining, descriptions, description);
    if (match < 0) match = remaining.findIndex(row => row.signature === description.signature);
    if (match >= 0) {
      const [row] = remaining.splice(match, 1);
      return { ...description, key: row.key };
    }
    return { ...description, key: `teacher-row-${nextId++}` };
  });
  return { source: JSON.stringify(descriptions), rows, nextId };
}

function acceptRows(table: RowTable, descriptions: RowDescription[]): AcceptedRows {
  const source = JSON.stringify(descriptions);
  if (table.pending?.source === source) return table.pending;
  const nextId = Math.max(table.nextId, table.pending?.nextId ?? 0);
  return reconcileRows(table.rows, descriptions, nextId);
}

/** 这里只保存 UI 行身份和匹配摘要，不保存可提交的业务草稿；显示与提交都继续使用父级 values。 */
export function useTeacherFieldRows(values: readonly unknown[]) {
  const descriptions = describeRows(values);
  const source = JSON.stringify(descriptions);
  const [table, setTable] = useState<RowTable>(() => reconcileRows([], descriptions, 0));
  let accepted = table;
  if (table.source !== source) {
    accepted = acceptRows(table, descriptions);
    // 只在父级真正接纳新值时切换身份；不能用 effect 让新值先借用上一行的节点。
    setTable(accepted);
  }

  function rememberChange(nextValues: readonly unknown[], retainedKeys: readonly string[]) {
    const nextDescriptions = describeRows(nextValues);
    setTable(current => {
      let nextId = Math.max(current.nextId, current.pending?.nextId ?? 0);
      const rows = nextDescriptions.map((description, index) => ({
        ...description,
        key: retainedKeys[index] ?? `teacher-row-${nextId++}`,
      }));
      // 拒绝变更时保留已接纳的 keys；待确认摘要不参与控件显示，也不进入答案 JSON。
      return { ...current, pending: { source: JSON.stringify(nextDescriptions), rows, nextId } };
    });
  }

  const rows = values.map((value, index) => ({ key: accepted.rows[index].key, value }));
  return { rows, rowKeys: accepted.rows.map(row => row.key), rememberChange };
}
