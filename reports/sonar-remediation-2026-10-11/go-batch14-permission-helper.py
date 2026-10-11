"""将角色权限集合查找作为独立语义函数，原授权断言不变。"""
from pathlib import Path

path = Path("services/api-gateway/internal/auth/migrations_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func assertAuthorizationContractPermissionBoundaries(")
begin = source.index("\n\tcontains := func(values []string, target string) bool {", function) + 1
end = source.index("\n\tfor _, permission :=", begin)
body_begin = source.index("{\n", begin) + 2
body = source[body_begin:end].rsplit("\n\t}", 1)[0]
body = "\n".join(line[1:] if line.startswith("\t") else line for line in body.splitlines())
source = source[:begin] + source[end+1:]
source = source[:function] + source[function:].replace("contains(", "authorizationPermissionPresent(")
source += "\nfunc authorizationPermissionPresent(values []string, target string) bool {\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
