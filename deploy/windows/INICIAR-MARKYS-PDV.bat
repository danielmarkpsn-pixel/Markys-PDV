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
echo Este computador sera o servidor do Markys PDV.
echo Navegador local: http://localhost:8080
echo Acesso dos clientes: http://markyspdv.local:8080
echo.
echo Antes do primeiro uso na rede:
echo 1. Execute CONFIGURAR-SERVIDOR-LAN.ps1 como administrador.
echo 2. Reserve o IP deste computador no roteador.
echo 3. Em cada cliente, execute CONFIGURAR-CLIENTE.ps1 como administrador,
echo    informando o IP reservado do servidor.
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
