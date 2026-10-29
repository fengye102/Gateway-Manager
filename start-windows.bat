@echo off
rem 网关管家 Windows 双击启动器
rem - 首次运行会自动生成本目录下的 config.json
rem - 启动后自动打开 http://127.0.0.1:7878/ui
rem - 停止请按 Ctrl+C，窗口会保留错误信息
setlocal
cd /d "%~dp0"

set "EXE=%~dp0gateway-manager-windows-amd64.exe"
if not exist "%EXE%" set "EXE=%~dp0gateway-manager.exe"
if not exist "%EXE%" (
  echo [错误] 同目录下找不到 gateway-manager-windows-amd64.exe
  echo         请把本 bat 与 exe 放在同一个文件夹里再运行。
  pause
  exit /b 1
)

if not exist "%~dp0config.json" (
  echo 首次运行：正在生成默认配置...
  "%EXE%" init -config "%~dp0config.json"
  echo.
  echo 默认配置已生成：%~dp0config.json
  echo 打开它把 upstreams[].base_url 改成你自己的网关地址即可。
  echo.
)

echo 启动网关管家（关闭窗口或 Ctrl+C 停止）...
"%EXE%" serve -config "%~dp0config.json"

echo.
echo 服务已退出（退出码 %ERRORLEVEL%）。
pause
