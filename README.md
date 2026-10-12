# Mestre

O Mestre orquestra agentes de IA (Claude Code, Codex, Antigravity CLI ou um shell comum) que trabalham juntos em repositórios do GitHub. Cada pedido vira uma **mesa** (um worktree com sua branch), com um time de agentes coordenado por um **Mestre da mesa**; acima de todos, o **Grão-Mestre** conversa com você pelo painel ou pelo Telegram e cuida do GitHub.

Diferente de um orquestrador local, o Mestre é distribuído (um **Core** e vários **nós**, cada um em Linux ou Windows, ou uma máquina só) e se cura sozinho: cada máquina tem um **Guardião** que vigia e conserta o motor, sem SSH. Não há interface gráfica além do painel no navegador; os agentes são de terminal.

Repositório: https://github.com/lucasndas/mestre

> Estado: especificação. Ainda não há código; a documentação em [`docs/`](docs/INDEX.md) é a base do desenvolvimento.

## Instalar em 3 passos

1. **Core (Ubuntu ou Windows):** rode o instalador e escolha o papel "Core". Confie uma vez na CA do Mestre no navegador, abra o painel, crie o usuário Dono e crie os Apps do GitHub pelos links que o painel mostra. Uma máquina só já funciona (só Linux ou só Windows).
2. **Nós (opcionais, Ubuntu ou Windows):** gere um convite no painel, rode o instalador, escolha "nó" e informe o código; o nó encontra o Core sozinho na rede local.
3. **Telegram e primeiro workspace:** pareie o bot do Telegram pelo painel, informe um repositório do GitHub e a ficha do Grão-Mestre.

Detalhes em [docs/operacao](docs/operacao/INDEX.md).

## Documentação

Tudo está em [docs/INDEX.md](docs/INDEX.md). Agentes que trabalham neste repositório seguem o [AGENTS.md](AGENTS.md).

## Licença

[PolyForm Noncommercial 1.0.0](LICENSE): uso não comercial liberado, com o aviso de autoria mantido; uso comercial só com autorização do autor. É código disponível, não "open source" no sentido da OSI.
