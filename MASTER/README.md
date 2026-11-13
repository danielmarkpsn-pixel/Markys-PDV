# MASTER — Identidade permanente do proprietário

No modo compartilhado, existe uma única identidade Master global, provisionada pelo proprietário. Ela não é criada pela tela pública nem pode ser criada por administradores de empresas.

## Armazenamento portátil

A identidade é armazenada fora dos dados operacionais, na pasta irmã `seguranca` da instalação:

```text
MARKYS-PDV/
├── aplicativo (executável)
├── data/
└── seguranca/
    ├── .markys-master.db
    └── .markys-master.provisioned
```

A pasta `data` pode ser apagada sem remover a identidade Master. O marcador de provisionamento impede que a ausência do banco Master gere silenciosamente uma nova conta. Se o banco Master já provisionado desaparecer ou estiver vazio, a inicialização é bloqueada e exige restauração/recuperação autorizada.

## Proteção e limites

- O arquivo é ocultado e recebe permissões restritas no Windows; no Linux/macOS recebe permissões de diretório/arquivo restritas.
- A senha e o código de recuperação são armazenados somente como hashes.
- O marcador não contém senha, token ou código secreto.
- A proteção do sistema operacional reduz alterações acidentais e acesso por outras contas, mas não impede um usuário que controle a mesma conta Administrador do Windows de tomar posse ou apagar arquivos.
- Para distribuição, não versionar senhas, tokens, códigos de recuperação ou credenciais reais neste repositório.

## Administração

- O Master é único e global.
- Somente o Master cadastra empresas e seus administradores.
- Administradores ficam limitados à própria empresa e não podem criar outro Master ou administrador global.
- O modo individual permanece independente e mantém seu fluxo local de configuração.
