interface Directory { offset: number; next: number; entries: number; count: number }
export interface TiffDirectories {
  directories: Directory[];
  little: boolean;
  big: boolean;
  first: number;
  pointerSize: number;
}

const fieldSizes: Record<number, number> = { 1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4, 16: 8, 17: 8, 18: 8 };

function exactNumber(value: bigint) {
  if (value < BigInt(Number.MIN_SAFE_INTEGER) || value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("TIFF目录数值超出本地精确读取范围");
  return Number(value);
}

function pointer(view: DataView, offset: number, big: boolean, little: boolean) {
  return big ? exactNumber(view.getBigUint64(offset, little)) : view.getUint32(offset, little);
}

/** 计数只沿主页面链；缩略图子目录不作为额外学生页，也不递归解析其像素。 */
export function captureTiffDirectories(bytes: ArrayBuffer): TiffDirectories {
  const view = new DataView(bytes);
  if (view.byteLength < 8) throw new Error("TIFF文件头不完整");
  const marker = view.getUint16(0), little = marker === 0x4949;
  if (marker !== 0x4949 && marker !== 0x4d4d) throw new Error("TIFF字节序无法识别");
  const version = view.getUint16(2, little), big = version === 43;
  if (version !== 42 && !big) throw new Error("TIFF格式无法识别");
  if (big && (view.byteLength < 16 || view.getUint16(4, little) !== 8 || view.getUint16(6, little) !== 0)) throw new Error("BigTIFF文件头无法核对");
  const header = big ? 16 : 8, first = big ? 8 : 4, pointerSize = big ? 8 : 4;
  const countSize = big ? 8 : 2, entrySize = big ? 20 : 12;
  const directories: Directory[] = [], visited = new Set<number>();
  let offset = pointer(view, first, big, little);
  while (offset) {
    if (offset < header || offset > view.byteLength - countSize || visited.has(offset) || directories.length >= 10_000) throw new Error("TIFF页面链异常或超过10000页");
    visited.add(offset);
    const count = big ? exactNumber(view.getBigUint64(offset, little)) : view.getUint16(offset, little);
    if (!count || count > 4096) throw new Error("TIFF页面目录异常");
    const next = offset + countSize + count * entrySize;
    if (next > view.byteLength - pointerSize) throw new Error("TIFF页面目录不完整");
    directories.push({ offset, next, entries: offset + countSize, count });
    offset = pointer(view, next, big, little);
  }
  if (!directories.length) throw new Error("TIFF未包含可导入页面");
  const sorted = [...directories].sort((a, b) => a.offset - b.offset);
  if (sorted.some((item, index) => index > 0 && item.offset < sorted[index - 1].next + pointerSize)) throw new Error("TIFF页面目录重叠，未调整原件");
  return { directories, little, big, first, pointerSize };
}

/** 标签读取供预览与原件完整性检查使用，不将BigTIFF转存为classic TIFF。 */
export function readCaptureTiffTags(bytes: ArrayBuffer, chain: TiffDirectories) {
  const view = new DataView(bytes), { big, little } = chain;
  const entrySize = big ? 20 : 12, inlineSize = big ? 8 : 4;
  let totalValues = 0;
  return chain.directories.map(directory => {
    const tags: Record<string, number[] | Uint8Array | string[]> = {};
    for (let index = 0; index < directory.count; index++) {
      const entry = directory.entries + index * entrySize;
      const tag = view.getUint16(entry, little), type = view.getUint16(entry + 2, little);
      const count = big ? exactNumber(view.getBigUint64(entry + 4, little)) : view.getUint32(entry + 4, little);
      const size = fieldSizes[type], length = size * count;
      if (!size || !Number.isSafeInteger(length) || length < 0) throw new Error("TIFF标签类型或长度无法核对");
      const valueSlot = entry + (big ? 12 : 8);
      const start = length <= inlineSize ? valueSlot : pointer(view, valueSlot, big, little);
      if (start < (big ? 16 : 8) || start > bytes.byteLength - length) throw new Error("TIFF页面数据越界，未调整原件");
      if (length > inlineSize && [chain.first, ...chain.directories.map(item => item.next)].some(at => start < at + chain.pointerSize && start + length > at)) throw new Error("TIFF标签数据与目录指针重叠，未调整原件");
      // 元数据也可能被恶意放大；超过预算时保留原件，交给服务端处理，不分配巨型数组。
      totalValues += count;
      if (totalValues > 1_000_000) throw new Error("TIFF标签数据超出本地预览范围");
      const key = `t${tag}`;
      if (tags[key]) throw new Error("TIFF页面包含重复标签，未调整原件");
      if (type === 1 || type === 7) { tags[key] = new Uint8Array(bytes, start, count); continue; }
      if (type === 2) { tags[key] = [new TextDecoder("ascii").decode(new Uint8Array(bytes, start, count)).replace(/\0+$/, "")]; continue; }
      const values: number[] = [];
      for (let item = 0; item < count; item++) {
        const at = start + item * size;
        let value: number;
        switch (type) {
          case 3: value = view.getUint16(at, little); break;
          case 4: case 13: value = view.getUint32(at, little); break;
          case 5: value = view.getUint32(at, little) / view.getUint32(at + 4, little); break;
          case 6: value = view.getInt8(at); break;
          case 8: value = view.getInt16(at, little); break;
          case 9: value = view.getInt32(at, little); break;
          case 10: value = view.getInt32(at, little) / view.getInt32(at + 4, little); break;
          case 11: value = view.getFloat32(at, little); break;
          case 12: value = view.getFloat64(at, little); break;
          case 17: value = exactNumber(view.getBigInt64(at, little)); break;
          default: value = exactNumber(view.getBigUint64(at, little));
        }
        if (!Number.isFinite(value)) throw new Error("TIFF标签数值无法核对");
        values.push(value);
      }
      tags[key] = values;
    }
    return tags;
  });
}

export function writeCaptureTiffPointer(view: DataView, offset: number, value: number, chain: TiffDirectories) {
  if (chain.big) view.setBigUint64(offset, BigInt(value), chain.little);
  else view.setUint32(offset, value, chain.little);
}
