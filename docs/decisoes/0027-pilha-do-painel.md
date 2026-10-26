# ADR 0027: Pilha do painel (Vite, Vitest, Biome, TypeScript 7, Node 26)

> **Assunto:** as ferramentas e bibliotecas do painel em `web/` e o porquê de cada escolha, com versões fixas, orçamento de testes próprio e o job `e2e` · **Componente:** Painel · **Atualizado:** 2026-10-26

## Status

Aceita, 2026-10-26, pelo Dono, ao aprovar o plano do M6. Completa o [ADR 0017](0017-canvas-react-flow.md) (React, TypeScript, Vite, `@xyflow/react`, xterm.js); onde o texto antigo diz ESLint e Prettier, vale este.

## Contexto

O M6 cria o `web/`. O [ADR 0017](0017-canvas-react-flow.md) já escolheu React, TypeScript e Vite, mas não as ferramentas de teste, lint e CI, nem as versões. O projeto fixa versões e orçamentos de tempo medidos por máquina ([testes](../qualidade/testes.md)), e o painel é embutido no binário Go ([ADR 0001](0001-stack-go.md)).

## Opções consideradas

1. **ESLint e Prettier com a versão mais antiga do TypeScript que eles aceitam.** Prós: o conjunto mais comum. Contras: o `typescript-eslint` 8.72 só aceita TypeScript abaixo da 6.1, o que prenderia o projeto fora do TypeScript 7 nativo.
2. **Biome 2.6 e `tsc --noEmit` do TypeScript 7.** Prós: uma ferramenta só para lint e formato, rápida o bastante para rodar dentro do orçamento; o `tsc` nativo faz a checagem de tipos. Contras: menos regras que o ecossistema do ESLint.
3. **Node 24 em vez do 26.** O Node 24 entrou em manutenção em 2026-10-20; o 26 vira LTS em 2026-10-28.

## Decisão

1. **Versões:** a mais nova estável de cada pacote, **exata** no `package.json` (`save-exact=true` em `web/.npmrc`), `package-lock.json` no git e `npm ci` no CI. **Node 26** (LTS a partir de 2026-10-28; o Node 24 entrou em manutenção em 2026-10-20), fixado em `engines`, `web/.nvmrc` e `setup-node`. O Dependabot só dá **alertas de segurança**, sem PRs de versão: as versões sobem num PR deliberado no começo de cada marco.
2. **TypeScript 7** (nativo, 7.0.x) com `strict`. Funcionou com Vite 8.3, `@vitejs/plugin-react` 6 e Vitest 5 na verificação do M6; se um dia algo quebrar, cai para o TypeScript mais novo que funcione, e o motivo vai neste ADR.
3. **Vite 8** (build, servidor de desenvolvimento e `vite preview`), **Vitest 5** com jsdom e `retry: 0`. O build vai para `internal/panel/webdist/`; um plugin esvazia a pasta preservando o `.gitkeep` que o `go:embed` precisa, porque o `emptyOutDir` do Vite o apagaria.
4. **Biome 2.6** (lint e formato) e `tsc --noEmit`, no lugar de ESLint e Prettier: o `typescript-eslint` 8.72 aceita só TypeScript abaixo da 6.1. Regras de hooks do React ligadas; `setTimeout`, `setInterval` e `Date` ficam proibidos em testes (`noRestrictedGlobals`), espelhando o `forbidigo` do Go. Biome e `tsc` rodam em paralelo ao Vitest, dentro do orçamento, como o `golangci-lint` ao lado do `go test`.
5. **Orçamento próprio do lado web:** 15 s (provisório; o Dono fixa o valor com as medições do CI), separado dos 36 s do Go, que não mudam ([testes](../qualidade/testes.md#executor-testrun)). Nunca se aumenta orçamento.
6. **CI:** job `web` (Linux e Windows) que **sempre** roda, porque uma verificação exigida que é pulada trava a fila de merge, e job `e2e` com Playwright (Chromium sem tela, Linux e Windows) sobre o pacote que o `web` construiu ([smoke](../qualidade/testes.md#smoke-do-painel)). Os jobs de Go não dependem de Node: os testes Go do painel usam `fstest.MapFS`, nunca o pacote construído. Os critérios continuam provados em Go e Vitest; o smoke só prova que as peças funcionam juntas.
7. **Bibliotecas das próximas unidades** (registradas, instaladas quando a unidade precisar): Tailwind CSS 4 com os tokens em `@theme`, `react-aria-components`, TanStack Router, TanStack Query 5, `react-markdown` 10 (sem HTML cru), CodeMirror 6 (editor dos pergaminhos), Recharts 3, `@xterm/xterm` 6, `@xyflow/react` 12, `react-i18next` e `i18next` ([ADR 0028](0028-painel-bilingue-e-marco-m6-mais.md)).
8. **Tipos:** gerados do Go pelo `tygo` para `web/src/gen/` e versionados, com teste de que não ficaram velhos.
9. O sistema de design será `docs/painel/design.md`.

## Consequências

- Quem desenvolve o painel precisa do Node 26 e do `npm ci` em `web/` ([ambiente](../desenvolvimento/ambiente.md)); o gancho de pré-commit só roda o lado web quando o commit mexe em `web/`.
- O build de release terá de construir `web/` antes do `go build` (trabalho do M7; [convenções](../desenvolvimento/convencoes.md#layout-do-repositório)).
- Três verificações novas exigidas na `main` (`web` e `e2e`, nos dois sistemas): passo do Dono no ruleset ([ambiente](../desenvolvimento/ambiente.md#ci-e-proteção-da-main)).
