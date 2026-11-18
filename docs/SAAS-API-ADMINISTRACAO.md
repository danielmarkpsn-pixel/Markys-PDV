# MARKYS PDV Online — API de administração do tenant

Esta camada é exclusiva do executável `cmd/pdv-saas` e do PostgreSQL. Não altera o executável desktop/SQLite.

## Autenticação

- `POST /api/auth/login`: recebe `company_document`, `username` e `password`; resolve a empresa no servidor e emite cookie de sessão HttpOnly.
- `GET /api/auth/status`: verifica sessão e situação do usuário/empresa/assinatura.
- `POST /api/auth/logout`: revoga a sessão atual.
- O login novo revoga a sessão anterior do mesmo usuário.
- O login recebe limite inicial de 10 requisições por IP em 5 minutos. O limite é por instância e não substitui proteção distribuída no gateway.

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
- O limite de tentativas atual é local à instância. Para múltiplas instâncias, mover a contagem para Redis/gateway.
- Ainda falta autenticação e API do administrador da plataforma para provisionar empresas, criar o primeiro proprietário, ativar contratos e alterar `seat_limit`.
- Ainda faltam testes HTTP de integração contra PostgreSQL para fluxo completo de login, criação de usuários, limite concorrente e substituição de sessão.
- Os módulos de PDV e a interface web ainda não foram conectados a esta API.
- Não publicar em produção antes de completar os itens acima e configurar HTTPS, origem confiável/proxy, segredo de sessão e credenciais PostgreSQL.

