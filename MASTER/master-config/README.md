# Configuração da conta Master

A conta Master é única, permanente e criada pelo proprietário da instalação. Ela não é criada pelo formulário público de primeiro acesso.

## Seleção do modo de operação

- **Modo individual (padrão):** mantenha `MARKYS_MODE` sem definir. O cadastro inicial e a administração local existentes continuam disponíveis.
- **Modo compartilhado:** defina `MARKYS_MODE=shared`. Nesse modo, a inicialização exige a conta Master e aplica a hierarquia de permissões descrita abaixo.

## Configuração inicial no Windows

Antes de iniciar o MARKYS PDV pela primeira vez, configure no ambiente do processo:

- `MARKYS_MASTER_USERNAME`: usuário Master (3 a 64 caracteres).
- `MARKYS_MASTER_NAME`: nome de exibição (opcional; padrão Proprietário).
- `MARKYS_MASTER_PASSWORD`: senha inicial (10 a 256 caracteres).
- `MARKYS_MASTER_RECOVERY_CODE`: código secreto inicial (mínimo 16 caracteres).

Exemplo de configuração temporária no PowerShell (substitua os valores por segredos fortes e exclusivos):

```powershell
$env:MARKYS_MODE = "shared"
$env:MARKYS_MASTER_USERNAME = "proprietario"
$env:MARKYS_MASTER_NAME = "Proprietário"
$env:MARKYS_MASTER_PASSWORD = "SENHA-FORTE-EXCLUSIVA"
$env:MARKYS_MASTER_RECOVERY_CODE = "CODIGO-SECRETO-LONGO-E-EXCLUSIVO"
.\MARKYS-PDV-windows-amd64.exe
```

Não use os valores ilustrativos literalmente. Em produção, configure os segredos no mecanismo protegido de variáveis do Windows/serviço que inicia o aplicativo. Não coloque segredos em arquivos versionados, instaladores públicos, scripts compartilhados ou no GitHub.

Na primeira inicialização, o sistema grava somente hashes PBKDF2-SHA256 da senha e do código no banco SQLite central. Depois da criação, as variáveis não substituem nem redefinem a conta existente.

## Proteções

No modo compartilhado, existe uma única identidade Master, com identificador fixo.
- O Master não pode ser criado, removido, bloqueado ou rebaixado pela administração comum.
- No modo compartilhado, somente o Master cadastra e inativa empresas e pode criar contas de Administrador adicionais em cada empresa.
- Administradores comuns gerenciam usuários de sua própria empresa e podem criar, bloquear ou reativar apenas perfis inferiores (Gerente, Operador, Estoquista legado e Consulta). Não podem criar ou administrar outra conta de Administrador ou Master.
- Somente o Master pode remover o acesso de usuários. Para preservar vendas, caixas e auditoria, a remoção desativa a conta e altera o identificador de login; os vínculos históricos permanecem no banco.
- A recuperação exige o código secreto, aceita somente chamadas locais, limita tentativas a cinco e aplica bloqueio de 30 minutos.
- Após uma recuperação válida, o código anterior é invalidado e um novo código é gerado. A API o exibe uma única vez; guarde-o imediatamente em local seguro.
- O código nunca é escrito na auditoria nem armazenado em texto puro.

O proprietário da máquina e do arquivo SQLite continua tendo controle técnico sobre a infraestrutura. Proteja o Windows, a pasta de dados e os backups com permissões do sistema operacional.
