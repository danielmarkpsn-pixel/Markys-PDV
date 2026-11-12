# Recuperação do Master

A recuperação é realizada pela tela local de acesso do MARKYS PDV. Ela não cria uma conta Master nova e não recupera contas comuns de Administrador.

1. Abra o MARKYS PDV no próprio computador servidor.
2. Na tela de login, abra **Recuperar acesso do Master**.
3. Informe o usuário Master, o código de recuperação vigente e a nova senha.
4. Após a confirmação, copie o novo código exibido e guarde-o imediatamente em local seguro.
5. Entre novamente com a nova senha.

O código anterior é invalidado automaticamente. Não feche nem atualize a tela antes de guardar o novo código. Se perder o código recém-gerado, será necessário seguir o procedimento de recuperação administrativa da instalação; não existe código padrão ou porta dos fundos.

A rota de recuperação rejeita conexões remotas, limita tentativas e bloqueia novas tentativas por 30 minutos após cinco erros. Nenhum código ou senha é registrado na auditoria.

**Importante:** esta pasta contém documentação, não credenciais. Nunca adicione aqui o código real, senha, token, arquivo `.env` ou cópia do banco.
