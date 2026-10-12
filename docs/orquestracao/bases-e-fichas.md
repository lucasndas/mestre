# Bases e fichas

> **Assunto:** campos de bases e fichas, exemplos, exportação/importação YAML/JSON e perfis de conta · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Todo terminal nasce de uma **base** (o "como iniciar") e todo agente de uma **ficha** (o "quem é"). São presets definidos pelos usuários; agentes só os usam, nunca os criam nem editam. Assim nenhum agente consegue abrir um terminal com comando livre.

## Comportamento

### Onde ficam e quem edita

- No banco do Core, valendo para todos os workspaces. Criar, editar e apagar: pelo painel ([administração](../painel/administracao.md)), só no nível que a [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades) permite (hoje, o Dono).
- Agentes só leem (`mestre sheet list|show`, `mestre base list|show`; sintaxe em [coordenação](../cli/coordenacao.md)).
- Alterações valem para personagens **novos**. Os vivos guardam a cópia usada ao nascer ([registro de terminais](personagens.md#registro-de-terminais)).

### Base

| Campo | Tipo | Obrigatório | Significado |
|---|---|---|---|
| `name` | texto `[a-z0-9-]`, único | sim | ex.: `claude-code` |
| `command`, `args` | texto, lista | sim | programa e argumentos |
| `shell` | `bash`\|`pwsh`\|`powershell`\|`cmd` | não | se presente, o comando roda dentro desse shell; senão, execução direta |
| `cwd` | caminho com variáveis | não (padrão `{mesa}`) | pasta inicial |
| `env` | mapa | não | variáveis de ambiente extras |
| `platforms` | lista de `{os, command?, args?}` | sim | sistemas onde pode rodar, com comando próprio por sistema quando preciso |
| `nodes` | lista de ids de nó | não | restringe a esses nós |
| `requires` | mapa de capacidades | não | ex.: `gui: true` |
| `ready` | `hook` \| `pattern: <regex>` \| `delay: <segundos>` | sim | como saber que está pronto para receber entrada |

- **Variáveis de `cwd`:** `{mesa}` = worktree da mesa no nó (para o Grão-Mestre, o clone principal); `{workspace}` = clone principal no Core, ou `<dados-no>/workspaces/<slug>` num nó; `{home}` = pasta do usuário do serviço. Para personagens com agente, `cwd` precisa ficar dentro de `{mesa}`.
- **`ready`:** `hook` espera o evento de início de sessão do `mestre hook` (padrão para agentes); `pattern` procura a regex na saída do terminal (padrão para shells, também usada para detectar o fim de cada comando); `delay` só como último recurso.
- O motor acrescenta sozinho as opções da camada do Mestre, as variáveis `MESTRE_*` e as do perfil de conta ([sessão zero do personagem](../sessao-zero/personagem.md)); a base não as define. Opções proibidas por provedor (ex.: em [Antigravity](../provedores/antigravity.md)) são recusadas ao salvar.

### Ficha

| Campo | Tipo | Obrigatório | Significado |
|---|---|---|---|
| `name` | texto, único | sim | ex.: "Dev Claude" |
| `base` | nome de base | sim | como iniciar |
| `agent` | `claude-code`\|`codex`\|`antigravity`\|`shell` | sim | tipo de agente (escolhe o [adaptador](../provedores/adaptador.md)); `shell` não recebe skills nem hooks |
| `class` | nome de classe | não | classe padrão; `recruit --class` substitui |
| `account` | nome de perfil de conta | não | conta/assinatura usada (abaixo); sem ela, o perfil padrão do provedor no nó |
| `mestre_skills` | booleano | não (padrão `true`) | recebe as skills do Mestre |
| `extra_skills` | lista | não | skills extras além das da classe |
| `preferred_node` | id de nó | não | preferência na [escolha do nó](personagens.md#escolha-do-nó) |
| `requires` | mapa de capacidades | não | soma às da base |
| `lifecycle` | `ephemeral`\|`persistent` | não (padrão `ephemeral`) | [ciclo de vida](personagens.md#ciclo-de-vida-definido-na-ficha) |
| `max_idle` | duração | não (padrão `120m`) | tempo máximo ocioso (persistentes) |
| `max_copies` | inteiro ≥ 1 | não (padrão 3) | cópias simultâneas |
| `restrictions` | mesmo formato de `permissions.yaml` | não | só restringe a classe, nunca libera ([permissões](../classes/permissoes.md)) |

Validação ao salvar: a base existe; `agent` é compatível com o `ready` da base (`hook` exige agente diferente de `shell`); `restrictions` não libera nada.

### Exemplos

```yaml
kind: mestre/presets
version: 1
bases:
  - name: claude-code
    command: claude
    platforms: [{os: linux}, {os: windows, command: claude.exe}]
    ready: hook
  - name: codex
    command: codex
    platforms: [{os: linux}, {os: windows, command: codex.exe}]
    ready: hook
  - name: antigravity
    command: agy
    platforms: [{os: linux}, {os: windows, command: agy.exe}]
    ready: hook
  - name: bash
    command: bash
    platforms: [{os: linux}]
    ready: {pattern: '\$ $'}
  - name: powershell
    command: pwsh
    platforms: [{os: windows}]
    ready: {pattern: 'PS .*> $'}
sheets:
  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, lifecycle: persistent, max_idle: 240m}
  - {name: Revisor Codex, base: codex, agent: codex, class: Revisor}
  - {name: QA Antigravity, base: antigravity, agent: antigravity, class: QA, account: agy-pessoal}
```

### Exportar e importar

- Formato único em YAML ou JSON (mesma estrutura acima: `kind`, `version`, `bases`, `sheets`). Exporta-se tudo ou uma seleção; nunca contém segredos.
- Importação: valida o arquivo inteiro primeiro (versão, campos, referências). Para cada nome que já existe, o usuário escolhe **substituir**, **renomear** ou **pular**. Depois das escolhas, aplica tudo de uma vez ou nada.
- `version` maior que a suportada: recusa dizendo a versão esperada.

### Perfis de conta

Para usar várias contas do mesmo provedor, cada nó declara **perfis de conta**: `name` (ex.: `claude-trabalho`), `provider` e a pasta de configuração isolada da conta. O motor aplica a pasta pela variável que cada provedor usa (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `HOME` separado), detalhada em [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md) e [Antigravity](../provedores/antigravity.md).
- O mesmo nome de perfil pode existir em vários nós (cada nó faz login na mesma conta). A ficha aponta para o nome; um nó sem esse perfil não é candidato.
- **Configuração inicial do perfil**, guiada pelo painel, uma vez por nó: (1) login do usuário na conta; (2) no Codex, aprovar uma vez os hooks do Mestre em `/hooks` ([codex](../provedores/codex.md)). O [checklist](../plataforma/checklist.md) confere se continua logado e, no Codex, se a confiança dos hooks existe.
- O Codex sempre usa um perfil do Mestre: ficha de Codex sem `account` usa o perfil padrão criado pelo Mestre no nó, nunca o `~/.codex` pessoal.
- Perfis são cadastrados no painel, por nó ([administração](../painel/administracao.md)).

## Regras e erros

- `env` não pode definir variáveis `MESTRE_*` nem conter segredos; valores que parecem segredo são recusados ao salvar ([segurança](../arquitetura/seguranca.md)).
- Apagar base usada por ficha, ou ficha usada por personagem vivo, é recusado com a lista de quem usa. Missões e grimórios que citam uma ficha apagada falham ao rodar/restaurar, dizendo qual ficha falta.
- Base sem nenhuma plataforma, ou `cwd` de agente fora de `{mesa}`: recusada ao salvar.

## Critérios de aceite

- **CA-BF-01** · *rápida* — **Dado** a base `claude-code` do exemplo, **quando** resolvida para um nó Windows, **então** o comando é `claude.exe`; para Linux, `claude`.
- **CA-BF-02** · *rápida* — **Dado** uma ficha com `restrictions` que libera um comando proibido pela classe, **quando** é salva, **então** é recusada.
- **CA-BF-03** · *rápida* — **Dado** o arquivo do exemplo exportado em YAML, **quando** é importado como JSON equivalente num banco vazio, **então** bases e fichas ficam idênticas; **quando** importado de novo com "pular", **então** nada muda.
- **CA-BF-04** · *rápida* — **Dado** uma base com `env: {MESTRE_AGENT_ID: x}`, **quando** é salva, **então** é recusada.
- **CA-BF-05** · *rápida* — **Dado** uma ficha com `account: agy-pessoal` e só um nó com esse perfil, **quando** se escolhe o nó, **então** só esse nó é candidato.

## Depende de

- [Persistência](../arquitetura/persistencia.md)
- [Adaptador de provedores](../provedores/adaptador.md)
- [Permissões](../classes/permissoes.md)
- [Classes](../classes/classes.md)
