# ============================================================
# ffmpeg-over-ip 服务端 一键部署（Windows / NVIDIA GPU）
# 适用：直通了 RTX 2070 Super 等 NVIDIA 显卡的 Windows VM / 主机。
#
# 以【管理员】身份打开 PowerShell，执行（示例）：
#   Set-ExecutionPolicy -Scope Process Bypass -Force
#   .\install-server-windows.ps1 -AuthSecret "你自己的长随机密钥" -Port 5050
#
# 脚本会：下载官方 ffmpeg-over-ip-server + Jellyfin-ffmpeg（原生 NVENC）、
# 写配置、加防火墙入站规则，并在前台启动服务端。
# 想常驻后台，可用同目录生成的 start-server.ps1 注册任务计划/服务。
# ============================================================
param(
    [string]$InstallDir = "C:\ffmpeg-over-ip",
    [int]$Port = 5050,
    [string]$AuthSecret = "change-me",
    [string]$ListenIp = "0.0.0.0",
    [switch]$NoFirewall
)

$ErrorActionPreference = "Stop"
$Platform = "windows-amd64"
$zipUrl  = "https://github.com/steelbrain/ffmpeg-over-ip/releases/latest/download/$Platform-ffmpeg-over-ip-server.zip"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Set-Location $InstallDir

$need = $true
if ((Test-Path ".\ffmpeg-over-ip-server.exe") -and (Test-Path ".\ffmpeg.exe") -and (Test-Path ".\ffprobe.exe")) {
    $need = $false
}

if ($need) {
    Write-Host "正在下载 ffmpeg-over-ip 服务端（含 Jellyfin-ffmpeg / NVENC）..." -ForegroundColor Cyan
    $tmpZip = Join-Path $env:TEMP "foip-server-$([guid]::NewGuid()).zip"
    Invoke-WebRequest -Uri $zipUrl -OutFile $tmpZip
    Expand-Archive -Path $tmpZip -DestinationPath "$InstallDir\_extract" -Force
    # 兼容新旧包结构（平铺 / 嵌套一层目录）
    $root = Get-ChildItem "$InstallDir\_extract" -Recurse -File |
            Where-Object { $_.Name -eq "ffmpeg-over-ip-server.exe" } |
            Select-Object -First 1 -ExpandProperty DirectoryName
    Copy-Item "$root\ffmpeg-over-ip-server.exe" "$InstallDir\" -Force
    Copy-Item "$root\ffmpeg.exe" "$InstallDir\" -Force
    Copy-Item "$root\ffprobe.exe" "$InstallDir\" -Force
    Remove-Item "$InstallDir\_extract" -Recurse -Force
    Remove-Item $tmpZip -Force
    # 解除 Windows 网络下载标记，避免被 SmartScreen 拦截
    foreach ($b in @("ffmpeg-over-ip-server.exe","ffmpeg.exe","ffprobe.exe")) {
        Unblock-File -Path (Join-Path $InstallDir $b) -ErrorAction SilentlyContinue
    }
}

# 写配置（UTF-8 无 BOM）
$cfg = [ordered]@{
    address    = "${ListenIp}:${Port}"
    authSecret = $AuthSecret
    log        = "stdout"
    debug      = $true
}
($cfg | ConvertTo-Json -Depth 5) |
    Out-File -FilePath "$InstallDir\ffmpeg-over-ip.server.jsonc" -Encoding utf8NoBOM

# 防火墙入站规则
if (-not $NoFirewall) {
    $ruleName = "ffmpeg-over-ip-server"
    if (-not (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue)) {
        New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Protocol TCP `
            -LocalPort $Port -Action Profile -Profile Any | Out-Null
        Write-Host "已添加防火墙入站规则：TCP $Port" -ForegroundColor Green
    }
}

# 生成启动脚本
@"
@echo off
cd /d "$InstallDir"
ffmpeg-over-ip-server.exe
pause
"@ | Out-File -FilePath "$InstallDir\start-server.bat" -Encoding ascii -Force

Write-Host ""
Write-Host "部署完成：$InstallDir" -ForegroundColor Green
Write-Host "请确认 VM 已安装最新 NVIDIA 驱动（GeForce/Studio 驱动即可）。" -ForegroundColor Yellow
Write-Host "nowen 客户端连接参数：" -ForegroundColor Yellow
Write-Host "  FFMPEG_OVER_IP_CLIENT_ADDRESS=<本机IP>:$Port"
Write-Host "  FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=$AuthSecret"
Write-Host ""
Write-Host "自检 NVENC（在本机执行，应列出 h264_nvenc）：" -ForegroundColor Cyan
Write-Host "  .\ffmpeg.exe -hide_banner -encoders | findstr nvenc"
Write-Host ""
Write-Host "现在前台启动服务端（Ctrl+C 退出）：" -ForegroundColor Cyan
& ".\ffmpeg-over-ip-server.exe"
