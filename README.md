# MARKYS PDV — Ponto de venda e gestão comercial local

O **MARKYS PDV** funciona com um computador principal como servidor. Os demais computadores acessam a mesma instalação e o mesmo banco de dados pelo navegador, por Wi-Fi ou cabo na mesma rede local.

## Recursos

- Frente de caixa, vendas, produtos, clientes, estoque e usuários.
- Gerador de orçamentos em desenvolvimento.
- Banco SQLite mantido no computador principal.
- Acesso compartilhado por navegador na rede local.

## Requisitos

- Computador principal Windows 10/11 (ou compatível) para executar o servidor.
- Computadores clientes com navegador atualizado.
- Todos os computadores conectados à mesma rede local/roteador.
- Go 1.23 ou superior somente para executar a partir do código-fonte.

## Configuração da rede local

### 1. Computador principal

1. Extraia todos os arquivos do pacote para uma pasta permanente, por exemplo `C:\MARKYS-PDV`.
2. Conecte o computador ao roteador por cabo ou Wi-Fi.
3. Execute `INICIAR-MARKYS-PDV.bat`.
4. Na primeira execução, o Windows solicitará autorização de administrador para criar regras de Firewall para TCP 2222 e UDP 5353. Autorize somente se estiver em uma rede privada e confiável.
5. Na primeira execução, crie a conta de administrador.
6. Mantenha a janela do servidor aberta enquanto o PDV estiver em uso.

O inicializador registra o servidor na inicialização do Windows para os próximos logins do mesmo usuário. Assim, depois de ligar o PC principal e entrar no Windows, o Markys PDV inicia automaticamente.

### 2. Outros computadores

1. Conecte o computador à mesma rede Wi-Fi ou cabeada do servidor.
2. Abra Chrome, Edge ou outro navegador atualizado.
3. Acesse **http://markyspdv.local:2222**.
4. Entre com o usuário cadastrado no computador principal.

Não é necessário instalar o Markys PDV, editar o arquivo hosts ou executar scripts nos computadores clientes. O servidor anuncia o nome `markyspdv.local` por mDNS. Se a rede ou o Windows não resolver esse nome, use o IPv4 exibido no computador principal, seguido de `:2222` (por exemplo, `http://192.168.0.10:2222`).

## Configuração técnica

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `PDV_ADDR` | `0.0.0.0:2222` | Escuta HTTP nas interfaces de rede do servidor |
| `PDV_DB` | `data/markys-pdv.db` | Banco SQLite no computador principal |

As regras do Firewall permitem TCP 2222 (PDV) e UDP 5353 (descoberta mDNS) somente no perfil Privado e na sub-rede local. Não encaminhe essa porta para a internet. Se a rede do Windows estiver como Pública, altere para Privada somente quando for uma rede confiável.

## Dados e backups

O banco fica em `data/markys-pdv.db` na pasta do servidor. Os clientes não mantêm cópias independentes. Antes de atualizar, encerre o servidor e faça backup da pasta `data`. Proteja os backups, pois podem conter dados comerciais e pessoais.

## Limites e validação

- O acesso por `markyspdv.local` depende de mDNS na rede e no dispositivo cliente. Algumas redes Wi-Fi corporativas podem bloquear multicast ou isolamento entre clientes; nesses casos, use o IPv4 do servidor ou ajuste a rede.
- Teste o acesso simultâneo por cabo e Wi-Fi antes do uso real.
- O servidor usa HTTP sem criptografia; mantenha-o restrito a uma rede local confiável.
- As sessões de login ficam em memória e são encerradas quando o servidor reinicia.
- O computador principal deve permanecer ligado e conectado à rede para os clientes acessarem.
- O sistema não emite NFC-e nem NF-e nesta versão.

## Desenvolvimento

```sh
go mod tidy
go run ./cmd/pdv
```

O workflow em `.github/workflows/build-windows.yml` executa os testes e gera o pacote Windows x64.
