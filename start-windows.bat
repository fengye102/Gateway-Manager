@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

set "EXE=%~dp0gateway-manager.exe"
if not exist "%EXE%" set "EXE=%~dp0gateway-manager-windows-amd64.exe"
if not exist "%EXE%" goto :noexe

echo 使用程序: %EXE%
if exist "%~dp0config.json" goto :run

echo 首次运行：正在生成默认配置 ...
"%EXE%" init -config "%~dp0config.json"
echo.
echo 配置已生成：%~dp0config.json
echo 把 upstreams[].base_url 改成你自己的网关地址即可。
echo.

:run
echo 启动网关管家，按 Ctrl+C 或关闭窗口停止 ...
"%EXE%" serve -config "%~dp0config.json"
echo.
echo 服务已退出，退出码 %ERRORLEVEL%。
pause
exit /b 0

:noexe
echo [错误] 没找到 gateway-manager.exe，请把 exe 与本脚本放在同一个文件夹。
echo 当前目录下的 exe：
dir /b "%~dp0*.exe"
pause
exit /b 1
