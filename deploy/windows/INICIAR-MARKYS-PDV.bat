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
set "PDV_ADDR=0.0.0.0:2222"

echo.
echo ============================================
echo          MARKYS PDV - SERVIDOR
echo ============================================
echo.
echo Configurando o acesso seguro na rede local...
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0CONFIGURAR-SERVIDOR-LAN.ps1"
if errorlevel 1 (
  echo Nao foi possivel concluir a configuracao do Firewall.
  echo O acesso por outros computadores pode nao funcionar.
)

REM Registra este inicializador para abrir automaticamente no proximo login do Windows.
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$startup=[Environment]::GetFolderPath('Startup'); $shortcut=Join-Path $startup 'MARKYS PDV Servidor.lnk'; if (-not (Test-Path $shortcut)) { $shell=New-Object -ComObject WScript.Shell; $link=$shell.CreateShortcut($shortcut); $link.TargetPath=$env:ComSpec; $link.Arguments='/c ""%~dp0INICIAR-MARKYS-PDV.bat""'; $link.WorkingDirectory='%~dp0'; $link.Description='Iniciar servidor MARKYS PDV'; $link.Save() }"

echo.
echo Servidor local: http://localhost:2222
echo Acesso dos clientes: http://markyspdv.local:2222
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
