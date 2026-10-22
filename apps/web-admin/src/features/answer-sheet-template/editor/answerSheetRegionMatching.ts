import type { LayoutRegion } from "../../../api/configuration";
import type { Question } from "../../../api/papers";

export type SheetFrame = { x: number; y: number; width: number; height: number; confidence: number };
export type SheetWord = { text: string; x: number; y: number; width: number; height: number; confidence: number; source?: "pdf_text" | "ocr"; lineId?: string };

function questionKey(value: string) {
  const key = value.normalize("NFKC").replace(/\s+/g, "").replace(/[.、:]$/, "");
  return /^\(\d+\)$/.test(key) ? key.slice(1, -1) : key;
}

function isInsideFrame(word: SheetWord, frame: SheetFrame) {
  return word.x >= frame.x - .006 && word.x + word.width <= frame.x + frame.width + .006
    && word.y >= frame.y - .012 && word.y + word.height <= frame.y + frame.height + .006;
}

function isChoiceQuestion(question: Question) {
  return question.question_type === "single_choice" || question.question_type === "multiple_choice";
}

function hasChoiceEvidence(anchor: SheetWord, question: Question, words: SheetWord[]) {
  if (!isChoiceQuestion(question) || !anchor.lineId) return false;
  // 选项不能从邻题借来：同一OCR行内，下一个数字题号就是本题选项证据的右边界。
  const nextNumber = words.filter(word => word.lineId === anchor.lineId && word.x > anchor.x + anchor.width
    && /^\d+$/.test(questionKey(word.text))).sort((a, b) => a.x - b.x)[0];
  const labels = new Set(words.flatMap(word => {
    if (word.lineId !== anchor.lineId || word.confidence < 55 || word.x < anchor.x + anchor.width
      || (nextNumber && word.x + word.width > nextNumber.x)) return [];
    const token = word.text.normalize("NFKC").replace(/\s+/g, "");
    const label = /^(?:\[([A-H])\]|\(([A-H])\)|([A-H])[.、:]?)$/.exec(token);
    return label ? [label[1] ?? label[2] ?? label[3]] : [];
  }));
  return labels.size >= 2;
}

function sameHeaderPosition(left: SheetWord, right: SheetWord) {
  if (!left.lineId?.startsWith("header:") && !right.lineId?.startsWith("header:")) return false;
  return Math.abs(left.x - right.x) <= Math.max(.006, Math.min(left.width, right.width) * .5)
    && Math.abs(left.y - right.y) <= Math.max(.004, Math.min(left.height, right.height) * .5)
    && Math.min(left.x + left.width, right.x + right.width) > Math.max(left.x, right.x)
    && Math.min(left.y + left.height, right.y + right.height) > Math.max(left.y, right.y);
}

/** 边框负责几何，OCR只把框内印刷题号关联到本场题目；不按区域数量猜题号。 */
export function matchAnswerSheetRegions(frames: SheetFrame[], words: SheetWord[], questions: Question[], used: Set<string>) {
  const keys = new Map(questions.map(question => [questionKey(question.question_no), question]));
  const anchors = words.flatMap(word => {
    const key = questionKey(word.text);
    const question = keys.get(key);
    if (!question || used.has(question.id) || word.confidence < 55 || !/^\d+(?:\.\d+|\(\d+\))?$/.test(key)) return [];
    // 裸数字必须有选择题选项作证，不能把已写答案当成题号；文字层继续兼容裸题号。
    const token = word.text.normalize("NFKC").replace(/\s+/g, "");
    const requiresChoiceEvidence = word.source !== "pdf_text" && !/^(?:\(\d+\)|\d+[.、:]|\d+(?:\.\d+|\(\d+\))[.、:]?)$/.test(token);
    if (requiresChoiceEvidence && (!/^\d+$/.test(token) || !isChoiceQuestion(question) || !word.lineId)) return [];
    return [{ ...word, question, requiresChoiceEvidence }];
  });
  const candidates: Array<LayoutRegion & { anchorConfidence: number }> = [];
  for (const frame of frames) {
    // 仅在确实有边框的区域内绑定；页眉、得分数字和边框外的手写数字不会单独变成答题区。
    const frameWords = words.filter(word => isInsideFrame(word, frame));
    const frameAnchors = anchors.filter(anchor => isInsideFrame(anchor, frame));
    const evidenced = frameAnchors.flatMap(anchor => {
      const choiceEvidence = hasChoiceEvidence(anchor, anchor.question, frameWords);
      return anchor.requiresChoiceEvidence && !choiceEvidence ? [] : [{ ...anchor, choiceEvidence }];
    });
    // 框头重扫与整页扫描可能读到同一题号；只在当前框内合并同位置，保留标点与原行选项证据。
    const inside = evidenced.reduce<typeof evidenced>((items, anchor) => {
      const existing = items.findIndex(item => item.question.id === anchor.question.id && sameHeaderPosition(item, anchor));
      if (existing < 0) items.push(anchor);
      else {
        const previous = items[existing];
        const preferred = anchor.lineId?.startsWith("header:") ? anchor : previous;
        items[existing] = { ...preferred, choiceEvidence: previous.choiceEvidence || anchor.choiceEvidence };
      }
      return items;
    }, []);
    if (!inside.length) continue;
    // 题号通常位于单元左上方；正文中的重复数字不能抢走框的归属。
    const possibleHeaders = inside.filter(a => a.x < frame.x + frame.width * .38
      && a.y < frame.y + Math.min(.055, frame.height * .3));
    const firstHeader = [...possibleHeaders].sort((a, b) => a.y - b.y || a.x - b.x)[0];
    if (!firstHeader) continue;
    const onHeaderRow = (word: typeof firstHeader) => Math.abs(word.y - firstHeader.y) <
      Math.max(.006, Math.min(word.height, firstHeader.height) * .7);
    const headers = possibleHeaders.filter(onHeaderRow);
    // 默认仅按顶端同一印刷行横分；下方只有完整选择题网格证据才允许纵分，避免手写(1)/(2)切碎大题框。
    const topRow = inside.filter(onHeaderRow);
    const unique = new Set(topRow.map(a => a.question.id));
    // 同题不同位置不能先按题号去重，否则会在最后的歧义检查前丢掉冲突证据。
    let cells = unique.size > 1 && frame.height < .3 ? topRow : headers;
    let gridRows: typeof inside[] | undefined;
    const choiceAnchors = inside.filter(anchor => anchor.choiceEvidence).sort((a, b) => a.y - b.y || a.x - b.x);
    // 选择题框中还有未证实的裸题号时不能让首题占满整框；只豁免被同位置框头二扫证实的题号。
    // 以实际框头或选项证据判断选择题框，避免主观题正文中的手写(1)/(2)触发拒绝。
    if ((choiceAnchors.length || cells.some(anchor => isChoiceQuestion(anchor.question)))
      && frameAnchors.some(anchor => anchor.requiresChoiceEvidence
        && !hasChoiceEvidence(anchor, anchor.question, frameWords)
        && !inside.some(verified => verified.question.id === anchor.question.id && !verified.requiresChoiceEvidence
          && sameHeaderPosition(verified, anchor)))) continue;
    const rows: typeof inside[] = [];
    for (const anchor of choiceAnchors) {
      const row = rows.find(items => Math.abs(items[0].y - anchor.y) < Math.max(.006, Math.min(items[0].height, anchor.height) * .7));
      if (row) row.push(anchor); else rows.push([anchor]);
    }
    if (rows.length > 1) {
      for (const row of rows) row.sort((a, b) => a.x - b.x);
      // 仅对每格都有选项证据、列位置一致的网格纵向切分；混排或重复题号保持未匹配。
      const consistentGrid = choiceAnchors.length === inside.length
        && new Set(choiceAnchors.map(anchor => anchor.question.id)).size === choiceAnchors.length
        && rows.every(row => row.length === rows[0].length && row.every((anchor, column) =>
          Math.abs(anchor.x - rows[0][column].x) <= Math.max(.012, frame.width * .04)));
      if (!consistentGrid) continue;
      cells = rows.flat();
      gridRows = rows;
    }
    for (const anchor of cells) {
      const gridRow = gridRows?.findIndex(row => row.includes(anchor)) ?? -1;
      const sameRow = gridRows && gridRow >= 0 ? gridRows[gridRow]
        : cells.filter(a => Math.abs(a.y - anchor.y) < Math.max(.012, anchor.height));
      const left = sameRow.filter(a => a.x < anchor.x - .006).sort((a, b) => b.x - a.x)[0];
      const right = sameRow.filter(a => a.x > anchor.x + .006).sort((a, b) => a.x - b.x)[0];
      const x = left ? anchor.x - .006 : frame.x;
      const y = gridRow > 0 ? anchor.y - .006 : frame.y;
      const endX = right ? right.x - .01 : frame.x + frame.width;
      const nextRow = gridRows && gridRow >= 0 ? gridRows[gridRow + 1] : undefined;
      const endY = nextRow ? Math.min(...nextRow.map(item => item.y)) - .008 : frame.y + frame.height;
      if (endX - x < .015 || endY - y < .012) continue;
      candidates.push({ id: crypto.randomUUID(), question_id: anchor.question.id, label: anchor.question.question_no,
        x, y, width: endX - x, height: endY - y, option_regions: [],
        suggestion_source: "ocr_layout", suggestion_confidence: Math.min(frame.confidence, anchor.confidence / 100),
        anchorConfidence: anchor.confidence });
    }
  }
  const output: LayoutRegion[] = [];
  const byQuestion = new Map<string, typeof candidates>();
  for (const item of candidates) byQuestion.set(item.question_id!, [...(byQuestion.get(item.question_id!) ?? []), item]);
  for (const [questionID, entries] of byQuestion) {
    // 同一题号落入两个不同框时保留为未匹配；不把不确定结果静默套给一个框。
    const distinct = entries.filter((item, index) => !entries.slice(0, index).some(old =>
      Math.abs(old.x - item.x) < .01 && Math.abs(old.y - item.y) < .01
      && Math.abs(old.width - item.width) < .01 && Math.abs(old.height - item.height) < .01));
    if (distinct.length !== 1 || used.has(questionID)) continue;
    const { anchorConfidence: _, ...region } = distinct[0];
    output.push(region);
    used.add(questionID);
  }
  return output;
}
