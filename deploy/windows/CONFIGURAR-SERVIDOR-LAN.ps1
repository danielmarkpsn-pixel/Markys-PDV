#requires -Version 5.1
$ErrorActionPreference = "Stop"

$principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell.exe -Verb RunAs -ArgumentList @(
        "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$PSCommandPath`""
    )
    exit
}

$ruleName = "MARKYS PDV LAN TCP 8080"
$existing = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
if ($existing) {
    $existing | Remove-NetFirewallRule
}

New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8080 -Profile Private -RemoteAddress LocalSubnet | Out-Null

Write-Host ""
Write-Host "MARKYS PDV - CONFIGURACAO DO COMPUTADOR PRINCIPAL" -ForegroundColor Cyan
Write-Host "Regra criada: TCP 8080, somente rede Privada e dispositivos da rede local."
Write-Host ""
$profiles = Get-NetConnectionProfile -ErrorAction SilentlyContinue
foreach ($profile in $profiles) {
    Write-Host ("Rede: {0} | Perfil: {1}" -f $profile.Name, $profile.NetworkCategory)
    if ($profile.NetworkCategory -ne "Private") {
        Write-Warning "A rede acima nao esta como Privada. Altere o perfil em Configuracoes > Rede e Internet antes de liberar o acesso."
    }
}
Write-Host ""
Write-Host "Enderecos IPv4 encontrados:"
$configs = Get-NetIPConfiguration | Where-Object { $_.IPv4Address -and $_.NetAdapter.Status -eq "Up" }
foreach ($config in $configs) {
    foreach ($address in $config.IPv4Address) {
        if ($address.IPAddress -notlike "169.254.*") {
            Write-Host ("  {0} ({1})" -f $address.IPAddress, $config.InterfaceAlias)
        }
    }
}
Write-Host ""
Write-Host "Reserve no roteador o IP do computador principal para ele nao mudar."
Write-Host "Nos computadores clientes, execute CONFIGURAR-CLIENTE.ps1 como administrador"
Write-Host "informando o IP reservado. Depois acesse http://markyspdv.local:8080"
Write-Host ""
Read-Host "Pressione Enter para fechar"
