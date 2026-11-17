@echo off
cd /d "%~dp0"
main.exe --stop-ui %*
exit /b %errorlevel%
