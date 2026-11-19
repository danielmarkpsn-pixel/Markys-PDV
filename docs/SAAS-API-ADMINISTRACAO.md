# MARKYS PDV Online — API de administração do tenant

Esta camada é exclusiva do executável `cmd/pdv-saas` e do PostgreSQL. Não altera o executável desktop/SQLite.

## Autenticação

- `POST /api/auth/login`: recebe `company_document`, `username` e `password`; resolve a empresa no servidor e emite cookie de sessão HttpOnly.
- `GET /api/auth/status`: verifica sessão e situação do usuário/empresa/assinatura.
- `POST /api/auth/logout`: revoga a sessão atual.
- O login novo revoga a sessão anterior do mesmo usuário.
- O login recebe limite inicial de 30 requisições por IP em 5 minutos. O limite é por instância e não substitui proteção distribuída no gateway.

## Administração da empresa

Todos os endpoints abaixo exigem sessão válida e perfil `owner` ou `admin`.

- `GET /api/admin/users`: lista usuários da própria empresa.
- `POST /api/admin/users`: cria usuário com perfil `admin`, `manager`, `cashier` ou `stockkeeper`. A senha precisa atender à política de 10 a 256 caracteres.
- `PUT /api/admin/users/{id}`: altera nome, e-mail, senha, perfil ou situação ativa. O usuário proprietário principal não pode ser alterado por esta rota.
- `GET /api/admin/license`: consulta logins contratados, ativos, disponíveis e status da assinatura.

O número de logins contratados é somente leitura para o administrador da empresa. A criação/reativação é validada também pelo gatilho PostgreSQL, que serializa a contagem e bloqueia excedentes mesmo em requisições concorrentes.

## Segurança e limites atuais

- As operações de leitura/escrita de usuários usam transação com `app.tenant_id` para respeitar RLS.
- A identidade do tenant vem da sessão validada, nunca de parâmetro enviado pelo navegador.
- O limite de tentativas atual é local à instância e cobre somente login da empresa, login da plataforma e bootstrap inicial (30 tentativas por IP em 5 minutos); as demais rotas não consomem esse limite. Para múltiplas instâncias, mover a contagem para Redis/gateway.
- O console e a API do administrador da plataforma já provisionam empresas, planos e limites; ainda faltam MFA, auditoria global dedicada e recuperação segura de acesso.
- Os testes de integração HTTP cobrem administração de usuários e estoque; ainda faltam cenários de concorrência em venda/caixa, cobrança e restauração de backup.
- A interface web inicial já está conectada à autenticação, administração, catálogo e estoque. O fluxo transacional de PDV ainda não foi conectado.
- Não publicar em produção antes de completar os itens acima e configurar HTTPS, origem confiável/proxy, segredo de sessão e credenciais PostgreSQL.

## Administrador da plataforma

A API de plataforma é separada da administração de cada empresa. Ela usa cookie próprio `markys_platform_session`, sessões persistidas no PostgreSQL e autenticação independente do usuário de uma empresa.

Antes do primeiro uso, configure o segredo forte `SAAS_PLATFORM_BOOTSTRAP_TOKEN` no ambiente do serviço. O bootstrap só é aceito enquanto não existir nenhum registro em `platform_admins`; exige o cabeçalho `X-Platform-Bootstrap-Token` e deve ser executado uma única vez. Depois da criação do primeiro administrador, remova essa variável do ambiente.

- `POST /api/platform/bootstrap`: cria o primeiro administrador global (somente uma vez).
- `POST /api/platform/login`: autentica o administrador e emite cookie HttpOnly.
- `GET /api/platform/status`: verifica a sessão global.
- `POST /api/platform/logout`: revoga a sessão global.
- `GET /api/platform/plans` e `POST /api/platform/plans`: consulta e cadastra planos.
- `GET /api/platform/tenants`: lista empresas, situação da assinatura, plano, limite contratado e usuários ativos.
- `POST /api/platform/tenants`: provisiona empresa, assinatura e primeiro usuário proprietário em uma única transação.
- `PUT /api/platform/tenants/{id}`: altera situação da empresa, situação da assinatura e/ou quantidade contratada de logins.

O limite de logins é controlado exclusivamente pela API da plataforma e também pelo gatilho PostgreSQL no momento de criação/reativação de usuários. O endpoint de administração da empresa continua somente leitura para licença.

O console visual da plataforma está disponível em `/plataforma.html` e consome essas APIs. O bootstrap inicial deve ser feito em ambiente controlado: configure o segredo forte no servidor, use o formulário protegido apenas durante a criação do primeiro administrador e remova a variável `SAAS_PLATFORM_BOOTSTRAP_TOKEN` imediatamente após o bootstrap. Ainda são necessários controles operacionais adicionais (auditoria global dedicada, MFA, recuperação segura de acesso e política de rotação de segredos) antes de uso comercial em produção.

## Catálogo da empresa (API)

Os três recursos abaixo exigem sessão válida. Consultas são permitidas a usuários autenticados; criação, edição e inativação exigem perfil `owner`, `admin` ou `manager`. Todas as operações usam transações com contexto RLS do tenant obtido da sessão.

- Categorias: `GET/POST /api/catalog/categories`, `PUT/DELETE /api/catalog/categories/{id}`.
- Fornecedores: `GET/POST /api/catalog/suppliers`, `PUT/DELETE /api/catalog/suppliers/{id}`.
- Produtos: `GET/POST /api/catalog/products`, `PUT/DELETE /api/catalog/products/{id}`.
- A listagem de produtos aceita `?q=` para pesquisar nome, código ou código de barras e retorna até 250 registros por consulta.
- DELETE realiza inativação lógica; não remove fisicamente registros relacionados.
- Preços e custos são armazenados em centavos inteiros. Estoque não pode ser alterado pela edição cadastral do produto; terá fluxo próprio de movimentação para preservar o histórico.

As APIs já estão conectadas ao serviço Go SaaS, mas ainda não estão conectadas à interface visual do PDV. Também faltam testes específicos de permissões por perfil e operações de estoque/lotes/validade.

## Estoque e movimentações (API)\n\n- `GET /api/inventory/stock`: posição de estoque, com pesquisa opcional `?q=`.\n- `GET /api/inventory/movements?limit=100`: histórico recente, limite de 1 a 500.\n- `POST /api/inventory/adjustments`: registra `entry`, `exit` ou `count`; quantidade aceita até três casas decimais.\n- Entradas e saídas atualizam o saldo e geram trilha em `tenant_stock_movements`; saídas que excedem o saldo são recusadas.\n- Para produtos com controle de lote, entrada/saída exige `lot_code`; se também houver controle de validade, a entrada exige `expires_on`. Saída valida saldo do lote. Contagem direta é bloqueada para produto controlado por lote.\n- Ajustes são permitidos a `owner`, `admin`, `manager` e `stockkeeper`; consulta exige sessão. Todas as operações são transacionais e isoladas por tenant.\n- A migração `0003_inventory_integrity.sql` impede configurar validade sem controle de lote.\n\nA interface web da empresa inclui a tela Estoque para posição, entradas, saídas, inventário e histórico.\n\n## Caixa e vendas (API)\n\n- `GET /api/pos/cash?terminal_code=PDV-01`: consulta o caixa aberto do terminal e o dinheiro esperado.\n- `POST /api/pos/cash/open`: abre caixa com código do terminal e fundo inicial em centavos. Um índice único impede dois caixas abertos no mesmo terminal da empresa.\n- `POST /api/pos/cash/close`: fecha o caixa com o valor contado; persiste valor esperado e diferença. O esperado soma o fundo inicial e pagamentos em dinheiro de vendas concluídas.\n- `GET /api/pos/sales?limit=50`: lista vendas recentes.\n- `POST /api/pos/sales`: conclui venda em uma transação, usando preço cadastrado, validando pagamento integral, caixa aberto e saldo. Deduz estoque e registra movimentação de venda; para produtos por lote, baixa primeiro lotes não vencidos por FEFO (validade mais próxima).\n- A tela inicial `/` inclui abertura/fechamento do caixa, carrinho, pagamento por uma forma e histórico de vendas. Nesta primeira versão não há desconto na tela, pagamento dividido, troco, cancelamento/estorno, emissão fiscal ou integração de TEF/adquirente.\n- As operações de caixa e venda exigem perfil `owner`, `admin`, `manager` ou `cashier`; o estoque continua acessível ao perfil `stockkeeper`.\n\n## Interface web inicial

O executável `cmd/pdv-saas` agora serve os arquivos estáticos embutidos em `cmd/pdv-saas/web`:
- `/`: login da empresa e área inicial com produtos, categorias, fornecedores, usuários e consulta de licença.
- `/plataforma.html`: console do administrador global para bootstrap inicial, planos e provisionamento/gestão de empresas.

A interface usa os endpoints descritos neste documento e mantém o login em cookies HttpOnly. O fluxo visual inicial de venda/PDV e caixa já foi conectado. Ainda faltam pagamento dividido/troco, cancelamento e estorno, compras, fiscal e integrações. Portanto, esta entrega é a primeira interface de acesso e cadastros SaaS, não uma versão completa pronta para operação comercial.


## Clientes da empresa (API e interface)

- `GET /api/customers?q=`: pesquisa por nome, CPF/CNPJ ou telefone; retorna até 300 registros da empresa autenticada.
- `POST /api/customers`: cadastra nome, CPF/CNPJ, telefone, e-mail e endereço. CPF/CNPJ é normalizado para dígitos e único dentro do tenant.
- `PUT /api/customers/{id}`: atualiza os dados cadastrais do cliente ativo.
- `DELETE /api/customers/{id}`: realiza inativação lógica, preservando vendas e histórico.
- Consultas exigem sessão; cadastro, edição e inativação exigem `owner`, `admin` ou `manager`.
- O PDV permite associar um cliente ativo à venda; cliente inativo não pode ser usado em nova venda. Consumidor final continua disponível sem cadastro.
- A migração `0005_customer_lifecycle.sql` adiciona situação e data de atualização e índice de pesquisa.
- A tela `/customers.html` oferece cadastro, pesquisa, edição e inativação.


## Pagamentos divididos e troco

- A venda aceita até 10 parcelas de pagamento em diferentes meios (dinheiro, PIX, débito, crédito, voucher ou outro).
- `amount_cents` representa o valor efetivamente destinado à quitação da venda.
- Para dinheiro, `received_cents` representa o valor entregue pelo cliente e pode ser maior que `amount_cents`; a diferença é registrada como troco.
- Para meios não monetários, o valor recebido deve ser igual ao valor aplicado.
- A soma de `amount_cents` precisa corresponder exatamente ao total da venda.
- O caixa soma somente o dinheiro líquido aplicado à venda, sem contar o valor devolvido como troco.
- A migração `0006_payment_tender_change.sql` registra `received_cents` e `change_cents`.


## Cancelamento e estorno integral de vendas

- `POST /api/pos/sales/{id}/refund` exige sessão e perfil `owner`, `admin` ou `manager`.
- O corpo exige `reason` com motivo de 5 a 500 caracteres. O estorno é integral nesta etapa; não há estorno parcial.
- A operação bloqueia a venda com `FOR UPDATE`, exige status `completed` e impede segundo estorno por restrição única no banco.
- Pagamentos são estornados pelos valores efetivamente aplicados à venda e pelos mesmos meios originais. O troco entregue na venda não é somado ao valor do reembolso.
- O estoque é recomposto usando as movimentações vinculadas à venda; para produtos com lote, a quantidade retorna exatamente aos lotes consumidos.
- Venda, estoque, lotes, pagamentos de estorno e auditoria são alterados na mesma transação. Qualquer falha reverte a operação inteira.
- Vendas antigas sem vínculo de movimentação de estoque criado pela migração `0007_sale_refunds.sql` são recusadas com segurança; não se tenta adivinhar o lote ou saldo original.
- A conciliação do caixa desconta pagamentos em dinheiro estornados. Para devolver dinheiro, o caixa vinculado à venda precisa continuar aberto; o endpoint bloqueia estorno em dinheiro após o fechamento, evitando alterar retroativamente uma conferência já encerrada. Nesse caso, a devolução deve seguir procedimento financeiro manual autorizado; abrir outro caixa não reabre nem altera o caixa original. Estornos exclusivamente não monetários podem ser registrados após o fechamento.
