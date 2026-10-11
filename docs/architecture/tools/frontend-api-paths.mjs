// 按字符扫描占位符，未闭合花括号不会反复回溯；只归一化盘点路径，不改变真实请求 URL。
export function canonicalApiPath(url) {
  let clean = url.split('?')[0];
  const suffix = '{dynamic}';
  if (clean.endsWith(suffix) && clean.at(-suffix.length - 1) && clean.at(-suffix.length - 1) !== '/') clean = clean.slice(0, -suffix.length);
  const parts = [];
  let cursor = 0;
  while (cursor < clean.length) {
    const open = clean.indexOf('{', cursor);
    if (open < 0) break;
    const close = clean.indexOf('}', open + 1);
    // 没有右括号时剩余部分都是字面量，不能再从后续左括号重复搜索。
    if (close < 0) break;
    parts.push(clean.slice(cursor, open), '{}');
    cursor = close + 1;
  }
  parts.push(clean.slice(cursor));
  return parts.join('');
}
