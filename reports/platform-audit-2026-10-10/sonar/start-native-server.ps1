# 从历史官方安装只读复制出的实例，数据、临时文件和日志均属于本次审计。
$ErrorActionPreference = 'Stop'
$sonarAuditRoot = 'D:\tools\sonarqube-yuejuan\20261010'
$sonarAuditServer = Join-Path $sonarAuditRoot 'server\sonarqube-26.9.0.129388'
$sonarAuditReport = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:SONAR_JAVA_PATH = 'D:\xic\tps\win64\jre21.0.1_12\bin\java.exe'
$env:SONAR_APP_JAVA_OPTS = '-Xmx48m -Xms16m -Xss512k -XX:+UseSerialGC -XX:TieredStopAtLevel=1 -XX:ReservedCodeCacheSize=24m -XX:ActiveProcessorCount=2'
$env:SONAR_PATH_DATA = Join-Path $sonarAuditRoot 'data'
$env:SONAR_PATH_TEMP = Join-Path $sonarAuditRoot 'temp'
$env:SONAR_PATH_LOGS = Join-Path $sonarAuditRoot 'logs'
$env:SONAR_WEB_HOST = '127.0.0.1'
$env:SONAR_WEB_PORT = '19100'
$env:SONAR_SEARCH_PORT = '19101'
$env:SONAR_TELEMETRY_ENABLE = 'false'
$env:SONAR_UPDATECENTER_ACTIVATE = 'false'
$sonarAuditProcess = Start-Process -FilePath (Join-Path $sonarAuditServer 'bin\windows-x86-64\StartSonar.bat') -WorkingDirectory $sonarAuditServer -WindowStyle Hidden -RedirectStandardOutput (Join-Path $sonarAuditReport 'server-native.stdout.log') -RedirectStandardError (Join-Path $sonarAuditReport 'server-native.stderr.log') -PassThru
@{ pid = $sonarAuditProcess.Id; install = $sonarAuditServer; runtime = $sonarAuditRoot; url = 'http://127.0.0.1:19100'; startedAt = (Get-Date -Format o) } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $sonarAuditReport 'native-start.json') -Encoding utf8
