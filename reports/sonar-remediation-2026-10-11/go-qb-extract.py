"""只提取已逐文件审读的题库阶段；绑定显式业务上下文，不包装布尔断言。"""
import re

def aliases(body, bindings):
    code = re.sub(r'"(?:\\.|[^"\\])*"|`[^`]*`|//[^\n]*', "", body)
    return "".join("\t" + name + " := " + value + "\n" for name, value in bindings.items() if re.search(r"\b" + re.escape(name) + r"\b", code))

def subtest(path, test, label, helper, params, args, bindings, replacements=None):
    source = path.read_text(encoding="utf-8")
    function = source.index("func " + test + "(")
    begin = source.index('\tt.Run("' + label + '", func(t *testing.T) {\n', function)
    body_begin = source.index("{\n", begin) + 2
    end = source.index("\n\t})", body_begin)
    body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[body_begin:end].splitlines())
    if replacements:
        for old, new in replacements.items():
            body = re.sub(r"\b" + old + r"\b", new, body)
    source = source[:body_begin] + "\t\t" + helper + "(t, " + args + ")" + source[end:]
    source += "\nfunc " + helper + "(t *testing.T, " + params + ") {\n\tt.Helper()\n" + aliases(body, bindings) + body + "\n}\n"
    path.write_text(source, encoding="utf-8")

def stage(path, owner, start, end, helper, params, call, bindings, result="", returns="", declare_err=False):
    source = path.read_text(encoding="utf-8")
    function = source.index("func " + owner + "(")
    begin = source.index(start, function)
    finish = source.index(end, begin)
    body = source[begin:finish]
    if result:
        body += "\n\treturn " + result + "\n"
    prefix = "\tvar err error\n" if declare_err else ""
    source = source[:begin] + "\t" + call + "\n" + source[finish:]
    source += "\nfunc " + helper + "(t *testing.T, " + params + ")" + returns + " {\n\tt.Helper()\n" + aliases(body, bindings) + prefix + body + "\n}\n"
    path.write_text(source, encoding="utf-8")
