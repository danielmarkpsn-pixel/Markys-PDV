#requires -Version 5.1
param(
    [Parameter(Mandatory = $true)]
    [string]$IPServidor
)
$ErrorActionPreference = "Stop"

$parsedIP = $null
if (-not [System.Net.IPAddress]::TryParse($IPServidor, [ref]$parsedIP) -or $parsedIP.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork) {
    throw "Informe um endereco IPv4 valido, por exemplo 192.168.0.10."
}

$principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell.exe -Verb RunAs -ArgumentList @(
        "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$PSCommandPath`"", "-IPServidor", $parsedIP.IPAddressToString
    )
    exit
}

$hostsPath = Join-Path $env:SystemRoot "System32\drivers\etc\hosts"
$backupPath = "$hostsPath.markys-backup"
if (-not (Test-Path $backupPath)) {
    Copy-Item $hostsPath $backupPath
}

$lines = @(Get-Content -Path $hostsPath -ErrorAction Stop | Where-Object { $_ -notmatch "(?i)\bmarkyspdv\.local\b" })
$lines += "$($parsedIP.IPAddressToString) markyspdv.local # MARKYS PDV"
[System.IO.File]::WriteAllLines($hostsPath, $lines, [System.Text.Encoding]::ASCII)

Write-Host ""
Write-Host "Computador configurado." -ForegroundColor Green
Write-Host "Acesse: http://markyspdv.local:8080"
Write-Host "O computador deve estar na mesma rede Wi-Fi ou cabeada do servidor."
Write-Host "Se o IP do servidor mudar, execute este script novamente com o novo IP."
Read-Host "Pressione Enter para fechar"
