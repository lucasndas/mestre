# ADR 0029: Campanha, Senhor da Guilda e Companhia

> **Assunto:** troca de três nomes da interface para o vocabulário do tema (Workspace vira Campanha, Dono vira Senhor da Guilda, Party vira Companhia), o que muda e o que continua técnico · **Componente:** Todos · **Atualizado:** 2026-10-27

## Status

Aceita, 2026-10-27, pelo Dono, na volta dele pelo painel (M6, etapa 4). Muda o glossário; não amplia o [escopo do MVP](../produto/escopo-mvp.md).

## Contexto

O vocabulário do Mestre é de RPG (Guilda, Cidadela, Torre, Mesa, Grão-Mestre), e o glossário diz em palavras do dia a dia o que cada termo é ([glossário](../produto/glossario.md)). Três nomes fugiam disso: **Workspace** e **Party** são palavras em inglês no meio de uma interface em português, e **Dono** é o cargo comum do nível mais alto, que o próprio usuário estranhou. O Dono pediu que o nome do mundo real fique só na coluna "No mundo real", como nos outros termos.

## Opções consideradas

1. **Manter e só reforçar a ajuda "No mundo real".** Nada a mexer, mas a interface continua misturando tema e jargão.
2. **Trocar só na interface e no glossário, deixando os identificadores técnicos.** Muda o que a pessoa lê; código, API, banco e CLI seguem em inglês, como já manda a [regra de idioma](../desenvolvimento/convencoes.md).
3. **Trocar tudo, inclusive rotas e tabelas.** Quebra links salvos, a CLI e a réplica do banco por um ganho que o usuário não vê.

## Decisão

Opção 2.

| Hoje | Em português | Em inglês | Por quê |
|---|---|---|---|
| Workspace | **Campanha** | Campaign | a aventura longa de um grupo; as Mesas são as sessões de jogo dentro dela (Campanha › Mesa) |
| Dono | **Senhor da Guilda** | Guildmaster | já existia "Dono da Guilda"; evita "Mestre", que é o nome do produto, e Grão-Mestre, que é o agente |
| Party | **Companhia** | Party (fica) | "party" é inglês no meio do português; em inglês o termo já é o do RPG |

- Os outros níveis (Administrador, Aprovador, Observador) ficam: são papéis funcionais e precisam ser inequívocos.
- **Continuam técnicos e em inglês:** `workspace` nas rotas (`/api/workspaces`, `/workspaces/<slug>`), no banco, nos identificadores, na CLI e nos nomes de arquivo; o nível `owner`. O endereço `/workspaces` do painel continua valendo para links salvos.
- O termo composto "Teto do workspace" vira **Teto da campanha**; "Sessão zero do workspace", **sessão zero da campanha**.
- A troca é feita por uma unidade própria, a **4A2**, antes das demais da [etapa 4 do M6](../desenvolvimento/ordem-de-implementacao.md#regras-de-execução-da-ordem): glossário (português e inglês) e arquivos gerados, catálogos do painel, textos do Telegram e da CLI, e a prosa dos documentos (os documentos técnicos podem manter `workspace` ao falar do identificador).

## Consequências

- Até a 4A2 entrar, os documentos do painel desta etapa ainda dizem "workspace" e "Dono"; o desenho e o protótipo já usam os nomes novos.
- O glossário e os arquivos gerados mudam juntos (a suíte rápida confere); termo e chaves em inglês acompanham.
- Quem escreve texto novo usa os nomes novos na interface e os identificadores antigos no código.
