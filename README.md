# Mestre

O Mestre orquestra agentes de IA (Claude Code, Codex, Antigravity CLI ou um shell comum) que trabalham juntos em repositórios do GitHub. Cada pedido vira uma **mesa** (um worktree com sua branch), com um time de agentes coordenado por um **Mestre da mesa**; acima de todos, o **Grão-Mestre** conversa com você pelo painel ou pelo Telegram e cuida do GitHub.

Diferente de um orquestrador local, o Mestre é distribuído (um **Core** Linux e vários **nós**, como um PC Windows) e se cura sozinho: cada máquina tem um **Guardião** que vigia e conserta o motor, sem SSH.

Repositório: https://github.com/lucasndas/mestre

> Estado: especificação. Ainda não há código; a documentação em [`docs/`](docs/INDEX.md) é a base do desenvolvimento.

## Instalar em 3 passos

1. **Core (Ubuntu):** instale o pacote do Mestre Core e abra o painel no navegador para criar o usuário Dono.
2. **Nó (Windows):** instale o Mestre Node; ele encontra o Core sozinho na rede local e pede o pareamento.
3. **Primeiro workspace:** no painel, informe um repositório do GitHub e a ficha do Grão-Mestre.

Detalhes em [docs/operacao](docs/operacao/INDEX.md).

## Documentação

Tudo está em [docs/INDEX.md](docs/INDEX.md). Agentes que trabalham neste repositório seguem o [AGENTS.md](AGENTS.md).

## Licença

[PolyForm Noncommercial 1.0.0](LICENSE): uso não comercial liberado, com o aviso de autoria mantido; uso comercial só com autorização do autor. É código disponível, não "open source" no sentido da OSI.
