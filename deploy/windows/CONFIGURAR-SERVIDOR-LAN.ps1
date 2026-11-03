#requires -Version 5.1
$ErrorActionPreference = "Stop"
$ruleName = "MARKYS PDV LAN TCP 2222"

$existing = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
if (-not $existing) {
    $principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Host "O Windows vai solicitar autorizacao uma unica vez para liberar o acesso local do Markys PDV."
        $arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
        Start-Process powershell.exe -Verb RunAs -Wait -ArgumentList $arguments
        exit
    }

    New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Action Allow -Protocol TCP -LocalPort 2222 -Profile Private -RemoteAddress LocalSubnet | Out-Null
    Write-Host "Regra criada: TCP 2222, somente rede Privada e dispositivos da rede local." -ForegroundColor Green
} else {
    Write-Host "Firewall ja configurado para o Markys PDV (TCP 2222)."
}

$profiles = Get-NetConnectionProfile -ErrorAction SilentlyContinue
foreach ($profile in $profiles) {
    if ($profile.NetworkCategory -ne "Private") {
        Write-Warning ("A rede '{0}' esta como {1}. Para permitir clientes, configure-a como Privada somente se for uma rede confiavel." -f $profile.Name, $profile.NetworkCategory)
    }
}

Write-Host ""
Write-Host "Acesso dos outros computadores: http://markyspdv.local:2222" -ForegroundColor Cyan
Write-Host "Se o nome .local nao resolver em algum equipamento, use o IPv4 do servidor com :2222."
