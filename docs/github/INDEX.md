# GitHub

> **Assunto:** índice da integração com o GitHub · **Componente:** Motor · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [acoes-por-nivel.md](acoes-por-nivel.md) | o que a mesa e o Grão-Mestre podem fazer no GitHub, épico como rótulo `epico`, como o motor confere que o alvo é da mesa, envio pelo espelho, push forçado, conflitos, merge | ao implementar `mestre issue`, `mestre pr` ou `mestre gh` |
| [identidades-e-apps.md](identidades-e-apps.md) | Apps fixos e por classe, nomes, limite de 100, permissões mínimas, tokens, autoria dos commits, App temporário "Mestre" com [Classe], workflows | ao mexer em credenciais do GitHub, commits ou permissões de App |
| [criacao-de-apps.md](criacao-de-apps.md) | link de criação preparado pelo Grão-Mestre, fluxo de manifesto no navegador do Dono, instalação nos repositórios, repositórios e permissões novos | ao implementar `mestre app create` ou a instalação |
| [eventos.md](eventos.md) | consulta periódica condicional e adaptativa, endpoints, ETag, deduplicação, entrega na caixa de entrada | ao implementar o acompanhamento do GitHub |
| [aprovacao-no-github.md](aprovacao-no-github.md) | review do App aprovador, teste na sessão zero, plano B por ruleset, aprovação humana e delegação, credencial pessoal por device flow | ao implementar reviews, merges ou a credencial dos usuários |
