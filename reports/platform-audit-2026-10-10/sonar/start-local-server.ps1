# 所有资源都以本次审计命名；不修改现有服务、数据库或宿主机内核设置。
$ErrorActionPreference = 'Stop'
$auditNetwork = 'yuejuan-audit-sonar-net-20261010'
$auditContainer = 'yuejuan-audit-sonar-20261010'
$auditReport = Split-Path -Parent $MyInvocation.MyCommand.Path
docker network create --internal $auditNetwork 2>&1 | Out-File -LiteralPath (Join-Path $auditReport 'network-create.log')
if ($LASTEXITCODE -ne 0) { throw 'Audit network creation failed' }
docker run -d --name $auditContainer --network $auditNetwork --network-alias sonar --memory 2g --cpus 3 -p 127.0.0.1:19000:9000 -e SONAR_TELEMETRY_ENABLE=false -e SONAR_ES_BOOTSTRAP_CHECKS_DISABLE=true -e 'SONAR_SEARCH_JAVAOPTS=-Xms256m -Xmx256m -XX:MaxDirectMemorySize=256m' -e 'SONAR_WEB_JAVAOPTS=-Xms128m -Xmx384m' -e 'SONAR_CE_JAVAOPTS=-Xms128m -Xmx512m' -v yuejuan-audit-sonar-data-20261010:/opt/sonarqube/data -v yuejuan-audit-sonar-logs-20261010:/opt/sonarqube/logs -v yuejuan-audit-sonar-extensions-20261010:/opt/sonarqube/extensions sonarqube:26.9.0.129388-community 2>&1 | Out-File -LiteralPath (Join-Path $auditReport 'container-create.log')
if ($LASTEXITCODE -ne 0) { throw 'Local SonarQube container creation failed' }
# 开发审计允许在容器内跳过 ES 生产宿主机校验，禁止改变宿主机 vm.max_map_count。
docker inspect $auditContainer | Out-File -LiteralPath (Join-Path $auditReport 'server-container-inspect.json') -Encoding utf8
docker image inspect sonarqube:26.9.0.129388-community | Out-File -LiteralPath (Join-Path $auditReport 'image-inspect.json') -Encoding utf8
