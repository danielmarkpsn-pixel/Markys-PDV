# MARKYS PDV Online — plano técnico SaaS

## Decisões confirmadas

- Produto: MARKYS PDV Online, modalidade SaaS comercial para várias empresas assinantes.
- Cada empresa (tenant) terá dados operacionais isolados das demais.
- A base online começará vazia. Não importar nem copiar o SQLite desktop.
- A edição desktop permanece local, com SQLite, e não depende da infraestrutura online.
- Branch de trabalho: `feature/markys-pdv-online`, criada a partir de `feature/gerador-orcamentos` para preservar o gerador de orçamentos já desenvolvido. Nada foi mesclado em `main`.

## Auditoria inicial do código

O projeto atual é uma aplicação Go com interface web embutida e driver `modernc.org/sqlite`. O banco é inicializado a partir de `cmd/pdv/web/schema.sql` e a inicialização executa comandos SQLite como `PRAGMA`. O código de negócio usa SQL com placeholders `?`, `LastInsertId()`, `COLLATE NOCASE` e funções específicas de SQLite.

A branch de origem já possui uma estrutura de cadastro de empresas, mas ela armazena o caminho de um arquivo SQLite por empresa. Essa solução é adequada ao cenário local existente, mas não é uma arquitetura de produção para o SaaS hospedado. Ela não deve ser reaproveitada como mecanismo de armazenamento online.

As sessões atuais ficam em memória no processo Go. Isso não é suficiente para um SaaS com múltiplas instâncias, reinicializações e escalabilidade horizontal. A autenticação e as sessões online precisarão de armazenamento persistente e cookies seguros.

**Conclusão:** não é seguro simplesmente configurar o aplicativo atual para usar PostgreSQL ou publicar a versão SQLite em hospedagem. Primeiro é necessário separar explicitamente o modo desktop do modo online e adaptar persistência, autenticação e isolamento multiempresa.

## Arquitetura alvo

### Desktop (preservado)

- Continuar usando SQLite local e o fluxo de inicialização atual.
- Continuar funcionando sem internet.
- Não adicionar dependência obrigatória de PostgreSQL, serviço externo ou login SaaS.
- Mudanças compartilhadas só serão aceitas se os testes garantirem que o comportamento desktop permaneça intacto.

### Online (novo)

- Aplicação Go hospedada como serviço web.
- PostgreSQL gerenciado como banco central.
- Uma base online vazia, inicializada por migrações versionadas.
- Modelo multi-tenant com `tenant_id` nas tabelas operacionais e controles de isolamento no banco (preferencialmente Row-Level Security, além da validação na camada Go).
- O tenant ativo será obtido exclusivamente da sessão autenticada no servidor; nunca confiar em um `tenant_id` enviado pelo navegador para autorizar consultas.
- Dados de clientes, produtos, vendas, estoque, caixa, usuários, orçamentos e auditoria serão sempre escopados à empresa.
- Administração da plataforma separada dos administradores de cada empresa.

## SaaS: domínios que precisam existir

1. **Plataforma:** cadastro e administração de empresas, planos, assinaturas, status de cobrança, limites e suporte.
2. **Empresa:** dados cadastrais, usuários, permissões e configurações.
3. **Assinatura:** plano, período, status, datas de renovação e histórico de eventos do provedor.
4. **Acesso:** trial/ativa, atraso, suspensão e cancelamento, com regras explícitas para acesso aos dados e exportação.
5. **Auditoria:** registrar ações administrativas e eventos relevantes de autenticação, cobrança e operações comerciais.

A integração de cobrança (por exemplo, um provedor brasileiro) deve ficar atrás de uma interface de pagamentos. Não armazenar dados de cartão. O provedor e os planos/preços serão definidos antes de ativar cobrança real; até lá, usar ambiente de testes e estados de assinatura controlados.

## Plano de implementação por etapas

### Etapa 1 — fundação e proteção desktop
- Criar configuração explícita de execução `desktop` e `online`.
- Manter o modo desktop como padrão do executável atual.
- Criar testes de regressão do SQLite e da inicialização local.
- Definir interfaces de persistência e adaptar gradualmente os repositórios/consultas sem espalhar condicionais de dialeto pelos handlers.

### Etapa 2 — PostgreSQL e migrações
- Adicionar driver PostgreSQL apenas ao modo online.
- Criar migrações próprias para PostgreSQL, sem executar o schema SQLite no online.
- Tratar diferenças de placeholders, geração de IDs, datas, índices, transações e concorrência.
- Testar migrações do zero e reinicialização sem perda de dados.

### Etapa 3 — multi-tenant seguro
- Criar o catálogo central de tenants e contas de plataforma.
- Migrar tabelas operacionais para escopo por tenant.
- Aplicar Row-Level Security e contexto de tenant por transação/conexão.
- Testar tentativas de leitura e escrita cruzadas entre empresas.
- Garantir que rotas, relatórios, arquivos, backups e orçamentos respeitem o tenant.

### Etapa 4 — identidade e administração SaaS
- Separar superadministrador da plataforma e administrador da empresa.
- Persistir sessões no banco (ou armazenamento compartilhado equivalente), com expiração, revogação e cookies Secure/HttpOnly/SameSite.
- Criar fluxo de cadastro/onboarding de empresa e primeiro administrador.
- Criar console de administração de empresas, planos e assinaturas.

### Etapa 5 — cobrança e limites
- Definir planos, preços, limites de usuários/terminais e período de teste.
- Integrar provedor de pagamento com webhooks assinados, idempotência e reconciliação.
- Bloquear novas operações conforme status da assinatura sem apagar ou ocultar indevidamente os dados da empresa.
- Oferecer exportação e processo de cancelamento.

### Etapa 6 — hospedagem e operação
- Preparar container e configuração de produção.
- Hospedar o serviço Go e PostgreSQL gerenciado (Render é a primeira opção a validar).
- Configurar domínio, HTTPS, segredos, backups automáticos, monitoramento e alertas.
- Validar restauração de backup e atualização/migração em ambiente de homologação.

### Etapa 7 — validação e lançamento
- Testes unitários, integração PostgreSQL e testes de isolamento multi-tenant.
- Testes de concorrência em vendas/estoque/caixa.
- Testes de sessão, permissões, cobrança, suspensão e retomada.
- Piloto com empresas de teste e dados fictícios antes de cadastrar clientes reais.

## Critérios obrigatórios antes de publicar

- Testes desktop SQLite passando sem mudança de comportamento.
- Online inicia com banco vazio e migrações aplicadas.
- Não há compartilhamento de dados entre tenants, inclusive por IDs previsíveis, relatórios ou endpoints de detalhe.
- Sessões continuam válidas entre instâncias e podem ser revogadas.
- Webhooks de cobrança são autenticados e idempotentes.
- Backup e restauração PostgreSQL testados.
- HTTPS, segredos e política de logs sem senhas/tokens/dados sensíveis.
- Nenhum deploy público antes da validação de segurança e isolamento.

## Fora do escopo inicial

- Sincronização automática entre desktop e online.
- Importação automática de bancos SQLite existentes.
- Compartilhamento de banco local via pasta de rede.
- Cobrança real antes da escolha e homologação do provedor.

Esses itens podem ser avaliados em etapas futuras, sem comprometer a separação entre as duas edições.
