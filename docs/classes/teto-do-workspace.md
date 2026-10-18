# Teto do workspace

> **Assunto:** o máximo de permissões e de custo que qualquer classe de um workspace pode ter · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Dar liberdade aos Mestres de mesa sem perder o controle: eles criam classes da mesa sem pedir aprovação, desde que fiquem **dentro do teto** do workspace. O teto também limita o custo. Ampliar o teto só por decreto.

## Comportamento

### Arquivo

Fica em `.mestre/regras/teto.yaml` ([estrutura de `.mestre/`](../sessao-zero/workspace.md#estrutura-de-mestre)), no clone principal do workspace na Cidadela. Usa o mesmo formato do [`permissions.yaml`](formato-das-permissoes.md), lido como **máximo**, mais o bloco de custo:

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
    workflows: never        # nenhuma classe envia mudanças no CI
  network:
    internet: true
    domains: ["registry.npmjs.org", "github.com"]
  cli:
    allow: ["issue *", "pr comment", "pr reply", "pr status"]
  operator: false           # nenhuma classe usa o Operador do PC; só o Dono liga
cost:                       # máximo por unidade de limites-de-uso.md: usd | tokens | hours
  usd: {per_table: 50, per_day: 100}
  hours: {per_table: 20}
```

- Como máximo: `files.write`, `network.domains`, `cli.allow` e, se existirem, `commands.allow` e `tools.allow` dizem o que **pode** ser dado; o que fica fora é negado: com `commands.allow` ou `tools.allow` no teto, o `default` do bloco nas regras efetivas é `deny` (origem `ceiling`), e uma classe que escreve `default: allow` ou `ask` nesse bloco passa do teto. `deny` e `ask` valem para todas as classes. Campo ausente não limita nada, menos `operator`: ausente vale `false`, porque uma classe só vai à base Operador do PC com `operator: true` no teto ([formato](formato-das-permissoes.md)).
- Leitura estrita, como a do `permissions.yaml`: `version: 1` só no topo (o bloco `permissions` não tem `version` próprio); campo desconhecido, mais de um documento, unidade fora de `usd | tokens | hours` e custo negativo, infinito ou (em `tokens`) fracionário tornam o arquivo inválido. O código é `internal/classes` (`ParseCeiling`, `CeilingKeeper`).
- **Teto padrão** (criado na [sessão zero do workspace](../sessao-zero/workspace.md)): sem restrição além das regras fixas (não restringe a internet), com `git.request_push: false`, `git.workflows: never`, `operator: false`, sem bloco `cost` (valem só os limites de [limites de uso](../plataforma/limites-de-uso.md)) e com um `files.deny` de segredos: `**/.env*`, `**/id_rsa*`, `**/id_ed25519*`, `**/id_ecdsa*`, `**/.npmrc`, `**/credentials*.json`, `**/service-account*.json`, `**/*.pem` e `**/*.key` (`.env.production`, `.env.development` e `.envrc` casam, e o `.env.example` também: o formato não tem exceção, o agente raramente precisa lê-lo, e quem precisar dele troca o glob por outros mais estreitos, o que amplia o teto e só vale por decreto; vale a partir do M5, [CA-TETO-08](#critérios-de-aceite), que substitui o trecho do `.env.example` de CA-TETO-07). Como todo `files.deny`, vale para a ferramenta de leitura e para caminho literal no shell, com o corte nativo `Read(<glob>)` só no Claude ([reforço nativo](reforco-nativo.md)): no Antigravity a leitura (`view_file`) é negada pelo despachante, e o Codex não tem ferramenta de leitura, então lá o `deny` pega o caminho literal no comando e fica com o limite conhecido do shell sem caminho literal ([permissões](permissoes.md#regras-e-erros)); assim nenhum token do projeto entra no transcript do provedor sem o Dono ter escrito o `deny`. Os mesmos globs estão em `permissoes_e_segredos` do [arquivo de política](../aprovacoes/arquivo-de-politica.md).

### Onde o teto age

- **Ao criar ou editar uma classe** (de workspace ou de mesa): o motor confere se ela cabe no teto e recusa mostrando o que passa (`ceiling_denied`), seja quem for o autor. A conferência de padrões é conservadora; serve para avisar cedo. Passa do teto: um padrão que o máximo não contém com certeza; uma concessão que um `deny` ou `ask` do teto pega inteira (`src/**` com o teto negando `src/**`); `default`, flags e `workflows` mais livres que os do teto; qualquer domínio quando o teto lista domínios. Uma concessão que só se sobrepõe a um `deny` do teto (`**` com o teto negando `infra/**`) cabe: o pipeline corta a sobreposição a cada decisão. Contam só os campos escritos na classe; o padrão do formato que ela deixa implícito ([ADR 0025](../decisoes/0025-padroes-seguem-os-provedores.md): `default` ausente em `ask`, `files.write` ausente no worktree inteiro, `internet` ausente com qualquer domínio, `git.commit` e `request_push` ausentes como pedido) é padrão do sistema, não ampliação: não conta ao salvar e é cortado ao montar as regras efetivas (o teto com `internet: false` deixa a classe sem internet; o com `git.commit: false`, sem commit).
- **A cada decisão:** o teto entra nas regras efetivas de cada personagem, combinado com as outras camadas como diz [precedência](../arquitetura/precedencia.md), e é aplicado pelo [pipeline de permissões](permissoes.md). Por isso vale mesmo para uma classe antiga ou editada à mão. Cada regra efetiva guarda a camada de origem, e a negação diz "teto do workspace".
- **Custo:** em cada unidade presente (`usd`, `tokens`, `hours`), `per_table` e `per_day` (do workspace) são o máximo que os limites configurados naquela unidade podem ter; unidade ausente não tem teto. `per_table` vale também para os limites de missão (a missão é parte da mesa); o limite total do workspace não tem teto próprio. Sem limite configurado, o próprio teto vale como limite da mesa ou do dia. O comportamento de aviso e pausa está em [limites de uso](../plataforma/limites-de-uso.md). Passar do teto de custo exige a aprovação de um usuário Administrador ou acima; o Grão-Mestre, aprovador delegado, não aprova isso ([política](../aprovacoes/politica.md)): a aprovação dele é recusada com `human_required` e o pedido vai aos Administradores e ao Dono; a de um Aprovador também é recusada.

### Quem muda o teto

| Mudança | Quem | Como |
|---|---|---|
| restringir | Administrador ou Dono | painel ([administração](../painel/administracao.md)) |
| ampliar | Administrador ou Dono | só por decreto permanente ([decretos](../telegram/decretos.md)); ampliar pelo painel cria esse decreto, com o autor |
| ligar `operator` ([Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)) | só o Dono | decreto `ceiling` do Dono, com PIN; ligar pelo painel cria esse decreto |
| propor | Grão-Mestre ou Mestre da mesa | pedem ao usuário; nunca ampliam sozinhos |

Quem muda é conferido antes de qualquer confirmação: um decreto `ceiling` de um Administrador que liga `operator` é recusado sem chegar ao PIN; ampliar pelo painel devolve a necessidade do decreto, que o painel cria.

Níveis: [usuários e níveis](../telegram/usuarios-e-niveis.md).

### Arquivo mudado por fora

Se `teto.yaml` mudar sem passar pelo painel ou por decreto (edição manual, ou PR integrado quando `.mestre/` é versionado), o motor continua aplicando a última versão aprovada (o hash fica no banco) e avisa os Administradores. Se a nova versão só restringe, um Administrador aprova pelo painel; se amplia, só vale com decreto.

- **Como o motor percebe:** relê o arquivo a cada 30 s (padrão) e compara o hash com o aprovado. Um aviso por mudança (o mesmo arquivo não avisa de novo); arquivo apagado também avisa e mantém a versão aprovada.
- **O banco guarda a versão aprovada inteira** (hash e arquivo, tabela `workspace_ceilings`), e é ela que vale, diga o arquivo o que disser. Antes de qualquer versão aprovada vale o teto padrão, e um arquivo presente conta como mudança a aprovar.
- **Restringe ou amplia:** a comparação é conservadora. Amplia tudo o que a nova versão não mantém limitado com certeza: um padrão novo fora dos antigos nas listas do que pode ser dado; um campo que deixa de limitar; um `deny` que sai ou vira `ask`; `default`, flags ou `workflows` mais livres; custo maior ou que deixa de existir; `operator` ligado. Mudança que amplia e restringe ao mesmo tempo amplia. Só comentários ou formatação não restringem nem ampliam, e passam pela aprovação no painel.
- A aprovação cita o hash que o Administrador viu; se o arquivo mudou de novo, é recusada.

### Teto reduzido com classes que já existem

Classes que passam do novo teto aparecem como "acima do teto" no `mestre class check` e no painel. Nada é apagado: o pipeline simplesmente corta o que passou, com o motivo.

Quem usa o teto: [classes](classes.md) (M3); a sessão zero cria o arquivo (M4); decretos, limites de uso e aprovações (M5) e a administração no painel (M6) o ampliam, consomem ou editam ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Regras e erros

- `teto.yaml` inválido: vale a última versão aprovada, com alerta aos Administradores.
- `teto.yaml` que não é um arquivo comum dentro do workspace (link, junção, FIFO, dispositivo) ou maior que 256 KiB: não é lido nem seguido (`internal/safefile`, [reforço nativo](reforco-nativo.md#quando-as-regras-mudam)); vale como inválido, e o alerta diz o que ele é, sem nada do conteúdo.
- Decreto que amplia o teto afrouxa a segurança: pede a confirmação reforçada com PIN ([segurança do canal](../telegram/seguranca-do-canal.md)).
- Mestre da mesa nunca libera para uma classe algo que o teto proíbe, nem por herança de App ou cópia de grimório.

## Critérios de aceite

- **CA-TETO-01** · *rápida* — **Dado** um teto com `network.domains: ["github.com"]`, **quando** o Mestre da mesa cria uma classe que acessa `example.com`, **então** a criação é recusada e a mensagem cita `example.com`.
- **CA-TETO-02** · *rápida* — **Dado** um teto que nega `infra/**` e uma classe que escreve em `**`, **quando** o personagem edita `infra/main.tf`, **então** a decisão é negar com o motivo "teto do workspace".
- **CA-TETO-03** · *completa* — **Dado** `teto.yaml` editado à mão para liberar `docker *`, **quando** o motor percebe a mudança, **então** segue negando `docker *`, avisa os Administradores e só aplica a nova versão depois de um decreto.
- **CA-TETO-04** · *completa* — **Dado** uma mesa no teto de custo, **quando** o Grão-Mestre tenta aprovar mais gasto, **então** a aprovação é recusada e o pedido vai aos usuários Administrador ou acima.
- **CA-TETO-05** · *rápida* — **Dado** uma classe que escreve em `src/**`, **quando** o teto passa a negar `src/**`, **então** `class check` mostra a classe como "acima do teto".
- **CA-TETO-06** · *rápida* — **Dado** o teto padrão e uma classe com `operator: true`, **quando** a Cidadela monta as regras efetivas, **então** `operator` sai `false` com `origin` `ceiling`; **dado** um decreto `ceiling` de um Administrador que liga `operator`, **então** ele é recusado antes da confirmação.
- **CA-TETO-07** · *rápida* — **Dado** o teto padrão e uma classe sem `files.deny`, **quando** a Cidadela monta as regras efetivas e o personagem lê `config/service-account-prod.json` ou `.env.local` pela ferramenta de leitura, **então** as duas leituras são negadas com o motivo "teto do workspace", `files.deny` traz esses globs com `origin` `ceiling`, e a leitura de `.env.example` passa.
- **CA-TETO-08** · *rápida* — **Dado** o teto padrão e uma classe sem `files.deny`, **quando** o personagem lê `.env.production`, `.env.development` ou `.env.example` pela ferramenta de leitura, **então** as três leituras são negadas com o motivo "teto do workspace" e `files.deny` traz `**/.env*` com `origin` `ceiling`; a leitura de `config/settings.json` passa. Substitui, a partir do M5, o trecho de CA-TETO-07 em que a leitura de `.env.example` passa.

## Depende de

- [Permissões](permissoes.md)
- [Precedência](../arquitetura/precedencia.md)
