export function capturePagePosition(offset: number, pages: number) {
  return `第 ${Math.floor(offset / pages) + 1} 份答卷，第 ${offset % pages + 1} 页`;
}

export function capturePageGroups(files: readonly File[], counts: ReadonlyMap<File, number>, pages: number) {
  if (!Number.isInteger(pages) || pages < 1 || files.some(file => !counts.has(file))) return undefined;
  const groups: { number: number; sources: { file: File; first: number; last: number }[]; received: number }[] = [];
  let offset = 0;
  for (const file of files) {
    const count = counts.get(file)!;
    // 文件边界不是学生边界，同一次选择按最终文件顺序连续分组，不能每个PDF重新从学生1开始。
    for (let first = 1; first <= count;) {
      const number = Math.floor(offset / pages) + 1;
      const amount = Math.min(pages - offset % pages, count - first + 1);
      const last = groups[groups.length - 1];
      const group = last?.number === number ? last : { number, sources: [], received: 0 };
      if (last !== group) groups.push(group);
      group.sources.push({ file, first, last: first + amount - 1 }); group.received += amount;
      offset += amount; first += amount;
    }
  }
  return groups;
}
