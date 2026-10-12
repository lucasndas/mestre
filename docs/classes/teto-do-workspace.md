# Teto do workspace

> **Assunto:** o máximo de permissões e de custo que qualquer classe de um workspace pode ter · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Dar liberdade aos Mestres de mesa sem perder o controle: eles criam classes da mesa sem pedir aprovação, desde que fiquem **dentro do teto** do workspace. O teto também limita o custo. Ampliar o teto só por decreto.

## Comportamento

### Arquivo

Fica em `.mestre/regras/teto.yaml` ([estrutura de `.mestre/`](../sessao-zero/workspace.md#estrutura-de-mestre)), no clone principal do workspace no Core. Usa o mesmo formato do [`permissions.yaml`](permissoes.md#formato-do-permissionsyaml), lido como **máximo**, mais o bloco de custo:

```yaml
version: 1
permissions:
  files:
    write: ["**"]
    deny: ["infra/**", ".github/workflows/**"]
  commands:
    ask: ["npm publish *"]
    deny: ["docker *", "terraform *"]
  git:
    request_push: false     # nenhuma classe pede envio; o Mestre da mesa pode, pelo nível dele
  network:
    internet: true
    domains: ["registry.npmjs.org", "github.com"]
  cli:
    allow: ["issue *", "pr comment", "pr reply", "pr status"]
cost:                       # na unidade de limites-de-uso.md
  per_table: 50
  per_day: 100
```

- Como máximo: `files.write`, `network.domains`, `cli.allow` e, se existir, `commands.allow` dizem o que **pode** ser dado; o que fica fora é negado. `deny` e `ask` valem para todas as classes. Campo ausente não limita nada.
- **Teto padrão** (criado na [sessão zero do workspace](../sessao-zero/workspace.md)): sem restrição além das regras fixas, com `git.request_push: false` e sem bloco `cost` (valem só os limites de [limites de uso](../plataforma/limites-de-uso.md)).

### Onde o teto age

- **Ao criar ou editar uma classe** (de workspace ou de mesa): o motor confere se ela cabe no teto e recusa mostrando o que passa. A conferência de padrões é conservadora; serve para avisar cedo.
- **A cada decisão:** o teto entra nas regras efetivas de cada personagem, combinado com as outras camadas como diz [precedência](../arquitetura/precedencia.md), e é aplicado pelo [pipeline de permissões](permissoes.md). Por isso vale mesmo para uma classe antiga ou editada à mão. Cada regra efetiva guarda a camada de origem, e a negação diz "teto do workspace".
- **Custo:** `per_table` e `per_day` (do workspace) são o máximo que os limites configurados podem ter. O comportamento de aviso e pausa está em [limites de uso](../plataforma/limites-de-uso.md). Passar do teto de custo exige a aprovação de um usuário Administrador ou acima; o Grão-Mestre, aprovador delegado, não aprova isso ([política](../aprovacoes/politica.md)).

### Quem muda o teto

| Mudança | Quem | Como |
|---|---|---|
| restringir | Administrador ou Dono | painel ([administração](../painel/administracao.md)) |
| ampliar | Administrador ou Dono | só por decreto permanente ([decretos](../telegram/decretos.md)); ampliar pelo painel cria esse decreto, com o autor |
| propor | Grão-Mestre ou Mestre da mesa | pedem ao usuário; nunca ampliam sozinhos |

Níveis: [usuários e níveis](../telegram/usuarios-e-niveis.md).

### Arquivo mudado por fora

Se `teto.yaml` mudar sem passar pelo painel ou por decreto (edição manual, ou PR integrado quando `.mestre/` é versionado), o motor continua aplicando a última versão aprovada (o hash fica no banco) e avisa os Administradores. Se a nova versão só restringe, um Administrador aprova pelo painel; se amplia, só vale com decreto.

### Teto reduzido com classes que já existem

Classes que passam do novo teto aparecem como "acima do teto" no `mestre class check` e no painel. Nada é apagado: o pipeline simplesmente corta o que passou, com o motivo.

## Regras e erros

- `teto.yaml` inválido: vale a última versão aprovada, com alerta aos Administradores.
- Decreto que amplia o teto afrouxa a segurança: pede a confirmação reforçada com PIN ([segurança do canal](../telegram/seguranca-do-canal.md)).
- Mestre da mesa nunca libera para uma classe algo que o teto proíbe, nem por herança de App ou cópia de grimório.

## Critérios de aceite

- **CA-TETO-01** · *rápida* — **Dado** um teto com `network.domains: ["github.com"]`, **quando** o Mestre da mesa cria uma classe que acessa `example.com`, **então** a criação é recusada e a mensagem cita `example.com`.
- **CA-TETO-02** · *rápida* — **Dado** um teto que nega `infra/**` e uma classe que escreve em `**`, **quando** o personagem edita `infra/main.tf`, **então** a decisão é negar com o motivo "teto do workspace".
- **CA-TETO-03** · *completa* — **Dado** `teto.yaml` editado à mão para liberar `docker *`, **quando** o motor percebe a mudança, **então** segue negando `docker *`, avisa os Administradores e só aplica a nova versão depois de um decreto.
- **CA-TETO-04** · *completa* — **Dado** uma mesa no teto de custo, **quando** o Grão-Mestre tenta aprovar mais gasto, **então** a aprovação é recusada e o pedido vai aos usuários Administrador ou acima.
- **CA-TETO-05** · *rápida* — **Dado** uma classe que escreve em `src/**`, **quando** o teto passa a negar `src/**`, **então** `class check` mostra a classe como "acima do teto".

## Depende de

- [Permissões](permissoes.md) e [classes](classes.md)
- [Precedência](../arquitetura/precedencia.md)
- [Decretos](../telegram/decretos.md) e [usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Política de aprovação](../aprovacoes/politica.md)
- [Administração no painel](../painel/administracao.md)
