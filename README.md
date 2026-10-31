# MARKYS PDV — Ponto de venda e gestão comercial local

O **MARKYS PDV** é uma aplicação de ponto de venda para pequenos ambientes comerciais, desenvolvida para operar em um computador Windows e permitir acesso por navegador em outros computadores da mesma rede local. O servidor é escrito em **Go** e os dados são armazenados em banco **SQLite local**.

## Recursos do sistema

### Frente de caixa e vendas
- Adição de produtos por SKU ou código de barras.
- Controle de quantidade, desconto e formas de pagamento.
- Opção de dividir o pagamento entre duas formas.
- Resumo da compra com itens, subtotal, desconto e total.
- Validação de estoque e registro da venda ao finalizar.
- Consulta das vendas recentes e cancelamento conforme permissões.

### Produtos, categorias e fornecedores
- Cadastro de produtos com SKU, código de barras, descrição, unidade, custo, preço de venda e estoque inicial.
- Associação de categoria e fornecedor ao produto.
- Cálculo visual de margem estimada.
- Consulta e pesquisa de produtos cadastrados.
- Cadastros separados de categorias e fornecedores.

### Estoque e caixa
- Registro de entradas, saídas e ajustes de estoque com justificativa.
- Histórico de movimentações.
- Relatório de estoque e relatório de movimentações em CSV.
- Abertura e fechamento de caixa, com valores esperados e conferência do valor contado.

### Usuários, permissões e auditoria
- Criação de usuários individuais e ativação/desativação de contas.
- Perfis disponíveis: **Administrador**, **Gerente**, **Caixa** e **Estoquista**.
- Validação de permissões na interface e no servidor.
- Registro de operações relevantes na auditoria.
- Download de backup consistente do banco SQLite pela área administrativa.


### Gerador de orçamentos (em desenvolvimento)
- Acesse `http://localhost:8080/orcamentos.html` após entrar no sistema.
- Monte propostas com produtos cadastrados ou itens/serviços digitados, dados do cliente, desconto, validade e observações.
- Salve e consulte orçamentos, marque-os como aprovados/recusados e use a impressão do navegador para salvar em PDF.
- Os orçamentos ficam armazenados no banco SQLite da empresa e não movimentam estoque nem caixa.

## Requisitos

- Windows para executar o binário Windows x64 publicado pelo workflow.
- Navegador atualizado (Chrome, Edge ou equivalente).
- Go 1.23 ou superior caso queira executar a aplicação a partir do código-fonte.

## Executar a partir do código-fonte

Na raiz do repositório:

```sh
go mod tidy
go run ./cmd/pdv
```

No computador servidor, abra [http://localhost:8080](http://localhost:8080).

O workflow do **GitHub Actions**, localizado em `.github/workflows/build-windows.yml`, executa os testes e gera o artefato Windows x64. Consulte a execução mais recente em **Actions** e baixe o artefato gerado quando o trabalho terminar com sucesso.

## Configuração

A aplicação aceita estas variáveis de ambiente:

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `PDV_ADDR` | `0.0.0.0:8080` | Endereço e porta HTTP do servidor |
| `PDV_DB` | `data/markys-pdv.db` | Caminho do banco SQLite |

Para acessar de outro computador da mesma rede, use o endereço IP local do computador servidor com a porta `8080` (por exemplo, `http://IP-DO-SERVIDOR:8080`). Configure o firewall para liberar essa porta somente na rede privada confiável.

## Primeiro acesso

Na primeira execução, a tela inicial solicita a criação da conta de administrador. Não existe senha padrão. Use uma senha forte e exclusiva. O cadastro de novos usuários fica disponível para o administrador autenticado na seção **Usuários e acessos**.

As senhas são armazenadas com hash PBKDF2-HMAC-SHA256 e salt aleatório. O login usa cookie de sessão HttpOnly com validade de 12 horas; as sessões são mantidas em memória e encerradas quando o servidor reinicia. As permissões são verificadas no servidor, não apenas escondidas na interface.

## Banco de dados e atualizações

O banco padrão fica em `data/markys-pdv.db`. Esse arquivo contém dados operacionais, produtos, usuários e registros comerciais.

**Antes de atualizar ou substituir o executável:**
1. Encerre o servidor MARKYS PDV.
2. Baixe um backup pela seção **Auditoria e backup** ou faça uma cópia segura do banco com o servidor parado.
3. Preserve a pasta `data` e o arquivo `markys-pdv.db`.
4. Substitua apenas os arquivos da aplicação que precisam ser atualizados.
5. Inicie novamente e confira o acesso e os dados.

Não apague nem substitua o banco por uma base vazia durante uma atualização, a menos que a intenção seja realmente reiniciar os dados.

## Segurança e limites

- Use o aplicativo apenas em rede local confiável.
- Não exponha diretamente a porta 8080 à internet.
- Proteja os backups, pois eles podem conter informações comerciais e dados pessoais.
- A restauração de backup pela interface ainda não está implementada; planeje e valide a recuperação manual antes de depender dela.

**Situação fiscal:** o MARKYS PDV não emite NFC-e nem NF-e nesta versão. Emissão fiscal exige desenvolvimento e integração específicos, além de validação conforme a legislação, a UF, o regime tributário e o certificado aplicáveis. Os relatórios CSV não são documentos fiscais.

## Estrutura do repositório

- `cmd/pdv/main.go` — servidor HTTP, rotas e operações comerciais.
- `cmd/pdv/auth.go` — autenticação, usuários, permissões, auditoria e backup.
- `cmd/pdv/web/index.html` — interface do sistema.
- `cmd/pdv/web/schema.sql` — estrutura do banco SQLite.
- `.github/workflows/build-windows.yml` — testes e compilação Windows x64.

## Boas práticas para desenvolvimento

Faça alterações em uma branch e confirme que os testes e a compilação terminaram com sucesso antes de publicar uma versão. Não envie ao repositório bancos de dados reais, senhas, backups, credenciais ou dados pessoais de clientes.
