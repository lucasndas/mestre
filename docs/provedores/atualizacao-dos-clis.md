# Atualização dos CLIs dos provedores

> **Assunto:** os CLIs `claude`, `codex` e `agy` se atualizam sozinhos; o Mestre detecta a versão nova por máquina e por perfil, roda a conformidade logo depois, marca o provedor como não conforme se ela falhar e avisa o Dono · **Componente:** Todos · **Atualizado:** 2026-10-19

## Objetivo

Deixar cada CLI de provedor se atualizar pelo próprio mecanismo, sem o Mestre desligar nada, e ainda assim nunca entregar personagem novo a uma versão que quebra o Mestre: o motor percebe a mudança de versão, roda o [teste de conformidade](conformidade.md) em seguida e, se a versão nova falhar, para de usar aquele provedor naquela máquina e avisa o Dono. O Mestre não aprova, não instala e não volta versões dos CLIs. A atualização do próprio Mestre está em [autoatualização](../plataforma/autoatualizacao.md).

## Comportamento

### O que cada CLI faz sozinho

O Mestre não desliga a autoatualização de nenhum CLI: nada de `DISABLE_AUTOUPDATER`, `check_for_update_on_startup = false` ou `AGY_CLI_DISABLE_AUTO_UPDATE`. Detalhes de cada provedor ficam no documento dele: [Claude Code](claude-code.md), [Codex](codex.md), [Antigravity](antigravity.md).

| Provedor | O que o CLI faz sozinho | O que o Mestre faz |
|---|---|---|
| Claude Code (instalação nativa) | baixa a versão nova em segundo plano; ela vale no próximo início | nada no início; detecta a versão no próximo início ou no checklist |
| Codex | ao iniciar, procura a versão nova e mostra a tela "Update now / Skip / Skip until next version" | o [adaptador](adaptador.md#prontidão-e-telas-interativas) escolhe **atualizar** (ver [Verificar](#verificar-na-implementação)) |
| Antigravity (`agy`) | atualizador automático em segundo plano (citado no changelog) | nada no início; detecta a versão no próximo início ou no checklist |

**Onde ficam:** cada CLI fica no `HOME` do perfil de conta, numa pasta que a própria atualização consegue gravar ([CLIs dos provedores](../plataforma/instalacao-e-atualizacao.md#clis-dos-provedores)). Assim cada perfil se atualiza por conta própria e pode estar numa versão diferente de outro perfil da mesma máquina. Como os agentes rodam como `mestre-agente`, dono dessas pastas, as regras fixas negam às ferramentas deles as pastas de dados do Mestre, perfis incluídos ([regras fixas](../classes/permissoes.md#regras-fixas-de-segurança)).

### Detecção

- O motor de cada máquina registra a versão de cada CLI **por perfil de conta** e compara com a anterior: ao iniciar ou retomar um personagem (o `--version` daquele perfil), no [checklist](../plataforma/checklist.md) a cada ciclo e no anúncio da [ficha da máquina](../guilda/ficha-da-maquina.md).
- Versão diferente da registrada → entra na auditoria (máquina, perfil, provedor, versão anterior e nova) e dispara a conformidade daquele provedor com aquele perfil, naquela máquina. Não há consulta de "versão disponível": o Mestre só reage ao que o CLI já instalou.
- O registro de terminais guarda a versão com que cada personagem iniciou ([personagens](../orquestracao/personagens.md#registro-de-terminais)).

### Conformidade logo depois

- Enquanto o teste roda, o provedor fica "em verificação" naquela máquina e perfil: recrutamentos desse provedor ali esperam ou vão para outra máquina que atenda ([escolha da máquina](../orquestracao/personagens.md#escolha-da-máquina)).
- **Passou:** a versão fica registrada como conforme e os personagens novos já nascem nela. Os resultados de [teste de classe](teste-de-classe.md#quando-um-cli-muda) daquele provedor passam a "testada em versão anterior".
- **Falhou:** o provedor fica **não conforme** naquela máquina, pela regra da [conformidade](conformidade.md#regras-e-erros): não recebe personagens novos ali, e o Dono recebe o aviso pelo Telegram e no painel com o relatório do teste. Não há volta de versão: o caminho é a próxima versão do provedor, testada quando aparecer, ou a correção manual pelo Dono.

### Personagens já rodando

- Seguem com a versão com que iniciaram até o próximo reinício natural (fim do ciclo de vida, troca de ficha, retomada depois de um reinício do motor). **Nenhum reinício é forçado** por causa de uma versão nova.
- Num reinício natural, o personagem sobe com a versão que estiver instalada no perfil. Se o provedor estiver não conforme ali, a retomada acontece assim mesmo e o personagem fica marcado, como os que já rodavam ([conformidade](conformidade.md#regras-e-erros)).

### Risco aceito

Uma versão nova pode entrar entre duas tarefas sem teste prévio: o CLI se atualiza antes de o Mestre saber. A conformidade roda logo depois e, até ela terminar, o provedor não recebe personagens novos naquela máquina e perfil. Personagens que iniciarem nesse intervalo por retomada usam a versão nova sem teste.

## Regras e erros

- O Mestre nunca desliga a autoatualização dos CLIs, nunca instala nem volta uma versão deles e não pede aprovação para isso.
- Conformidade sem cota do provedor: a versão fica "em verificação" e o teste é tentado de novo quando a cota voltar.
- Versão que muda de novo durante o teste: o teste em curso é descartado e começa outro com a versão mais nova.
- Versão abaixo da mínima do documento do provedor: a base é recusada naquela máquina e o checklist avisa (ALL-10).

## Critérios de aceite

- **CA-CLIUPD-01** · *rápida* — **Dado** uma ficha de cada provedor, **quando** o adaptador monta o início, **então** o ambiente não tem `DISABLE_AUTOUPDATER`, `DISABLE_UPDATES` nem `AGY_CLI_DISABLE_AUTO_UPDATE`, e o `config.toml` do perfil Codex não tem `check_for_update_on_startup = false`.
- **CA-CLIUPD-02** · *rápida* — **Dado** a versão do `claude` do perfil A que passou de 1 para 2 numa máquina, **quando** o checklist roda, **então** a mudança entra na auditoria com máquina, perfil e versões, e a conformidade roda só para o Claude Code com o perfil A naquela máquina.
- **CA-CLIUPD-03** · *rápida* — **Dado** a conformidade reprovando a versão nova, **quando** o resultado chega, **então** o provedor fica não conforme na máquina, um recrutamento desse provedor vai para outra máquina que atenda ou falha com a explicação, o Dono recebe o aviso com o relatório, e nenhum personagem é reiniciado nem nada é desinstalado.
- **CA-CLIUPD-04** · *rápida* — **Dado** três personagens rodando na versão 1, **quando** a versão 2 aparece e passa na conformidade, **então** nenhum deles é reiniciado; o próximo personagem criado usa a 2 e o registro de terminais mostra a versão de cada um.
- **CA-CLIUPD-05** · *rápida* — **Dado** uma conformidade em andamento para o Codex numa máquina, **quando** chega um recrutamento de ficha Codex, **então** ele espera ou vai para outra máquina que atenda.
- **CA-CLIUPD-06** · *rápida* — **Dado** o agente falso do Codex mostrando a tela de atualização ao iniciar, **quando** o personagem começa, **então** o adaptador escolhe atualizar, o personagem chega a pronto e a versão nova é detectada.
- **CA-CLIUPD-10** · *completa* — **Dado** uma Cidadela e uma Torre reais com o agente falso trocando sozinho da versão 1 para a 2 na Torre, **quando** o motor detecta, **então** a conformidade roda na Torre e passa, os personagens existentes seguem na versão 1 e o próximo criado nasce na 2.
- **CA-CLIUPD-11** · *rápida* — **Dado** resultados de [teste de classe](teste-de-classe.md) no Claude Code 2.1.299 e a conformidade passando no 2.1.300, **quando** o motor registra a versão nova, **então** os resultados da classe nesse provedor ficam "testada em versão anterior" e o `class check` mostra a dica de testar de novo, sem rodar nada.

## Depende de

- [Teste de conformidade](conformidade.md) · [Adaptador](adaptador.md)
- [Claude Code](claude-code.md) · [Codex](codex.md) · [Antigravity](antigravity.md)
- [Personagens](../orquestracao/personagens.md)
- [Checklist](../plataforma/checklist.md)
- [Teste de classe](teste-de-classe.md)
- [Autoatualização](../plataforma/autoatualizacao.md)
- [Instalação e atualização](../plataforma/instalacao-e-atualizacao.md)

## Verificar na implementação

- **Verificar na implementação:** no Codex, escolher "Update now" na tela de início atualiza e deixa o personagem seguir. **Padrão:** o Codex atualiza e continua o início (ou sai, e o motor o inicia de novo uma vez, já na versão nova). **Se falhar:** se a tela atrapalhar o início (o Codex para, pede reinício manual ou trava), o adaptador escolhe "Skip" naquele início e o motor roda `codex update` como `mestre-agente` com o `CODEX_HOME` do perfil quando nenhum personagem Codex daquele perfil estiver rodando; a versão nova segue a detecção acima. **Onde:** no laboratório da [suíte pesada](../qualidade/testes.md#laboratório), na primeira versão nova do Codex depois da implementação.
- **Verificar na implementação:** o atualizador próprio de cada CLI grava só no `HOME` do perfil, sem precisar de pasta do root nem de outra conta. **Padrão:** grava (o instalador nativo do Claude Code usa `$HOME/.local/share/claude`). **Se falhar:** aquele CLI deixa de se atualizar naquele perfil; o checklist mostra a versão parada e, abaixo da mínima, recusa a base (ALL-10), e o Dono atualiza à mão. **Onde:** no laboratório, com dois perfis do mesmo provedor numa VM de cada sistema.
- **Verificar na implementação:** no Windows, a atualização própria de cada CLI com personagens do mesmo perfil rodando (arquivo em uso). **Padrão:** o atualizador do CLI lida com isso (o Claude Code guarda versões em pastas separadas). **Se falhar:** a atualização falha naquele momento e o CLI tenta de novo depois; nada muda para o Mestre além de a versão demorar a mudar. **Onde:** no laboratório, na VM Windows.

## Fontes

- Claude Code, atualizações e versões: https://code.claude.com/docs/en/setup (Auto-updates) · retomada: https://code.claude.com/docs/en/cli-reference
- Codex, `check_for_update_on_startup`: https://learn.chatgpt.com/docs/config-file/config-reference · código-fonte `openai/codex` (commit `b3c3bad`): `codex-rs/tui/src/updates.rs`, `update_prompt.rs`, `codex-rs/cli/src/main.rs` (`update`)
- Antigravity, `agy update`: https://antigravity.google/docs/cli/install · changelog: binário `agy` 1.3.3 instalado (`agy changelog`)
