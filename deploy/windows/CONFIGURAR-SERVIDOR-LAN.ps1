#requires -Version 5.1
$ErrorActionPreference = "Stop"
$tcpRuleName = "MARKYS PDV LAN TCP 2222"
$mdnsRuleName = "MARKYS PDV mDNS UDP 5353"

$tcpRule = Get-NetFirewallRule -DisplayName $tcpRuleName -ErrorAction SilentlyContinue
$mdnsRule = Get-NetFirewallRule -DisplayName $mdnsRuleName -ErrorAction SilentlyContinue
if (-not $tcpRule -or -not $mdnsRule) {
    $principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Host "O Windows vai solicitar autorizacao para liberar o acesso do Markys PDV somente na rede local."
        $arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
        Start-Process powershell.exe -Verb RunAs -Wait -ArgumentList $arguments
        exit
    }
    if (-not $tcpRule) {
        New-NetFirewallRule -DisplayName $tcpRuleName -Direction Inbound -Action Allow -Protocol TCP -LocalPort 2222 -Profile Private -RemoteAddress LocalSubnet | Out-Null
        Write-Host "Regra TCP 2222 criada para a rede Privada local." -ForegroundColor Green
    }
    if (-not $mdnsRule) {
        New-NetFirewallRule -DisplayName $mdnsRuleName -Direction Inbound -Action Allow -Protocol UDP -LocalPort 5353 -Profile Private -RemoteAddress LocalSubnet | Out-Null
        Write-Host "Regra mDNS UDP 5353 criada para a rede Privada local." -ForegroundColor Green
    }
} else {
    Write-Host "Firewall ja configurado para o Markys PDV (TCP 2222 e mDNS UDP 5353)."
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
