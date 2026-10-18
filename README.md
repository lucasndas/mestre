# Mestre

O Mestre orquestra agentes de IA (Claude Code, Codex, Antigravity CLI ou um shell comum) que trabalham juntos em repositórios do GitHub. Cada pedido vira uma **mesa** (um worktree com sua branch), com um time de agentes coordenado por um **Mestre da mesa**; acima de todos, o **Grão-Mestre** conversa com você pelo painel ou pelo Telegram e cuida do GitHub.

Diferente de um orquestrador local, o Mestre é distribuído e se cura sozinho: a mesma instalação em cada máquina, Linux ou Windows, forma uma **Guilda**, com uma máquina no comando (a **Cidadela**) e as outras (as **Torres**) rodando agentes, guardando uma cópia do estado e prontas para assumir se a Cidadela cair de vez, com a sua aprovação. Cada máquina tem um **Guardião** que vigia e conserta o motor, sem SSH. Uma máquina só também funciona. Não há interface gráfica além do painel no navegador; os agentes são de terminal.

Repositório: https://github.com/lucasndas/mestre

> Estado: em desenvolvimento, na ordem dos [marcos](docs/desenvolvimento/ordem-de-implementacao.md#marcos); a tabela deles diz o que cada marco entrega e quais já foram auditados. Ainda não há instalador nem painel. A documentação em [`docs/`](docs/INDEX.md) é a base do desenvolvimento.

## Instalar em 3 passos

1. **Primeira máquina (Ubuntu ou Windows):** rode o instalador. Sem outra Guilda na rede, ele cria a Guilda e esta máquina vira a Cidadela. Confie uma vez na CA do Mestre no navegador, abra o painel em `https://mestre.local:7443`, crie o usuário Dono e crie os Apps do GitHub pelos links que o painel mostra. Uma máquina só já funciona.
2. **Outras máquinas (opcionais, Ubuntu ou Windows):** rode o mesmo instalador; ele encontra a Guilda sozinho na rede local e mostra um código. Confira o código na mensagem que chega ao painel (ou ao Telegram) e aprove com o seu PIN.
3. **Telegram e primeiro workspace:** pareie o bot do Telegram pelo painel, informe um repositório do GitHub e a ficha do Grão-Mestre.

Detalhes em [docs/operacao](docs/operacao/INDEX.md).

## Documentação

Tudo está em [docs/INDEX.md](docs/INDEX.md). Agentes que trabalham neste repositório seguem o [AGENTS.md](AGENTS.md).

## Licença

[PolyForm Noncommercial 1.0.0](LICENSE): uso não comercial liberado, com o aviso de autoria mantido; uso comercial só com autorização do autor. É código disponível, não "open source" no sentido da OSI.
