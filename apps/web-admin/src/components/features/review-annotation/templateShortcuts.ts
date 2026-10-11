const trailingShortcutPattern = /(?:^|\s)\/([a-z0-9][a-z0-9._-]*)$/i;

export function trailingTemplateShortcut(content: string): string | undefined {
  return trailingShortcutPattern.exec(content)?.[1]?.toLowerCase();
}

// 只替换末尾的模板命令。阅卷人可以在
// 已聚焦的批注编辑器中输入模板命令，用 Enter 插入，
// 然后立即根据这份试卷调整插入的措辞。
export function replaceTrailingTemplateShortcut(content: string, replacement: string): string {
  return content.replace(trailingShortcutPattern, (command) => {
    const leadingSpace = command.startsWith(" ") ? " " : "";
    return `${leadingSpace}${replacement}`;
  });
}
