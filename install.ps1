# gateway-manager 一体化安装（Windows）：复制二进制 + 生成/修正配置 + 注册服务
# 用法（管理员 PowerShell）：
#   .\install.ps1
#   .\install.ps1 -Port 7878 -BindAddress 127.0.0.1 -AccessKey sk-local -AdminToken admin -Service
#   .\install.ps1 -Uninstall
[CmdletBinding()]
param(
    [string]$ExePath = (Join-Path $PSScriptRoot 'build\gateway-manager-windows-amd64.exe'),
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'gateway-manager'),
    [int]$Port = 7878,
    [string]$BindAddress = '127.0.0.1',
    [string]$AccessKey = '',
    [string]$AdminToken = '',
    [switch]$Service,
    [string]$ServiceName = 'gateway-manager',
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'

function Info($msg) { Write-Host "[gateway-manager] $msg" -ForegroundColor Cyan }
function Fail($msg) { Write-Host "[gateway-manager] $msg" -ForegroundColor Red; exit 1 }

if ($Uninstall) {
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        Info "停止并删除服务 $ServiceName"
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        sc.exe delete $ServiceName | Out-Null
    }
    Info "保留安装目录 $InstallDir（彻底删除： Remove-Item -Recurse -Force '$InstallDir'）"
    exit 0
}

if (-not (Test-Path -LiteralPath $ExePath)) { Fail "找不到二进制：$ExePath" }
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$target = Join-Path $InstallDir 'gateway-manager.exe'
Info "复制二进制到 $target"
Copy-Item -LiteralPath $ExePath -Destination $target -Force

$configPath = Join-Path $InstallDir 'config.json'
if (-not (Test-Path -LiteralPath $configPath)) {
    Info '生成默认配置'
    & $target init -config $configPath | Out-Null
}

Info '修正配置（端口 / 绑定地址 / 密钥）'
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
if (-not $cfg.server) { $cfg | Add-Member -NotePropertyName server -NotePropertyValue ([pscustomobject]@{}) }
$cfg.server | Add-Member -NotePropertyName port -NotePropertyValue $Port -Force
$cfg.server | Add-Member -NotePropertyName bind -NotePropertyValue $BindAddress -Force
if ($AccessKey) { $cfg.server | Add-Member -NotePropertyName access_keys -NotePropertyValue @($AccessKey) -Force }
if ($AdminToken) { $cfg.server | Add-Member -NotePropertyName admin_token -NotePropertyValue $AdminToken -Force }
$cfg | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $configPath -Encoding UTF8

if ($Service) {
    $statePath = Join-Path $InstallDir 'state.json'
    $binPath = '"' + $target + '" serve -config "' + $configPath + '" -state "' + $statePath + '"'
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        Info '服务已存在，先停止删除'
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        sc.exe delete $ServiceName | Out-Null
        Start-Sleep -Seconds 1
    }
    Info "注册服务 $ServiceName"
    sc.exe create $ServiceName binPath= $binPath start= auto DisplayName= 'gateway-manager gateway manager' | Out-Null
    sc.exe failure $ServiceName reset= 86400 actions= restart/3000/restart/3000/restart/3000 | Out-Null
    Start-Service -Name $ServiceName
    Get-Service -Name $ServiceName | Format-Table -AutoSize
} else {
    Info '未注册服务（加 -Service 注册为开机自启服务）'
    Info ('前台运行： ' + $target + ' serve -config ' + $configPath)
}

Info ('控制台： http://' + $BindAddress + ':' + $Port + '/ui')
Info ('指标：   http://' + $BindAddress + ':' + $Port + '/metrics')
Info '完成。卸载： .\install.ps1 -Uninstall'
