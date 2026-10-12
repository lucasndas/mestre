# Escopo do MVP

> **Assunto:** o que é o MVP, os 12 critérios globais de aceite e o que fica de fora · **Componente:** Projeto · **Atualizado:** 2026-10-12

## O que é o MVP

Tudo o que a especificação original descreve é o MVP, e só isso. Nenhum documento acrescenta funcionalidade além dela; escolhas de detalhe necessárias para implementar (valores padrão, nomes, formatos) são propostas nos documentos e marcadas como "padrão" quando configuráveis.

**Ambiente do MVP:**
- Core em Ubuntu (versão LTS atual, registrada na instalação).
- Um nó Windows 11 ou superior (conferido pelo [checklist](../plataforma/checklist.md)).
- Tudo na rede local. O desenho já suporta N nós e nós fora da rede, mas o MVP é validado com esses dois.
- Repositórios na conta pessoal do GitHub do Dono (não organização).

## Critérios globais (MVP-1 a MVP-12)

Cada critério é coberto por um teste de ponta a ponta na suíte pesada; o mapa critério → teste está em [testes](../qualidade/testes.md) (seção "Mapa do MVP para a suíte pesada"). Os documentos listados detalham o critério em critérios menores (que citam `(MVP-N)`).

| Critério | Texto | Detalhado em |
|---|---|---|
| **MVP-1** | Instalo no Linux e no Windows, crio o Dono, e o nó Windows encontra e se pareia com o Core sozinho na rede local. | [instalação e atualização](../plataforma/instalacao-e-atualizacao.md), [login](../painel/login.md), [descoberta](../conectividade/descoberta.md), [pareamento](../conectividade/pareamento.md), [nó Windows](../arquitetura/no-windows.md), [instalar](../operacao/instalar.md), [primeiro acesso](../operacao/primeiro-acesso.md), [parear nó](../operacao/parear-no.md) |
| **MVP-2** | Mato o motor de um lado e ele volta sozinho; travo o motor do Core e um nó conduz a recuperação pelo Guardião; recebo alerta no Telegram e no painel quando nada resolve. | [Guardião](../autocura/guardiao.md), [camadas](../autocura/camadas.md), [diagnóstico com IA](../autocura/diagnostico-com-ia.md), [página reserva](../autocura/pagina-reserva.md), [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) |
| **MVP-3** | Crio um workspace a partir de um repositório: a sessão zero gera `.mestre/`, skills e hooks, sem alterar meu `AGENTS.md` nem atrapalhar quem clonar o repositório. | [workspaces](../orquestracao/workspaces.md), [sessão zero do workspace](../sessao-zero/workspace.md), [sessão zero da mesa](../sessao-zero/mesa.md), [sessão zero do personagem](../sessao-zero/personagem.md), [adaptador](../provedores/adaptador.md) |
| **MVP-4** | Peço ao Grão-Mestre uma funcionalidade: ele cria o épico no GitHub, abre uma mesa (worktree + branch), e o Mestre da mesa monta missões ligadas a issues e sub-issues, recruta personagens de classes diferentes em Claude Code, Codex e Antigravity, distribuídos entre Linux e Windows, e o trabalho passa de etapa em etapa até o resultado, com esperas, devolução e encerramento dos efêmeros. Duas mesas trabalham em paralelo sem interferir uma na outra. | [Grão-Mestre](../orquestracao/grao-mestre.md), [mesas](../orquestracao/mesas.md), [Mestre da mesa](../orquestracao/mestre-da-mesa.md), [missões](../orquestracao/missoes.md), [personagens](../orquestracao/personagens.md), [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md), [conexões](../orquestracao/conexoes.md), [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md), [Antigravity](../provedores/antigravity.md) |
| **MVP-5** | No GitHub, vejo a árvore épico → issues → sub-issues atualizada, cada commit, comentário e atualização marcados com o App da classe de quem fez, o PR do épico aberto pela mesa (App "Mestre") e aprovado pelo Grão-Mestre (App "Grão-Mestre"). Quando surge uma classe nova, o Grão-Mestre cria e instala o App dela sozinho. | [ações por nível](../github/acoes-por-nivel.md), [identidades e Apps](../github/identidades-e-apps.md), [criação de Apps](../github/criacao-de-apps.md), [aprovação no GitHub](../github/aprovacao-no-github.md), [eventos](../github/eventos.md) |
| **MVP-6** | As permissões de cada classe são respeitadas igualmente nos três provedores (ex.: o QA é impedido de editar código de produção). | [classes](../classes/classes.md), [permissões](../classes/permissoes.md), [teto do workspace](../classes/teto-do-workspace.md), [adaptador](../provedores/adaptador.md), [conformidade](../provedores/conformidade.md), [precedência](../arquitetura/precedencia.md) |
| **MVP-7** | O Grão-Mestre aprova sozinho o que a política permite e me pergunta pelo Telegram quando algo cai numa área sensível ou ele está em dúvida; respondo pelo Telegram (ou pelo painel) e a decisão é aplicada, inclusive no GitHub. | [política](../aprovacoes/politica.md), [fluxo](../aprovacoes/fluxo.md), [aprovação no GitHub](../github/aprovacao-no-github.md), [conversa e aprovações](../painel/conversa-e-aprovacoes.md) |
| **MVP-8** | Crio um decreto permanente pelo Telegram, ele passa a valer acima das regras, e consigo revogá-lo. | [decretos](../telegram/decretos.md), [precedência](../arquitetura/precedencia.md) |
| **MVP-9** | Um usuário não autorizado escreve para o bot e é ignorado, e eu sou alertado. | [segurança do canal](../telegram/seguranca-do-canal.md), [usuários e níveis](../telegram/usuarios-e-niveis.md) |
| **MVP-10** | Mando um áudio pelo Telegram: ele é transcrito localmente, o Grão-Mestre responde no próprio Telegram; mando uma imagem ou um PDF e eles chegam à mesa certa e são usados na tarefa. | [anexos e transcrição](../telegram/anexos-e-transcricao.md), [roteamento](../telegram/roteamento.md), [bot](../telegram/bot.md) |
| **MVP-11** | Vejo tudo no painel: a visão do workspace com as mesas e, dentro de cada mesa, personagens, conexões, pergaminhos, missões, esperas, saúde dos nós e consumo de uso; salvo um grimório e restauro em outra mesa; uso rituais. | [canvas](../painel/canvas.md), [visão do workspace](../painel/visao-do-workspace.md), [administração](../painel/administracao.md), [grimórios](../orquestracao/grimorios.md), [rituais](../orquestracao/rituais.md), [limites de uso](../plataforma/limites-de-uso.md) |
| **MVP-12** | Reinicio o Core e os nós: workspaces, mesas, personagens persistentes, esperas e missões em andamento voltam ao estado anterior. | [persistência](../arquitetura/persistencia.md), [personagens](../orquestracao/personagens.md), [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md), [missões](../orquestracao/missoes.md), [camadas](../autocura/camadas.md) |

## Fora do escopo do MVP

Itens que a especificação ou as decisões deixam explicitamente para depois (ou nunca):

| Item | Situação | Onde está registrado |
|---|---|---|
| Nós fora da rede local por Tailscale | documentado como opção; não é requisito do MVP | [descoberta](../conectividade/descoberta.md) |
| Commits assinados ("Verified") | MVP faz commits sem assinatura; recriar commits pela API fica como opção futura | [ADR 0014](../decisoes/0014-commits-sem-assinatura-no-mvp.md) |
| Repositórios de organização e "tipo de issue" | MVP usa conta pessoal; épico é rótulo | [ADR 0015](../decisoes/0015-epico-como-rotulo.md) |
| Webhooks do GitHub | nunca: o GitHub é acompanhado por consulta periódica | [ADR 0009](../decisoes/0009-github-por-consulta-periodica.md) |
| SSH (dia a dia ou autocura) | nunca | [ADR 0003](../decisoes/0003-autocura-com-guardiao-sem-ssh.md) |
| Classes prontas de fábrica | nunca: classes são criadas pelos usuários, pelo Grão-Mestre ou pelo Mestre da mesa | [classes](../classes/classes.md) |
| Agentes com GUI no Windows sem usuário logado | não suportado: exigem sessão do usuário (ou login automático) | [nó Windows](../arquitetura/no-windows.md) |
| Transcrição em nó com GPU | o desenho escolhe o nó por capacidade, mas no MVP roda no Core por CPU | [ADR 0016](../decisoes/0016-whisper-cpp-local.md) |
| Uso comercial | só com autorização do autor | [ADR 0018](../decisoes/0018-licenca-polyform-noncommercial.md) |
