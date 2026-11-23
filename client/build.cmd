@echo off
rem Rebuilds the Foghorn client using the C# compiler that is part of Windows.
rem No Visual Studio or SDK needed. Run from this folder.
rem
rem   build.cmd              -> writes ..\dist\FoghornClient.exe (what ships)
rem   build.cmd <path.exe>   -> writes somewhere else, for checking a build
rem                             without touching dist\
rem
rem It writes straight into dist\ on purpose. Building to client\ and copying by
rem hand is how 1.0.1 shipped a fixed source with the old binary still in dist\.
setlocal
set OUT=%~1
if "%OUT%"=="" set OUT=%~dp0..\dist\FoghornClient.exe

rem Pinned to the 64-bit compiler. The 32-bit one produces a working but byte-for-byte
rem different assembly, and the "dist matches source" check in CI compares assemblies.
set CSC=%WINDIR%\Microsoft.NET\Framework64\v4.0.30319\csc.exe
if not exist "%CSC%" (
  echo Could not find the 64-bit C# compiler at:
  echo   %CSC%
  echo Foghorn's client is built with the one that ships in .NET Framework 4 on 64-bit Windows.
  exit /b 1
)

"%CSC%" /nologo /target:winexe /platform:anycpu /optimize+ /out:"%OUT%" /win32icon:"%~dp0foghorn.ico" ^
  /r:System.dll /r:System.Core.dll /r:System.Drawing.dll /r:System.Windows.Forms.dll /r:System.Web.Extensions.dll /r:System.ServiceProcess.dll ^
  "%~dp0src\*.cs"
if errorlevel 1 exit /b 1
echo Built %OUT%
