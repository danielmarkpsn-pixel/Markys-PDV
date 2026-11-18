# MARKYS PDV Online — licenças e sessões exclusivas

## Regras de produto aprovadas

### 1. Vagas de usuários por empresa

- Cada empresa possui um limite de logins definido no contrato/assinatura (tenant_subscriptions.seat_limit).
- Cada conta pertence a uma única empresa.
- O primeiro administrador da empresa também ocupa uma vaga.
- Contas ativas consomem vagas. Desativar uma conta libera a vaga, sem apagar seu histórico.
- A interface deve exibir vagas contratadas, vagas ocupadas e vagas disponíveis.
- Ao atingir o limite, a criação e a reativação de usuários devem ser bloqueadas tanto na interface quanto na API e no banco.
- Aumentar ou reduzir o limite é uma operação exclusiva do administrador da plataforma, com registro de auditoria.
- Reduzir o limite abaixo da quantidade de contas ativas não apaga nem desativa usuários automaticamente. A empresa fica acima do limite e não pode ativar/criar outras contas até regularizar.
- A tentativa de criar usuários em assinatura suspensa/cancelada ou sem licença deve ser negada.

A verificação de vagas precisa ser transacional. O serviço inicia uma transação, bloqueia a linha da assinatura com SELECT ... FOR UPDATE, conta as contas ativas e insere o novo usuário na mesma transação. A trigger da migration 0001_saas_access_foundation.sql também aplica o limite para evitar que uma rota esquecida ou uma corrida de requisições ultrapasse o contratado.

### 2. Sessão exclusiva por login

- Cada usuário pode ter somente uma sessão não revogada.
- Se o mesmo login entrar em outro navegador/aparelho, a nova autenticação revoga a sessão anterior e cria a nova sessão na mesma transação.
- A sessão anterior passa a receber HTTP 401 na próxima requisição protegida e a interface deve exibir: **"Sua conta foi acessada em outro dispositivo. Entre novamente para continuar."**
- A interface deve verificar a sessão periodicamente (heartbeat curto, por exemplo a cada 20–30 segundos) para detectar a revogação mesmo quando o usuário estiver parado.
- A sessão deve ser validada no servidor em cada requisição protegida, conferindo hash do token, usuário ativo, empresa ativa, assinatura autorizada e expiração.
- O cookie deve ser HttpOnly, Secure, SameSite=Lax ou mais restritivo quando compatível, com token aleatório forte. O banco armazena somente o hash SHA-256 do token, nunca o token em texto puro.
- Logout, redefinição de senha, desativação de usuário e suspensão da empresa revogam as sessões aplicáveis.
- A unicidade parcial uq_tenant_sessions_one_active_per_user é uma proteção adicional no banco. A troca precisa revogar a sessão antiga antes de inserir a nova, dentro da mesma transação.

### 3. Isolamento e autorização

- O tenant_id vem da sessão autenticada no servidor; nunca é aceito como autoridade a partir do corpo, query string ou cabeçalho enviado pelo navegador.
- Cada transação operacional define o contexto com set_config('app.tenant_id', tenant_id, true) e executa as consultas dentro dessa transação.
- As tabelas de sessões e auditoria usam RLS com FORCE ROW LEVEL SECURITY. A tabela de usuários usa RLS sem FORCE para permitir que a função SECURITY DEFINER da trigger de limite conte usuários; por isso, a role de runtime deve obrigatoriamente ser separada da proprietária das tabelas, sem SUPERUSER e sem BYPASSRLS.
- A conexão da aplicação não deve ser superuser nem possuir BYPASSRLS.
- Administração da plataforma e administração da empresa são identidades e permissões separadas.
- Aumentos de vagas, alterações de assinatura, suspensão e retomada devem gerar eventos de auditoria.

## Contratos de API previstos

- GET /api/account/license: limite, contas ativas, vagas disponíveis e situação da assinatura.
- POST /api/account/users: criação sujeita ao limite transacional.
- PATCH /api/account/users/:id/status: ativação/desativação, sujeita ao limite.
- POST /api/auth/login: substitui a sessão anterior do mesmo usuário.
- POST /api/auth/logout: revoga a sessão atual.
- GET /api/auth/session: heartbeat/validação de sessão.
- Rotas administrativas da plataforma para alterar seat_limit e situação da assinatura, protegidas por papel de plataforma e auditoria.

Os caminhos são uma especificação inicial; devem ser harmonizados com o roteamento final antes da implementação.

## Testes de aceitação obrigatórios

1. Empresa com limite 5 consegue criar cinco contas ativas e recebe conflito ao tentar criar a sexta.
2. Duas requisições simultâneas para a última vaga resultam em apenas uma criação bem-sucedida.
3. Desativar uma conta libera uma vaga; reativá-la sem vaga disponível é bloqueado.
4. Reduzir o limite abaixo do uso atual não exclui contas, mas impede novas ativações/cadastros.
5. Empresa suspensa não autentica usuários nem cria sessões.
6. Um login no segundo aparelho revoga o primeiro; o primeiro recebe 401 no heartbeat/requisição seguinte.
7. Duas empresas com usuários/produtos/vendas de IDs semelhantes não conseguem ler nem alterar dados cruzados.
8. Logout, troca de senha e desativação de usuário revogam as sessões pertinentes.
9. Reiniciar ou escalar horizontalmente o serviço não perde nem duplica o estado das sessões.

## Estado desta entrega

A branch agora contém o driver PostgreSQL isolado, configuração explícita de conexão, executor controlado e idempotente de migrations, contexto RLS transacional e serviço de autenticação de credenciais com PBKDF2-SHA256 compatível com os hashes existentes e criação/validação/revogação de sessões persistentes. O serviço substitui a sessão anterior do mesmo usuário na mesma transação e armazena somente SHA-256 do token. Os testes de CI verificam a migration via Go, limite de vagas, sessão única e isolamento RLS com role sem SUPERUSER/BYPASSRLS. Ainda não há integração dessas rotinas com as rotas HTTP e o fluxo de login do PDV; o runtime comercial segue SQLite. Não publicar como SaaS nem conectar banco de produção até concluir autenticação por senha, autorização, camada de dados multi-tenant e integração das rotas.
