function Resolve-LocalProcessingMode {
    param(
        [ValidateSet('Auto','Enabled','Disabled')][string]$Requested = 'Auto',
        [string]$Saved,
        [bool]$ExplicitWorkers,
        [scriptblock]$Choose = { Read-Host 'Answer-sheet processing: 1 = enable OCR, image checks and page processing; 2 = management only. Choose 1 or 2' }
    )
    if ($Requested -eq 'Disabled' -and $ExplicitWorkers) { throw 'ProcessingMode Disabled conflicts with explicit Enable* processing switches.' }
    if ($Requested -ne 'Auto') { return $Requested }
    # 精确启用单个Worker的旧命令保持原范围，不能被保存的“全启用”模式额外扩展。
    if ($ExplicitWorkers) { return 'Explicit' }
    if ($Saved -in @('Enabled','Disabled')) { return $Saved }
    $choice = & $Choose
    switch ($choice) {
        '1' { return 'Enabled' }
        '2' { return 'Disabled' }
        default { throw 'Choose processing mode 1 or 2, or pass -ProcessingMode Enabled/Disabled.' }
    }
}

function Stop-DisabledLocalProcessingWorkers {
    param([string]$Mode, [string]$EnvFile, [string]$ComposeFile)
    if ($Mode -ne 'Disabled') { return }
    # 禁用模式也要停止上次已运行的处理服务；否则配置显示关闭，旧Worker仍会继续消费任务。
    & docker compose --env-file $EnvFile -f $ComposeFile stop ocr-worker image-quality-worker page-processing-worker
    if ($LASTEXITCODE -ne 0) { throw 'Could not stop answer-sheet processing workers; disabled mode was not verified.' }
}
