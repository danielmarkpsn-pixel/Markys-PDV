# MARKYS PDV — Ponto de venda e gestão comercial local

O **MARKYS PDV** é uma aplicação local com um computador principal atuando como servidor. Outros computadores Windows acessam a mesma instalação e o mesmo banco de dados pelo navegador, tanto por cabo de rede quanto por Wi-Fi na mesma rede local.

## Recursos

- Frente de caixa, vendas, produtos, clientes, estoque e usuários.
- Gerador de orçamentos em desenvolvimento.
- Banco SQLite mantido no computador principal.
- Acesso compartilhado por navegador na rede local.

## Requisitos

- Computador principal Windows 11 (ou Windows compatível) para executar o binário.
- Computadores clientes com navegador atualizado.
- Todos os computadores conectados ao mesmo roteador/rede local, por cabo ou Wi-Fi.
- Go 1.23 ou superior somente para executar a partir do código-fonte.

## Instalação em rede

### 1. Computador principal

1. Extraia todos os arquivos do pacote para uma pasta permanente, por exemplo `C:\MARKYS-PDV`.
2. Conecte o computador principal ao roteador por cabo ou Wi-Fi.
3. Execute `CONFIGURAR-SERVIDOR-LAN.ps1` como Administrador. O script cria uma regra de firewall para TCP 8080 limitada à rede Privada local e mostra os endereços IPv4 disponíveis.
4. No roteador, crie uma reserva DHCP para que o IP do computador principal não mude.
5. Inicie o sistema com `INICIAR-MARKYS-PDV.bat`.
6. Na primeira execução, crie a conta de administrador. Mantenha a janela do servidor aberta.

### 2. Cada computador cliente

1. Conecte o computador à mesma rede do servidor, por cabo ou Wi-Fi.
2. Execute `CONFIGURAR-CLIENTE.ps1` como Administrador e informe o IP reservado do servidor. Exemplo:

```powershell
powershell -ExecutionPolicy Bypass -File .\CONFIGURAR-CLIENTE.ps1 -IPServidor 192.168.0.10
```

3. Abra [http://markyspdv.local:8080](http://markyspdv.local:8080) no navegador e entre com seu usuário.

O script de cliente cria a associação `markyspdv.local` no arquivo hosts do Windows. Assim, o nome funciona sem depender de mDNS ou de configuração especial do roteador. Se o IP do servidor mudar, atualize a reserva DHCP e execute novamente o script em cada cliente.

## Configuração técnica

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `PDV_ADDR` | `0.0.0.0:8080` | Escuta HTTP em todas as interfaces de rede do servidor |
| `PDV_DB` | `data/markys-pdv.db` | Banco SQLite no computador principal |

O servidor escuta na porta TCP 8080. A regra de firewall criada pelo script permite conexões somente no perfil Privado e na sub-rede local. Não encaminhe essa porta para a internet. O computador principal deve permanecer ligado e conectado à rede para que os clientes usem o PDV.

## Dados e backups

O banco de dados fica em `data/markys-pdv.db` na pasta do servidor. Os clientes não mantêm cópias independentes. Antes de atualizar, encerre o servidor e faça backup da pasta `data`. Proteja os backups, pois podem conter dados comerciais e pessoais.

## Limites e validação

- Teste o acesso simultâneo por cabo e Wi-Fi antes do uso real.
- O servidor usa HTTP sem criptografia; mantenha-o restrito a uma rede local confiável.
- As sessões de login ficam em memória e são encerradas quando o servidor reinicia.
- O sistema não emite NFC-e nem NF-e nesta versão.
- O pacote não instala serviço automático do Windows; a janela do servidor precisa permanecer aberta.

## Desenvolvimento

```sh
go mod tidy
go run ./cmd/pdv
```

O workflow em `.github/workflows/build-windows.yml` executa os testes e gera o pacote Windows x64.
