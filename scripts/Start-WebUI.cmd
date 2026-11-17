@echo off
cd /d "%~dp0"
main.exe --ui %*
exit /b %errorlevel%
