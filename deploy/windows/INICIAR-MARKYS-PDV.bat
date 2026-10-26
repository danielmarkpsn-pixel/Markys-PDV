@echo off
setlocal
cd /d "%~dp0"

if not exist "MARKYS-PDV-windows-amd64.exe" (
  echo ERRO: MARKYS-PDV-windows-amd64.exe nao foi encontrado.
  echo Extraia todos os arquivos do pacote para uma pasta antes de iniciar.
  pause
  exit /b 1
)

if not exist "data" mkdir "data"
set "PDV_DB=%~dp0data\markys-pdv.db"
set "PDV_ADDR=0.0.0.0:8080"

echo.
echo ============================================
echo          MARKYS PDV - SERVIDOR
echo ============================================
echo.
echo O navegador sera aberto automaticamente em http://localhost:8080
echo Dados locais: "%PDV_DB%"
echo Nos outros computadores, use o IP deste PC seguido de :8080.
echo.
echo Mantenha esta janela aberta enquanto usar o PDV.
echo Para encerrar, pressione Ctrl+C nesta janela.
echo.

"MARKYS-PDV-windows-amd64.exe"
if errorlevel 1 (
  echo.
  echo O MARKYS PDV foi encerrado com erro.
  pause
)
endlocal
