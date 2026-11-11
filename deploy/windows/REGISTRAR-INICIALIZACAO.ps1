#requires -Version 5.1
$ErrorActionPreference = "Stop"
$startupFolder = [Environment]::GetFolderPath("Startup")
$shortcutPath = Join-Path $startupFolder "MARKYS PDV Servidor.lnk"
$batchPath = Join-Path $PSScriptRoot "INICIAR-MARKYS-PDV.bat"
if (-not (Test-Path $batchPath)) { throw "INICIAR-MARKYS-PDV.bat nao foi encontrado." }
$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($shortcutPath)
$link.TargetPath = Join-Path $env:SystemRoot "System32\cmd.exe"
$link.Arguments = '/c "' + $batchPath + '"'
$link.WorkingDirectory = $PSScriptRoot
$link.Description = "Iniciar servidor MARKYS PDV"
$link.Save()
Write-Host "Inicializacao automatica configurada para o usuario atual do Windows."
