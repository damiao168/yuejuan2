function Get-ApplicationSourceVersion([string]$RepositoryRoot) {
    $sourcePaths = @('apps/web-admin', 'services/api-gateway', 'ai-services', 'packages', 'contracts/authorization', 'contracts/grading-agent', 'package.json', 'package-lock.json', '.dockerignore')
    $candidates = @(& git -C $RepositoryRoot ls-files --cached --others --exclude-standard -- @sourcePaths)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate application sources for version comparison.' }
    $hash = [Security.Cryptography.SHA256]::Create()
    $manifest = [Text.StringBuilder]::new()
    try {
        foreach ($relative in @($candidates | Sort-Object -Unique)) {
            # 测试、文档和本机输出不进入运行镜像指纹；新增未提交源码也必须计入。
            $name = [IO.Path]::GetFileName([string]$relative)
            if (($name.EndsWith('.md', [StringComparison]::OrdinalIgnoreCase) -and $relative -notmatch '^ai-services/prompts/') -or $name.Contains('.test.') -or $name.Contains('.spec.') -or $name.EndsWith('_test.go')) { continue }
            if ([string]$relative -match '(^|/)(dist|node_modules|testdata|tests|__tests__)/') { continue }
            $file = Join-Path $RepositoryRoot $relative
            if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { continue }
            $digest = [BitConverter]::ToString($hash.ComputeHash([IO.File]::ReadAllBytes($file))).Replace('-', '').ToLowerInvariant()
            [void]$manifest.Append($relative).Append(':').Append($digest).Append("`n")
        }
        $fingerprint = [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes($manifest.ToString()))).Replace('-', '').ToLowerInvariant()
        $commit = & git -C $RepositoryRoot rev-parse --short HEAD
        if ($LASTEXITCODE -ne 0) { throw 'Cannot read source revision.' }
        return @{ commit = [string]$commit; fingerprint = $fingerprint }
    } finally { $hash.Dispose() }
}
