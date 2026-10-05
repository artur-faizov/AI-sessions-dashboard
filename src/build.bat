@echo off
setlocal
cd /d "%~dp0.."

where python >nul 2>nul
if errorlevel 1 (
    echo Error: python not found in PATH. Install Python 3.8+ from python.org
    exit /b 1
)

if not exist .buildvenv\Scripts\python.exe (
    python -m venv .buildvenv || exit /b 1
    .buildvenv\Scripts\python -m pip install --quiet --upgrade pyinstaller || exit /b 1
)

set ARCH=%PROCESSOR_ARCHITECTURE%
if "%ARCH%"=="AMD64" set ARCH=amd64
if "%ARCH%"=="ARM64" set ARCH=arm64

.buildvenv\Scripts\python -m PyInstaller --onefile --clean ^
    --name token-usage-tree-windows-%ARCH% ^
    --add-data "%CD%\src\index.html;." ^
    --distpath dist --workpath build --specpath build ^
    dashboard.py || exit /b 1

echo.
echo Built dist\token-usage-tree-windows-%ARCH%.exe
