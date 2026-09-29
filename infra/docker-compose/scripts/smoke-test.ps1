param(
  [string]$ApiUrl = "http://127.0.0.1:8080",
  [string]$PublicUrl = "http://127.0.0.1:8088",
  [switch]$SkipPublic,
  [string]$TenantCode = $env:EDUGRADE_SMOKE_TENANT_CODE,
  [string]$Username = $env:EDUGRADE_SMOKE_USERNAME,
  [string]$Password = $env:EDUGRADE_SMOKE_PASSWORD,
  [string]$SessionCookieName = $(if ([string]::IsNullOrWhiteSpace($env:EDUGRADE_SESSION_COOKIE_NAME)) { "edugrade_session" } else { $env:EDUGRADE_SESSION_COOKIE_NAME })
)

$ErrorActionPreference = "Stop"

function Assert-HttpOk([string]$Name, [string]$Url, $Session = $null) {
  $params = @{ Uri = $Url; UseBasicParsing = $true; TimeoutSec = 15 }
  if ($Session) { $params.WebSession = $Session }
  $response = Invoke-WebRequest @params
  if ($response.StatusCode -lt 200 -or $response.StatusCode -ge 300) {
    throw "$Name returned HTTP $($response.StatusCode)."
  }
  Write-Host "$Name ok ($($response.StatusCode))"
  return $response
}

Assert-HttpOk "API liveness" "$($ApiUrl.TrimEnd('/'))/health/live" | Out-Null
Assert-HttpOk "API readiness" "$($ApiUrl.TrimEnd('/'))/health/ready" | Out-Null

if (-not $SkipPublic) {
  Assert-HttpOk "Nginx health" "$($PublicUrl.TrimEnd('/'))/health" | Out-Null
  Assert-HttpOk "Backend health through Nginx" "$($PublicUrl.TrimEnd('/'))/backend-health" | Out-Null
  Assert-HttpOk "Web entry" "$($PublicUrl.TrimEnd('/'))/" | Out-Null
}


# 无凭据只验证公开健康入口；提供部分凭据则失败，避免把认证链路误报为已通过。
$credentialsProvided = -not [string]::IsNullOrWhiteSpace($TenantCode) -and -not [string]::IsNullOrWhiteSpace($Username) -and -not [string]::IsNullOrWhiteSpace($Password)
if ($credentialsProvided) {
  $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $body = @{ tenant_code = $TenantCode; username = $Username; password = $Password } | ConvertTo-Json
  $login = Invoke-WebRequest -UseBasicParsing -WebSession $session -Uri "$($ApiUrl.TrimEnd('/'))/api/v1/auth/login" -Method Post -ContentType "application/json" -Body $body -TimeoutSec 15
  if ($login.StatusCode -ne 200) { throw "Authenticated smoke login failed." }
  $cookie = $session.Cookies.GetCookies($ApiUrl) | Where-Object { $_.Name -eq $SessionCookieName }
  if (-not $cookie -or -not $cookie.HttpOnly) { throw "Login did not return the expected HttpOnly session cookie." }
  Assert-HttpOk "Authenticated /auth/me" "$($ApiUrl.TrimEnd('/'))/api/v1/auth/me" $session | Out-Null
  Assert-HttpOk "Authenticated system status" "$($ApiUrl.TrimEnd('/'))/api/v1/system/status" $session | Out-Null
} elseif ($TenantCode -or $Username -or $Password) {
  throw "Authenticated smoke test requires tenant, username, and password together."
} else {
  Write-Host "Authenticated smoke test skipped because EDUGRADE_SMOKE_* credentials were not provided."
}

Write-Host "EduGrade smoke test passed."
