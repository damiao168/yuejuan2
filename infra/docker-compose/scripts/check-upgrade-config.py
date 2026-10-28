"""只检查部署配置差异；输出键名，安全默认补丁单独生成且不改现有配置。"""
import argparse
from pathlib import Path
import re


# 仅明确无凭据且无需主机身份的缺失项可以进入补丁；账号、DSN、证书与镜像由管理员核对。
SAFE_DEFAULTS = {
    "EDUGRADE_POSTGRES_TENANT_RLS": "true",
    "EDUGRADE_INTERNAL_BIND_HOST": "127.0.0.1",
    "EDUGRADE_POSTGRES_MAX_OPEN_CONNS": "10",
    "EDUGRADE_POSTGRES_MAX_IDLE_CONNS": "5",
    "EDUGRADE_POSTGRES_CONN_MAX_LIFETIME": "30m",
    "EDUGRADE_POSTGRES_CONN_MAX_IDLE_TIME": "5m",
    "EDUGRADE_POSTGRES_STATEMENT_TIMEOUT": "60s",
    "EDUGRADE_POSTGRES_LOCK_TIMEOUT": "5s",
}


def read_env(path):
    values = {}
    for number, line in enumerate(path.read_text(encoding="utf-8-sig").splitlines(), 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, separator, value = line.partition("=")
        key = key.strip()
        if not separator or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
            # 不回显原行，格式错误也可能包含未遮蔽的凭据。
            raise ValueError(f"Invalid configuration syntax at line {number}")
        if key in values:
            raise ValueError(f"Duplicate configuration key: {key}")
        values[key] = value.strip().strip('"').strip("'")
    return values


def capability(key):
    for word, label in (("POSTGRES", "数据库与租户隔离"), ("REDIS", "会话与队列"), ("MINIO", "文件存储"),
                        ("QDRANT", "检索"), ("TLS", "服务间加密"), ("CA_", "服务间加密"),
                        ("MODEL", "模型"), ("AI_", "智能评分"), ("OCR", "答卷识别")):
        if word in key:
            return label
    return "部署运行"


def check(current, example):
    issues = []
    patch = {}
    for key, sample in example.items():
        if key not in current:
            safe = key in SAFE_DEFAULTS and sample == SAFE_DEFAULTS[key]
            issues.append((key, "缺少配置", "可审阅安全默认补丁" if safe else "由管理员按示例说明补充"))
            if safe:
                patch[key] = SAFE_DEFAULTS[key]
    # 这些旧值会被正式预检拒绝；只给出修改方向，不写回，也不输出当前值。
    for key in ("EDUGRADE_POSTGRES_TENANT_RLS",):
        if key in current and current[key].lower() != "true":
            issues.append((key, "需启用租户隔离", "核对应用数据库账号后设为 true"))
    for key in ("EDUGRADE_AI_SERVICE_URL", "EDUGRADE_INTERNAL_MATH_VERIFY_BASE_URL", "EDUGRADE_GRADING_AGENT_HEALTH_URL", "EDUGRADE_MATH_VERIFY_HEALTH_URL"):
        if current.get(key) and not current[key].startswith("https://"):
            issues.append((key, "需迁移内部 HTTPS", "先准备组织或本地证书，再更新连接地址"))
    key = "EDUGRADE_AI_PROMPT_VERSION"
    if key in current and key in example and current[key] != example[key]:
        issues.append((key, "治理提示词版本不同", "对照受治理清单确认后更新；不要直接替换运行输入"))
    return issues, patch


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    root = Path(__file__).resolve().parent.parent
    parser.add_argument("--env-file", type=Path, default=root / ".env")
    parser.add_argument("--example-file", type=Path, default=root / ".env.example")
    parser.add_argument("--patch-file", type=Path, help="生成独立的安全缺失项补丁；不覆盖已有文件")
    args = parser.parse_args()
    try:
        issues, patch = check(read_env(args.env_file), read_env(args.example_file))
        for key, reason, action in issues:
            print(f"{key} | {capability(key)} | {reason} | {action}")
        print(f"检查发现 {len(issues)} 项差异。未输出配置值；现有配置未修改。仍需执行 preflight 完成环境校验。")
        if args.patch_file:
            # x 模式防止覆盖已审阅补丁或用户误选的实际 .env。
            with args.patch_file.open("x", encoding="utf-8") as output:
                output.write("# 仅缺失的非秘密默认项，审阅后手工合并；不可替换整份 .env。\n")
                for key, value in patch.items():
                    output.write(f"{key}={value}\n")
            print(f"已生成 {len(patch)} 项安全默认补丁。账号、密钥、证书和生产镜像不在补丁中。")
    except (OSError, ValueError) as error:
        # 不输出异常中的配置值或文件内容，路径/行号足以定位读写失败。
        print(f"配置差异检查失败：{type(error).__name__}；请核对文件、键名格式和补丁文件是否已存在。")
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
