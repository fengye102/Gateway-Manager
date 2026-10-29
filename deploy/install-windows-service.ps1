# 以 Windows 服务方式常驻运行（需要管理员 PowerShell）
# 用法： .\deploy\install-windows-service.ps1 -ExePath D:\gateway-manager\gateway-manager.exe -ConfigPath D:\gateway-manager\config.json

param(
    [Parameter(Mandatory = $true)][string]$ExePath,
    [Parameter(Mandatory = $true)][string]$ConfigPath,
    [string]$ServiceName = 'gateway-manager'
)

$ErrorActionPreference = 'Stop'
$binPath = '"{0}" serve -config "{1}"' -f $ExePath, $ConfigPath

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    Write-Host "service $ServiceName exists, stopping and removing"
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 1
}

sc.exe create $ServiceName binPath= $binPath start= auto DisplayName= "gateway-manager gateway manager" | Out-Null
sc.exe failure $ServiceName reset= 86400 actions= restart/3000/restart/3000/restart/3000 | Out-Null
Start-Service -Name $ServiceName
Write-Host "service $ServiceName installed and started"
Get-Service -Name $ServiceName
