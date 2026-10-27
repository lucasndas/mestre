# Product

<!-- impeccable:product-schema 1 -->

Arquivo de contexto para o impeccable. A fonte da verdade do produto está em `docs/` ([visão](docs/produto/visao.md), [glossário](docs/produto/glossario.md)); aqui ficam só os fatos que orientam o design, sem copiar os documentos.

## Platform

web

## Stack

React 19, Vite 8, TypeScript 7, Tailwind CSS 4, react-aria-components, TanStack Router e Query, react-i18next ([ADR 0027](docs/decisoes/0027-pilha-do-painel.md)). Painel embutido no binário Go do Mestre.

## Users

O Senhor da Guilda e as pessoas que ele convida (Administrador, Aprovador, Observador etc.), no navegador do computador ou do celular. Não são, em geral, pessoas técnicas: acompanham o que os agentes de IA fazem nos repositórios do GitHub, aprovam ações e conversam com o Grão-Mestre.

## Product Purpose

O Mestre orquestra agentes de IA (Claude, Codex, Gemini) que trabalham em repositórios do GitHub, em várias máquinas (a Guilda). O painel é a janela do usuário: ver o que acontece, aprovar, conversar, administrar. Sucesso: uma pessoa não técnica entende o estado da Guilda e decide com segurança.

## Positioning

Vocabulário próprio de RPG (Guilda, Cidadela, Torre, Mesa, Grimório) como identidade, sempre com ajuda "no mundo real" para quem não conhece RPG.

## Operating Context

Tarefas de operação e leitura (Operate/Read): login, aprovações, conversa, canvas de personagens, terminal, tabelas de administração. Pode ficar aberto o dia todo. Dois idiomas desde o início (pt-BR e en); horários sempre no fuso da Guilda.

## Capabilities and Constraints

- O servidor decide o que cada nível pode; o painel só esconde.
- Ações sensíveis pedem PIN a cada vez.
- Textos de ajuda vêm do glossário, nunca escritos no código do painel.
- Acessível (teclado, contraste) e usável no celular.

## Brand Commitments

Direção escolhida pelo Senhor da Guilda em 2026-10-26: **Pergaminho** (papel quente, tinta escura, destaque vermelho-tijolo, títulos com serifa), tom de aventura e personalidade, sem perder a calma de uma ferramenta de trabalho. Detalhes em `docs/painel/design.md`.
