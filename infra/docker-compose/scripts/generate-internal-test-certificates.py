"""为隔离开发/验收生成短期内部 TLS 证书，不用于生产 PKI。"""

import argparse
import os
from pathlib import Path
import shutil
import subprocess


# 公开 CA 的生成、签发引用和只读权限共用此文件名，与 Compose 的证书挂载约定一致。
CA_CERTIFICATE_FILENAME = "ca.crt"

CA_CONFIG = """[req]
distinguished_name = dn
x509_extensions = ca_ext
prompt = no
[dn]
CN = EduGrade disposable test CA
[ca_ext]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
"""

SERVICE_DIRECTORIES = {
    "grading-agent": "grading-agent",
    "math-verification-worker": "math-verifier",
}


def run_openssl(executable, *arguments):
    subprocess.run([str(executable), *map(str, arguments)], check=True, capture_output=True)


def issue_service_certificate(executable, root, service, directory, serial):
    destination = root / directory
    destination.mkdir()
    destination.chmod(0o755)
    request_config = root / "private-ca" / f"{service}.cnf"
    request_config.write_text(
        f"""[req]
distinguished_name = dn
prompt = no
[dn]
CN = {service}
[server_ext]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid,issuer
subjectAltName = DNS:{service},DNS:localhost,IP:127.0.0.1
""",
        encoding="ascii",
    )
    csr = root / "private-ca" / f"{service}.csr"
    key = destination / "server.key"
    run_openssl(executable, "req", "-new", "-newkey", "rsa:2048", "-nodes",
                "-config", request_config, "-keyout", key, "-out", csr)
    run_openssl(executable, "x509", "-req", "-in", csr,
                "-CA", root / "internal-ca" / CA_CERTIFICATE_FILENAME,
                "-CAkey", root / "private-ca" / "ca.key", "-set_serial", serial,
                "-days", "2", "-sha256", "-extfile", request_config,
                "-extensions", "server_ext", "-out", destination / "server.crt")
    # 仅限短期测试密钥：只读卷中的非 root 服务 UID 各不相同，需要可读而不可写。
    # Windows 的文件权限由 ACL 管理，chmod 不代表已设置 Windows ACL。
    key.chmod(0o444)
    (destination / "server.crt").chmod(0o444)


def generate_certificates(output, executable):
    # 不复用或覆盖任何现有目录，失败后也保留现场；重试时使用另一个新目录。
    output.mkdir(parents=True, exist_ok=False)
    output.chmod(0o755)
    private = output / "private-ca"
    private.mkdir(mode=0o700)
    public = output / "internal-ca"
    public.mkdir()
    public.chmod(0o755)
    config = private / "ca.cnf"
    config.write_text(CA_CONFIG, encoding="ascii")
    ca_key = private / "ca.key"
    run_openssl(executable, "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                "-days", "2", "-sha256", "-config", config,
                "-keyout", ca_key, "-out", public / CA_CERTIFICATE_FILENAME)
    ca_key.chmod(0o600)
    (public / CA_CERTIFICATE_FILENAME).chmod(0o444)
    for serial, (service, directory) in enumerate(SERVICE_DIRECTORIES.items(), start=2):
        issue_service_certificate(executable, output, service, directory, serial)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--environment", choices=("local", "development", "dev", "test"), required=True)
    parser.add_argument("--openssl", default=os.environ.get("EDUGRADE_TEST_OPENSSL", ""))
    arguments = parser.parse_args()
    path = arguments.openssl or shutil.which("openssl")
    if not path or not Path(path).is_absolute() or not Path(path).is_file():
        parser.error("Provide an installed OpenSSL executable by absolute path.")
    # 工具安装目录的可信性由操作人员负责；不从仓库下载、构建或执行其他程序。
    executable = Path(path).resolve(strict=True)
    generate_certificates(arguments.output_dir.resolve(), executable)
    print(f"Disposable certificates created: {arguments.output_dir.resolve()}")
    print("Mount only internal-ca and each service directory; never mount private-ca.")


if __name__ == "__main__":
    main()
