# ADR 0020: Quatro usos, com o Core em Linux ou Windows e sem interface gráfica

> **Assunto:** por que o Mestre nasce com quatro usos (só Linux, Linux + Windows, Windows + Linux, só Windows) e sem nenhuma interface gráfica além do painel no navegador · **Componente:** Todos · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12. Decisão completada em 2026-10-12: o nó local do Core é o serviço `mestre-node` também no Core Linux, com o mesmo caminho de código de um nó remoto.

## Contexto

- O desenho começou com um Core em Ubuntu e um nó Windows, a máquina do autor. Muita gente tem só uma máquina, ou tem o servidor em Windows e um Linux de apoio.
- O motor é um binário Go único por plataforma, sem CGO ([ADR 0001](0001-stack-go.md)): o que muda entre Linux e Windows é pouco e já existe dos dois lados (serviço, PTY/ConPTY, caminhos, firewall).
- O nó Windows tinha um processo na sessão do usuário, iniciado por tarefa agendada no logon, só para agentes com tela e para o navegador que criava GitHub Apps. Isso prendia parte do trabalho a um usuário logado, criava um canal local a mais e não tinha equivalente no Linux.
- Os agentes de terminal dos três provedores já navegam na web pelas ferramentas MCP de navegador do próprio provedor, e o GitHub permite criar um App a partir de um manifesto com um clique do usuário ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md)).
- A segurança do Core no Linux depende de contas separadas: o motor, dono do cofre, roda como `mestre`; os agentes, como `mestre-agente` ([segurança](../arquitetura/seguranca.md)).

## Opções consideradas

1. **Core só em Linux; nós em Linux ou Windows.**
   - Prós: um sistema a menos para o Core; o cofre fica só no modelo de contas do Linux.
   - Contras: quem só tem Windows não usa o Mestre; uma máquina só (Linux) fica sem caminho claro.
2. **Core em Linux ou Windows, nós em qualquer um dos dois, mesmo binário.**
   - Prós: quatro usos com o mesmo código; uma máquina só já funciona.
   - Contras: o Core no Windows precisa de separação de contas própria; a suíte pesada cresce.
3. **Core no Windows com um serviço só, rodando motor e agentes na conta do usuário.**
   - Prós: mais simples de instalar.
   - Contras: os agentes rodariam com a conta que lê o cofre. Descartada.

## Decisão

- **Quatro usos desde o nascimento**; em todos, o Core aceita N nós de qualquer sistema:

  | Uso | Core | Nós | Observação |
  |---|---|---|---|
  | 1. Só Linux | Linux, com o nó local | nenhum | máquina única |
  | 2. Linux + Windows | Linux | Windows (e Linux) | o uso do autor |
  | 3. Windows + Linux | Windows, com o nó local | Linux (e Windows) | — |
  | 4. Só Windows | Windows, com o nó local | nenhum | máquina única |

- **Mesmo binário, partes por sistema:** serviço do systemd com watchdog no Linux e serviço do Windows com recuperação; caminhos (`/var/lib/mestre/…` ou `%ProgramData%\Mestre\…`); regras de firewall no [checklist](../plataforma/checklist.md); anúncio mDNS pelo Avahi no Linux e pelo mDNS nativo do Windows, registrado pelo Guardião, no Windows ([descoberta](../conectividade/descoberta.md)); `whisper-server` e `ffmpeg` para os dois sistemas; ajudante de troca `mestre-update` nos dois ([componentes](../arquitetura/componentes.md)).
- **Agente nunca roda com a conta dona do cofre**, nos dois sistemas. Em toda máquina Core isso vira dois serviços: `mestre-core`, dono do cofre, do banco, do espelho e do painel, que nunca roda agentes (Linux: conta `mestre`; Windows: conta virtual `NT SERVICE\MestreCore`); e o nó local `mestre-node`, que roda os agentes, inclusive o Grão-Mestre (Linux: conta `mestre`, iniciando os agentes como `mestre-agente`; Windows: conta do usuário), ligado ao Core pela mesma WSS com mTLS de um nó remoto, em `127.0.0.1:7443`, com banco de nó próprio ([componentes](../arquitetura/componentes.md), [Windows](../arquitetura/windows.md)).
- **Sem interface gráfica em lugar nenhum.** O acesso é pelo painel no navegador; os agentes são de terminal; os nós, nos dois sistemas, são só serviços. Saem o processo na sessão do usuário, a tarefa de logon, o canal entre serviço e sessão, a notificação na área de trabalho, a capacidade `gui` e o navegador dedicado.
  - Navegação web dos agentes: pelas ferramentas MCP de navegador do próprio provedor, liberadas ou negadas pela classe como qualquer ferramenta ([permissões](../classes/permissoes.md)). O Mestre não fornece nem gerencia navegador.
  - GitHub Apps: o Grão-Mestre prepara o manifesto e o usuário cria e instala com um clique por link ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md)).
- **Modo máquina única** (usos 1 e 4): a autocura usa as camadas 1, 2 e 5; não há supervisão cruzada; o diagnóstico com IA, a página reserva e o Telegram em modo reduzido ficam com o Guardião da própria máquina ([camadas](../autocura/camadas.md#modo-máquina-única)).
- **Instalação:** os dois instaladores perguntam o papel da máquina: "Core" (com o nó local) ou "nó" ([instalação](../plataforma/instalacao-e-atualizacao.md)).
- **Testes:** a suíte pesada cobre instalação e autocura nos quatro usos; os demais testes de ponta a ponta rodam uma vez, no uso Linux + Windows ([testes](../qualidade/testes.md)). Critério global MVP-13 ([escopo](../produto/escopo-mvp.md)).

## Consequências

- **Positivas:**
  - Quem tem uma máquina só, de qualquer sistema, usa o Mestre.
  - Linux e Windows ficam simétricos: nó é serviço, agentes rodam deslogado, nenhuma parte depende de alguém estar na frente da tela.
  - Menos peças no Windows: um processo e um canal local a menos.
- **Negativas:**
  - **Máquina única não tem quem perceba a queda da máquina inteira.** O painel e os guias dizem isso; a recomendação é parear um segundo nó ([checklist](../plataforma/checklist.md)).
  - **Core com dois serviços do motor, nos dois sistemas:** mais um processo a vigiar; no Windows, também ACLs nas pastas dos workspaces para a conta do usuário e a senha dessa conta pedida na instalação ([Windows](../arquitetura/windows.md)).
  - **Laboratório maior:** a pesada monta os quatro usos a partir de duas VMs ([testes](../qualidade/testes.md#laboratório)).
  - **Agentes não usam programas com janela.** Trabalho que exigiria tela é feito pelas ferramentas do provedor (MCP) ou não é feito pelo Mestre.
  - **Criação de App depende de um clique do usuário**, por link no Telegram ou no painel; até lá a classe usa o App "Mestre" ([identidades e Apps](../github/identidades-e-apps.md)).
