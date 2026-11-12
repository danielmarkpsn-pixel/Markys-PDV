# MARKYS PDV — Ponto de venda e gestão comercial local

O **MARKYS PDV** é um sistema de ponto de venda e gestão comercial local, projetado para ter um computador principal executando o sistema e armazenando os dados.

## Posicionamento comercial

A oferta comercial do MARKYS PDV é destinada a **um computador principal por licença**. A comunicação comercial deve apresentar o produto para uso em um único computador do estabelecimento.

A possibilidade técnica de abrir o sistema em outros computadores pela rede local **não significa que o uso em vários PCs seja o cenário recomendado, tenha desempenho garantido ou esteja incluído como benefício assegurado da oferta**. Não anuncie o produto como uma solução multiestação.

## Recursos

- Frente de caixa, vendas, produtos, clientes, estoque e usuários.
- Gerador de orçamentos.
- Banco SQLite mantido localmente no computador principal.
- Possibilidade técnica de acesso por navegador em outros dispositivos da mesma rede local, sujeita às limitações descritas neste documento.

## Requisitos para o uso recomendado

- Um computador principal com Windows 10/11 (ou compatível) para executar o sistema.
- Go 1.23 ou superior somente para executar a partir do código-fonte.

## Capacidade técnica opcional: acesso pela rede local

O servidor pode aceitar conexões de outros computadores conectados à mesma rede, por Wi-Fi ou cabo. Esse recurso descreve uma capacidade técnica do programa, **não uma promessa de funcionamento estável ou de desempenho adequado em múltiplos computadores**.

O resultado pode variar conforme o computador principal, a quantidade de dispositivos, a qualidade e configuração do roteador/rede, o tráfego simultâneo e as condições do ambiente. Antes de depender desse modo de uso, é necessário testá-lo no ambiente real. Para a utilização comercial recomendada, considere um único computador principal.

### 1. Computador principal

1. Extraia todos os arquivos do pacote para uma pasta permanente, por exemplo `C:\\MARKYS-PDV`.
2. Conecte o computador ao roteador por cabo ou Wi-Fi.
3. Execute `INICIAR-MARKYS-PDV.bat`.
4. Na primeira execução, o Windows solicitará autorização de administrador para criar regras de Firewall para TCP 2222 e UDP 5353. Autorize somente se estiver em uma rede privada e confiável.
5. Na primeira execução, crie a conta de administrador.
6. Mantenha a janela do servidor aberta enquanto o PDV estiver em uso.

O inicializador registra o servidor na inicialização do Windows para os próximos logins do mesmo usuário. Assim, depois de ligar o PC principal e entrar no Windows, o Markys PDV inicia automaticamente.

### 2. Acesso opcional por outro computador

Somente para testes ou uso por conta e risco do operador, um dispositivo na mesma rede pode tentar acessar:

1. Conecte o dispositivo à mesma rede Wi-Fi ou cabeada do servidor.
2. Abra Chrome, Edge ou outro navegador atualizado.
3. Acesse **http://markyspdv.local:2222**.
4. Entre com o usuário cadastrado no computador principal.

Não é necessário instalar o Markys PDV, editar o arquivo hosts ou executar scripts nos computadores clientes. O servidor anuncia o nome `markyspdv.local` por mDNS. Se a rede ou o Windows não resolver esse nome, use o IPv4 exibido no computador principal, seguido de `:2222` (por exemplo, `http://192.168.0.10:2222`).

## Configuração técnica

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `PDV_ADDR` | `0.0.0.0:2222` | Escuta HTTP nas interfaces de rede do servidor |
| `PDV_DB` | `data/markys-pdv.db` | Banco SQLite operacional no computador principal |
| `PDV_MASTER_DB` | `.markys-master.db` ao lado de `data` | Arquivo separado e protegido da conta Master e do cadastro de empresas |

As regras do Firewall permitem TCP 2222 (PDV) e UDP 5353 (descoberta mDNS) somente no perfil Privado e na sub-rede local. Não encaminhe essa porta para a internet. Se a rede do Windows estiver como Pública, altere para Privada somente quando for uma rede confiável.

## Dados e backups

Os dados operacionais ficam na pasta `data`, incluindo `markys-pdv.db` e os bancos das empresas em `data/companies`. Essa pasta permanece normal, visível e sem proteção especial do Windows. O arquivo exclusivo da conta Master fica fora dela, como `.markys-master.db`, oculto e com permissões restritas ao proprietário, SYSTEM e Administradores locais. O Master não é armazenado dentro de `data`. Antes de atualizar, encerre o servidor e faça backup da pasta `data` normalmente; não é necessário ocultá-la nem alterar suas permissões.

## Limites e validação

- O acesso por `markyspdv.local` depende de mDNS na rede e no dispositivo cliente. Algumas redes Wi-Fi corporativas podem bloquear multicast ou isolamento entre clientes; nesses casos, use o IPv4 do servidor ou ajuste a rede.
- O acesso por outros computadores não tem garantia de desempenho, estabilidade ou compatibilidade para todos os ambientes. Testes locais não equivalem a uma garantia de operação multiestação.
- O servidor usa HTTP sem criptografia; mantenha-o restrito a uma rede local confiável.
- As sessões de login ficam em memória e são encerradas quando o servidor reinicia.
- O computador principal deve permanecer ligado para executar o sistema e manter os dados disponíveis.
- O sistema não emite NFC-e nem NF-e nesta versão.

## Desenvolvimento

```sh
go mod tidy
go run ./cmd/pdv
```

O workflow em `.github/workflows/build-windows.yml` executa os testes e gera o pacote Windows x64.
