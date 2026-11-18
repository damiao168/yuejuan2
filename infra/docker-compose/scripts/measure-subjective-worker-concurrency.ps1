[CmdletBinding()]
param(
    [ValidateRange(1, 10)][int]$Trials = 3,
    [ValidateRange(2, 96)][int]$Tasks = 24,
    [ValidateRange(1, 5)][int]$PollSeconds = 1,
    [ValidateRange(0, 2000)][int]$ModelDelayMs = 250,
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._:/@-]+$')]
    [string]$GatewayImage = 'edugrade/api-gateway:local-functional-followup-20261115',
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._:/@-]+$')]
    [string]$AgentImage = 'edugrade/grading-agent:local-rebuild-20261101-0a03f15c6a85',
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._:/@-]+$')]
    [string]$WorkerImage = 'edugrade/subjective-grading-worker:local-functional-fixes-round2-20261115',
    [string]$GatewayBinary,
    [string]$ReportPath
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$project = 'codex-subjective-measure-' + [Guid]::NewGuid().ToString('N').Substring(0, 10)
$tempBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$scratch = Join-Path $tempBase $project
$null = New-Item -ItemType Directory -Path $scratch
$base = Join-Path $repo 'infra/docker-compose/docker-compose.story060-e2e.yml'
$overlay = Join-Path $scratch 'workers.yml'
$envFile = Join-Path $scratch 'worker-test.env'
$composeArguments = @('--project-name', $project, '--env-file', $envFile, '--file', $base, '--file', $overlay, '--profile', 'e2e')
$adminPassword = 'Worker-Test!Aa-' + [Guid]::NewGuid().ToString('N')

function Invoke-TestCompose {
    & docker compose @composeArguments @args
    if ($LASTEXITCODE -ne 0) { throw "Compose command failed for $project ($($args[0]))" }
}

function Invoke-TestSQL([string]$Sql) {
    $Sql | & docker compose @composeArguments exec -T postgres psql -U edugrade -d edugrade -X -q -A -t -v ON_ERROR_STOP=1
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL fixture failed for $project" }
}

function ConvertTo-YamlPath([string]$Path) {
    ConvertTo-Json -InputObject ((Resolve-Path -LiteralPath $Path).Path.Replace('\', '/')) -Compress
}

function New-TestSegments([string]$Key) {
    # 每题位于独立合成答卷，保留 Story060 的已准备题目、评分规则和文本 OCR 条件。
    Invoke-TestSQL @"
WITH seed AS (
  SELECT s.*, seg.question_id, seg.question_no, seg.bbox, a.answer_text,
         p.file_asset_id, p.quality_issues AS page_quality_issues
  FROM submission s
  JOIN submission_page p ON p.tenant_id=s.tenant_id AND p.submission_id=s.id
  JOIN answer_segment seg ON seg.tenant_id=s.tenant_id AND seg.submission_page_id=p.id
  JOIN answer_segment_answer a ON a.tenant_id=seg.tenant_id AND a.answer_segment_id=seg.id
  WHERE seg.id='00000000-0000-0000-0000-000000000614'
), submissions AS (
  INSERT INTO submission(tenant_id,exam_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by)
  SELECT tenant_id,exam_id,'$Key-'||n,source_type,status,1,1,quality_status,quality_issues,collected_by
  FROM seed CROSS JOIN generate_series(1,$Tasks) n RETURNING id,tenant_id,exam_id
), pages AS (
  INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status,quality_issues,quality_status)
  SELECT s.tenant_id,s.id,seed.file_asset_id,1,'accepted',seed.page_quality_issues,'passed'
  FROM submissions s CROSS JOIN seed RETURNING id,tenant_id,submission_id
), progress AS (
  INSERT INTO submission_page_processing_state(tenant_id,page_id,submission_id,exam_id,current_stage,blocking,parser_quality_json)
  SELECT p.tenant_id,p.id,p.submission_id,seed.exam_id,'READY',false,'{"text_quality":0.96}'
  FROM pages p CROSS JOIN seed
), segments AS (
  INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
  SELECT p.tenant_id,p.submission_id,p.id,seed.question_id,seed.question_no,seed.bbox,'manual','accepted'
  FROM pages p CROSS JOIN seed RETURNING id,tenant_id
), answers AS (
  INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,answer_payload,source,confidence,recorded_by)
  SELECT seg.tenant_id,seg.id,seed.answer_text,'{"synthetic":true}','ocr_text',0.96,seed.collected_by
  FROM segments seg CROSS JOIN seed RETURNING answer_segment_id
)
SELECT json_agg(answer_segment_id ORDER BY answer_segment_id)::text FROM answers;
"@
}

function Read-TestMetrics {
    $lines = Invoke-TestCompose exec -T model-stub python -c "import urllib.request; print(urllib.request.urlopen('http://api-gateway:8080/metrics', timeout=5).read().decode())"
    $result = @{}
    foreach ($line in $lines) {
        if ($line -match '^(edugrade_postgres_connection_wait(?:_seconds)?_total) ([\d.e+-]+)$') {
            $result[$Matches[1]] = [double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture)
        }
    }
    if ($result.Count -ne 2) { throw 'Gateway did not report both existing PostgreSQL pool metrics' }
    $result
}

function Measure-TestBatch([string]$Batch, [string]$ReadyAt) {
    (Invoke-TestSQL @"
WITH results AS (
  SELECT task.id, task.status, task.attempt_count, task.completed_at,
         run.status run_status, run.grade_id, attempt.worker_instance_id, attempt.started_at, attempt.duration_ms,
         (SELECT count(*) FROM ai_grade g WHERE g.tenant_id=run.tenant_id AND g.subjective_grading_run_id=run.id AND g.deleted_at IS NULL) suggestions
  FROM subjective_grading_run run
  JOIN agent_worker_task task ON task.tenant_id=run.tenant_id AND task.source_id=run.id AND task.source_type='subjective_grading_run'
  LEFT JOIN agent_worker_task_attempt attempt ON attempt.tenant_id=task.tenant_id AND attempt.task_id=task.id AND attempt.attempt_no=1
  WHERE run.batch_id='$Batch'::uuid AND task.queue_name='subjective-grading'
), totals AS (
  SELECT count(*) tasks, count(*) FILTER(WHERE status='succeeded') succeeded,
         count(*) FILTER(WHERE status IN ('failed','dead_letter','cancelled')) failed,
         count(*) FILTER(WHERE attempt_count>1) retried,
         count(*) FILTER(WHERE status='succeeded' AND run_status='succeeded' AND grade_id IS NOT NULL AND suggestions=1) unique_suggestions,
         percentile_cont(.95) WITHIN GROUP(ORDER BY extract(epoch FROM (started_at-'$ReadyAt'::timestamptz))*1000) queue_p95_ms,
         percentile_cont(.95) WITHIN GROUP(ORDER BY duration_ms) attempt_p95_ms,
         extract(epoch FROM (max(completed_at)-'$ReadyAt'::timestamptz))*1000 drain_ms
  FROM results
)
SELECT to_jsonb(totals) || jsonb_build_object('per_worker', COALESCE(
  (SELECT jsonb_object_agg(worker_instance_id, count) FROM (
    SELECT worker_instance_id,count(*) FROM results WHERE worker_instance_id IS NOT NULL GROUP BY worker_instance_id
  ) counts), '{}'::jsonb)) FROM totals;
"@) | ConvertFrom-Json -AsHashtable
}

try {
    $gatewayMount = ''
    if ($GatewayBinary) {
        $binarySource = ConvertTo-YamlPath $GatewayBinary
        $gatewayMount = @"

    volumes:
      - type: bind
        source: $binarySource
        target: /usr/local/bin/api-gateway
        read_only: true
"@
    }
    $storyClient = ConvertTo-YamlPath (Join-Path $repo 'tests/e2e/story060_grading_agent.py')
    $fixtureScript = ConvertTo-YamlPath (Join-Path $PSScriptRoot 'subjective_worker_concurrency_fixture.py')
    @"
services:
  postgres:
    healthcheck:
      test: ["CMD", "pg_isready", "-h", "127.0.0.1", "-U", "edugrade", "-d", "edugrade"]
  api-gateway:
    image: "$GatewayImage"$gatewayMount
  bootstrap-admin:
    image: "$GatewayImage"$gatewayMount
  provision-story060-users:
    image: "$GatewayImage"$gatewayMount
  grading-agent:
    image: "$AgentImage"
  subjective-grading-worker:
    image: "$WorkerImage"
    environment:
      EDUGRADE_SUBJECTIVE_WORKER_POLL_INTERVAL: "$PollSeconds"
  model-stub:
    command: ["python", "/e2e/subjective_worker_concurrency_fixture.py", "serve"]
    environment:
      EDUGRADE_E2E_MODEL_DELAY: "$($ModelDelayMs / 1000)"
    volumes:
      - type: bind
        source: $storyClient
        target: /e2e/story060_grading_agent.py
        read_only: true
      - type: bind
        source: $fixtureScript
        target: /e2e/subjective_worker_concurrency_fixture.py
        read_only: true
"@ | Set-Content -LiteralPath $overlay -Encoding utf8NoBOM
    $tls = Join-Path $scratch 'tls'
    & python (Join-Path $PSScriptRoot 'generate-internal-test-certificates.py') --environment test --output-dir $tls
    if ($LASTEXITCODE -ne 0) { throw 'Could not generate isolated stack TLS certificates' }
    # 显式指定本次试验的凭据和模型桩，避免借用工作区部署环境。
    @"
EDUGRADE_STORY060_POSTGRES_PASSWORD=$([Guid]::NewGuid().ToString('N'))
EDUGRADE_STORY060_MINIO_SECRET=$([Guid]::NewGuid().ToString('N'))
EDUGRADE_E2E_ADMIN_PASSWORD=$adminPassword
EDUGRADE_E2E_SCHOOL_ADMIN_PASSWORD=School-Test!Aa-$([Guid]::NewGuid().ToString('N'))
EDUGRADE_E2E_SUBJECTIVE_WORKER_PASSWORD=Worker-Test!Aa-$([Guid]::NewGuid().ToString('N'))
EDUGRADE_E2E_GRADING_TOKEN=$([Guid]::NewGuid().ToString('N'))
EDUGRADE_E2E_MODEL_API_KEY=$([Guid]::NewGuid().ToString('N'))
EDUGRADE_E2E_MODEL_BASE_URL=http://model-stub:8087/v1
EDUGRADE_E2E_TLS_DIR=$($tls.Replace('\','/'))
EDUGRADE_MINIO_IMAGE=quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z
EDUGRADE_MINIO_MC_IMAGE=quay.io/minio/mc:RELEASE.2025-04-16T18-13-26Z
"@ | Set-Content -LiteralPath $envFile -Encoding utf8NoBOM
    $imageIDs = @{}
    foreach ($image in @($GatewayImage, $AgentImage, $WorkerImage)) {
        $imageIDs[$image] = & docker image inspect $image --format '{{.Id}}'
        if ($LASTEXITCODE -ne 0) { throw "Required cached image was missing: $image" }
    }
    Invoke-TestCompose config -q
    Invoke-TestCompose up -d --no-build --pull never --wait --wait-timeout 300 api-gateway

    $samples = @()
    foreach ($trial in 1..$Trials) {
        $order = if ($trial % 2) { @(1, 2) } else { @(2, 1) }
        foreach ($workers in $order) {
            $key = "$project-$trial-$workers"
            $segments = New-TestSegments $key
            $batch = (Invoke-TestCompose exec -T -e "EDUGRADE_E2E_ADMIN_PASSWORD=$adminPassword" model-stub python /e2e/subjective_worker_concurrency_fixture.py $key $segments)
            if ($batch -notmatch '^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$') { throw 'Batch API did not return an ID' }
            # 领取时会清空 not_before。提前记录统一可领取时间，排除逐条入队和容器启动造成的偏差。
            $before = Read-TestMetrics
            $readyAt = Invoke-TestSQL @"
WITH cutoff AS (SELECT clock_timestamp()+interval '10 seconds' AS ready_at), queued AS (
  UPDATE agent_worker_task task SET not_before=cutoff.ready_at
  FROM subjective_grading_run run, cutoff
  WHERE run.batch_id='$batch'::uuid AND task.tenant_id=run.tenant_id AND task.source_id=run.id
    AND task.source_type='subjective_grading_run' AND task.status='queued'
  RETURNING cutoff.ready_at
)
SELECT min(ready_at)::text FROM queued HAVING count(*)=$Tasks;
"@
            if ($readyAt -notmatch '^\d{4}-\d{2}-\d{2} [0-9:.+\-]+$') { throw 'Could not set a common eligibility time for the exact batch' }
            $names = @()
            try {
                foreach ($instance in 1..$workers) {
                    $name = "$key-$instance"
                    Invoke-TestCompose run -d --no-deps --pull never --name $name -e "EDUGRADE_SUBJECTIVE_WORKER_ID=$name" subjective-grading-worker | Out-Null
                    $names += $name
                }
                $deadline = [DateTimeOffset]::UtcNow.AddSeconds(45 + ($Tasks * ($PollSeconds + 2)))
                do {
                    $metrics = Measure-TestBatch $batch $readyAt
                    if ($metrics.failed -gt 0) { throw "Batch $batch failed: $(ConvertTo-Json $metrics -Compress)" }
                    if ($metrics.succeeded -eq $Tasks) { break }
                    Start-Sleep -Milliseconds 500
                } while ([DateTimeOffset]::UtcNow -lt $deadline)
                $after = Read-TestMetrics
                if ($metrics.tasks -ne $Tasks -or $metrics.succeeded -ne $Tasks -or $metrics.retried -ne 0 -or
                    $metrics.unique_suggestions -ne $Tasks -or $metrics.per_worker.Count -ne $workers) {
                    throw "Unexpected completion: $(ConvertTo-Json $metrics -Compress)"
                }
                $metrics.trial = $trial
                $metrics.workers = $workers
                $metrics.tasks_per_second = $Tasks / ($metrics.drain_ms / 1000)
                $metrics.pg_waits = $after.edugrade_postgres_connection_wait_total - $before.edugrade_postgres_connection_wait_total
                $metrics.pg_wait_seconds = $after.edugrade_postgres_connection_wait_seconds_total - $before.edugrade_postgres_connection_wait_seconds_total
                $samples += $metrics
                ConvertTo-Json $metrics -Depth 4 -Compress
            } finally {
                if ($names.Count) {
                    $owned = @(Invoke-TestCompose ps --all --quiet)
                    foreach ($name in $names) {
                        $id = & docker container inspect $name --format '{{.Id}}'
                        if ($LASTEXITCODE -ne 0) { throw "Missing isolated worker container $name" }
                        if (!$owned.Where({ $id.StartsWith($_) })) { throw "Container $name is outside $project" }
                        & docker rm -f $name | Out-Null
                        if ($LASTEXITCODE -ne 0) { throw "Could not remove isolated worker $name" }
                    }
                }
            }
        }
    }
    if ($ReportPath) {
        $binaryHash = if ($GatewayBinary) { (Get-FileHash -LiteralPath $GatewayBinary).Hash.ToLowerInvariant() }
        @{
            project = $project; scope = 'real Python worker and PostgreSQL with text model protocol fixture'
            images = $imageIDs; gateway_binary_sha256 = $binaryHash; tasks_per_batch = $Tasks
            poll_seconds = $PollSeconds; model_delay_ms = $ModelDelayMs; samples = $samples
        } | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $ReportPath -Encoding utf8NoBOM
    }
} catch {
    if ((Test-Path -LiteralPath $overlay) -and (Test-Path -LiteralPath $envFile)) {
        & docker compose @composeArguments ps --all
        & docker compose @composeArguments logs --no-color --tail 40 api-gateway grading-agent db-migrate seed bootstrap-admin provision-story060-users
    }
    throw
} finally {
    if ((Test-Path -LiteralPath $overlay) -and (Test-Path -LiteralPath $envFile)) {
        Invoke-TestCompose down --volumes --remove-orphans
    }
    $resolved = (Resolve-Path -LiteralPath $scratch).Path
    if ($resolved -eq (Join-Path $tempBase $project)) {
        Remove-Item -LiteralPath $resolved -Force -Recurse
    }
}
