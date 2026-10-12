# Decisões (ADRs)

> **Assunto:** índice dos registros de decisão de arquitetura · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [0001-stack-go.md](0001-stack-go.md) | Go como linguagem do motor, da CLI e do Guardião; comparação com Rust e TypeScript/Bun; bibliotecas escolhidas | Antes de escrever código ou adicionar uma dependência |
| [0002-wss-iniciado-pelo-no.md](0002-wss-iniciado-pelo-no.md) | Por que cada nó abre uma conexão WSS de saída até o Core, multiplexada | Ao mexer na conectividade nó↔Core ou no protocolo |
| [0003-autocura-com-guardiao-sem-ssh.md](0003-autocura-com-guardiao-sem-ssh.md) | Por que a autocura usa um Guardião local com lista fechada de ações, sem SSH | Ao mexer no Guardião ou na recuperação |
| [0004-mesa-igual-worktree.md](0004-mesa-igual-worktree.md) | Por que cada mesa é um git worktree com branch própria, saído do espelho do Core | Ao mexer no ciclo de vida das mesas ou no espelho |
| [0005-motor-executa-github-grao-mestre-porteiro.md](0005-motor-executa-github-grao-mestre-porteiro.md) | Por que nenhum agente tem credencial do GitHub e o motor executa tudo | Ao adicionar qualquer ação no GitHub |
| [0006-app-por-classe-criado-pelo-navegador.md](0006-app-por-classe-criado-pelo-navegador.md) | Um GitHub App por classe, criado pelo fluxo de manifesto no navegador dedicado | Ao mexer em classes, identidades ou criação de Apps |
| [0007-apps-fixos-grao-mestre-e-mestre.md](0007-apps-fixos-grao-mestre-e-mestre.md) | Os Apps fixos "Grão-Mestre" e "Mestre" e as credenciais do motor (chaves privadas, tokens de instalação) | Ao mexer em PRs, aprovação ou autenticação no GitHub |
| [0008-grao-mestre-aprovador-delegado.md](0008-grao-mestre-aprovador-delegado.md) | Grão-Mestre como aprovador delegado, plano B por ruleset e credencial pessoal por device flow | Ao mexer em aprovações ou na política |
| [0009-github-por-consulta-periodica.md](0009-github-por-consulta-periodica.md) | Eventos do GitHub por consulta periódica condicional, sem webhooks | Ao mexer na coleta de eventos do GitHub |
| [0010-instrucoes-em-duas-camadas.md](0010-instrucoes-em-duas-camadas.md) | Camada do Mestre separada do `AGENTS.md` e o canal de entrega por provedor | Ao mexer na montagem de instruções ou num adaptador de provedor |
| [0011-agents-skills-pasta-oficial.md](0011-agents-skills-pasta-oficial.md) | `.agents/skills/` como fonte das skills, com cópia gerenciada em `.claude/skills/` | Ao mexer na geração de skills |
| [0012-despachante-unico-de-hooks.md](0012-despachante-unico-de-hooks.md) | `mestre hook` como despachante único de hooks e permissões nos três provedores | Ao mexer em hooks, permissões ou num provedor |
| [0013-sincronizacao-de-worktrees-por-git-bundle.md](0013-sincronizacao-de-worktrees-por-git-bundle.md) | Sincronização de worktrees entre nó e Core por git bundle pela conexão WSS | Ao mexer em mesas com mais de um nó ou na transferência de arquivos |
| [0014-commits-sem-assinatura-no-mvp.md](0014-commits-sem-assinatura-no-mvp.md) | Commits locais sem assinatura com o autor do bot do App; opção futura de commits "Verified" | Ao mexer em commits, envio de branch ou rulesets de assinatura |
| [0015-epico-como-rotulo.md](0015-epico-como-rotulo.md) | Épico como issue com rótulo `epico` e hierarquia por sub-issues | Ao mexer no espelho mesa↔épico no GitHub |
| [0016-whisper-cpp-local.md](0016-whisper-cpp-local.md) | Transcrição local com whisper.cpp `whisper-server` e large-v3-turbo quantizado | Ao mexer em transcrição ou na instalação do modelo |
| [0017-canvas-react-flow.md](0017-canvas-react-flow.md) | Painel em React + TypeScript + Vite com @xyflow/react e xterm.js, embutido no Core | Ao mexer no painel ou no build do frontend |
| [0018-licenca-polyform-noncommercial.md](0018-licenca-polyform-noncommercial.md) | Licença PolyForm Noncommercial 1.0.0 e o termo "código disponível" | Ao adicionar dependências ou tratar de uso comercial e contribuições |
| [0019-autoatualizacao-por-releases.md](0019-autoatualizacao-por-releases.md) | Autoatualização do Mestre pelas Releases do GitHub, binários assinados distribuídos pelo Core, motor e Guardião alternados | Ao mexer em atualização do motor, do Guardião ou dos CLIs dos provedores |
