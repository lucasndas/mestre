# ADR 0020: Linux e Windows em qualquer papel, sem interface gráfica

> **Assunto:** por que o Mestre roda com o mesmo binário em Linux e Windows, em qualquer papel e nas quatro composições (só Linux, Linux + Windows, Windows + Linux, só Windows), e sem nenhuma interface gráfica além do painel no navegador · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. Revisada em 2026-10-12: no Windows, o serviço que roda os agentes deixa a conta do usuário e passa a uma conta virtual, e os agentes rodam na conta local própria `mestre-agente`, como no Linux; saem a senha do usuário na instalação e a tarefa agendada de diagnóstico. Exceção aberta em 2026-10-12 pelo [ADR 0021](0021-operador-do-pc.md): o Operador do PC, opcional, roda com a conta do usuário quando habilitado e ativado. **Revista em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): os papéis fixos de servidor e de máquina de apoio saem; cada máquina tem uma só instalação, com o serviço `mestre` (Linux: conta `mestre`; Windows: conta virtual `NT SERVICE\Mestre`) e os agentes como `mestre-agente`; a instalação não pergunta papel; os quatro usos viram as quatro composições da Guilda; e, como qualquer máquina pode virar Cidadela, tudo roda nos dois sistemas. Contexto, opções e decisão abaixo já usam os nomes novos.

## Contexto

- O desenho começou com um servidor Ubuntu no comando e um PC Windows de apoio, as máquinas do autor. Muita gente tem só uma máquina, ou tem o servidor em Windows e um Linux de apoio.
- O motor é um binário Go único por plataforma, sem CGO ([ADR 0001](0001-stack-go.md)): o que muda entre Linux e Windows é pouco e já existe dos dois lados (serviço, PTY/ConPTY, caminhos, firewall).
- A máquina Windows tinha um processo na sessão do usuário, iniciado por tarefa agendada no logon, só para agentes com tela e para o navegador que criava GitHub Apps. Isso prendia parte do trabalho a um usuário logado, criava um canal local a mais e não tinha equivalente no Linux.
- Os agentes de terminal dos três provedores navegam na web por ferramentas MCP de navegador, e o GitHub permite criar um App a partir de um manifesto com um clique do usuário ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md)).
- A segurança da máquina no comando, no Linux, depende de contas separadas: o motor, dono do cofre, roda como `mestre`; os agentes, como `mestre-agente` ([segurança](../arquitetura/seguranca.md)).

## Opções consideradas

1. **Cidadela só em Linux; Torres em Linux ou Windows.**
   - Prós: um sistema a menos para a Cidadela; o cofre fica só no modelo de contas do Linux.
   - Contras: quem só tem Windows não usa o Mestre; uma máquina só (Linux) fica sem caminho claro.
2. **Cidadela em Linux ou Windows, Torres em qualquer um dos dois, mesmo binário.**
   - Prós: quatro composições com o mesmo código; uma máquina só já funciona.
   - Contras: a Cidadela no Windows precisa de separação de contas própria; a suíte pesada cresce.
3. **No Windows, motor e agentes na conta do usuário, num serviço só.**
   - Prós: mais simples de instalar.
   - Contras: os agentes rodariam com a conta que lê o cofre. Descartada.
4. **No Windows, o serviço que roda os agentes e os agentes na conta do usuário, e o cofre noutra conta** (o desenho anterior à revisão de 2026-10-12).
   - Prós: os CLIs acham os logins que o usuário já tem.
   - Contras: os agentes alcançam os arquivos pessoais e as sessões de navegador do usuário e os dados do motor; a instalação precisa da senha do usuário, e trocá-la derruba o serviço; o diagnóstico precisava de uma tarefa agendada. Descartada na revisão.

## Decisão

- **Quatro composições desde o nascimento** (os antigos "quatro usos"); em todas, a Guilda aceita N máquinas de qualquer sistema e qualquer uma pode virar Cidadela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)):

  | Composição | Cidadela | Torres | Observação |
  |---|---|---|---|
  | 1. Só Linux | Linux | nenhuma | Guilda de uma máquina (modo máquina única) |
  | 2. Cidadela Linux + Torre Windows | Linux | Windows (e Linux) | o uso do autor |
  | 3. Cidadela Windows + Torre Linux | Windows | Linux (e Windows) | — |
  | 4. Só Windows | Windows | nenhuma | Guilda de uma máquina (modo máquina única) |

- **Mesmo binário e mesmo instalador, partes por sistema:** serviço do systemd com watchdog no Linux e serviço do Windows com recuperação; caminhos (`/var/lib/mestre/…` ou `%ProgramData%\Mestre\…`); regras de firewall no [checklist](../plataforma/checklist.md); anúncio mDNS pelo Avahi no Linux e pelo mDNS nativo do Windows, registrado pelo Guardião, no Windows ([descoberta](../conectividade/descoberta.md)); `whisper-server` e `ffmpeg` para os dois sistemas; ajudante de troca `mestre-update` nos dois ([componentes](../arquitetura/componentes.md)).
- **Tudo roda nos dois sistemas:** painel, Telegram, transcrição, banco, GitHub e cofre, porque qualquer máquina pode virar Cidadela ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#tudo-roda-nos-dois-sistemas)).
- **Agente nunca roda com a conta dona do cofre nem com a conta do usuário**, nos dois sistemas. Cada máquina tem um serviço do motor, `mestre`, dono do cofre, do banco e da configuração (Linux: conta `mestre`; Windows: conta virtual `NT SERVICE\Mestre`), e os agentes, inclusive o Grão-Mestre, rodam com a conta `mestre-agente`, iniciados pelo serviço mínimo `mestre-launcher` (desde o [ADR 0023](0023-terminais-fora-do-motor.md); no Linux, o único com `CAP_SETUID`/`CAP_SETGID`; no Windows, LocalSystem) ([componentes](../arquitetura/componentes.md), [Windows](../arquitetura/windows.md)).
- **Sem interface gráfica em lugar nenhum.** O acesso é pelo painel no navegador; os agentes são de terminal; as máquinas, nos dois sistemas, são só serviços. Saem o processo na sessão do usuário, a tarefa de logon, o canal entre serviço e sessão, a notificação na área de trabalho e o navegador dedicado. A ficha da máquina informa se há interface ([ficha da máquina](../guilda/ficha-da-maquina.md)), mas só o Operador do PC a usa.
  - Navegação web dos agentes: pelo MCP de navegador próprio de cada personagem (ex.: Playwright MCP headless), como `mestre-agente` e com navegador limpo, liberado ou negado pela classe como qualquer ferramenta ([permissões](../classes/permissoes.md)); login em sites só com credencial do cofre ([cofre](../plataforma/cofre.md#login-em-sites)).
  - GitHub Apps: o Grão-Mestre prepara o manifesto e o usuário cria e instala com um clique por link ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md)).
- **Modo máquina única** (composições 1 e 4): a autocura usa as camadas 1, 2 e 5; não há supervisão cruzada nem troca de Cidadela; o diagnóstico com IA, a página reserva e o Telegram em modo reduzido ficam com o Guardião da própria máquina ([camadas](../autocura/camadas.md#modo-máquina-única)).
- **Instalação:** o instalador único não pergunta papel: descobre a Guilda e entra nela ou a cria ([Guilda](../guilda/guilda.md)).
- **Testes:** a suíte pesada cobre instalação, autocura e troca de Cidadela nas composições, com a Cidadela em cada sistema; os demais testes de ponta a ponta rodam uma vez, na composição Cidadela Linux + Torre Windows ([testes](../qualidade/testes.md)). Critérios globais MVP-13 e MVP-15 ([escopo](../produto/escopo-mvp.md)).

## Consequências

- **Positivas:**
  - Quem tem uma máquina só, de qualquer sistema, usa o Mestre.
  - Nenhum processo do Mestre usa a conta do usuário (fora o Operador do PC): os agentes não alcançam os arquivos pessoais nem as sessões de navegador dele, e trocar a senha do Windows não afeta o Mestre.
  - Linux e Windows ficam simétricos: a mesma instalação, serviços, agentes rodando deslogado, nenhuma parte dependendo de alguém na frente da tela.
- **Negativas:**
  - **Máquina única não tem quem perceba a queda da máquina inteira** nem quem assuma. O painel e os guias dizem isso; a recomendação é pôr uma segunda máquina na Guilda ([checklist](../plataforma/checklist.md)).
  - **No Windows,** a conta `mestre-agente` (senha guardada pelo Mestre, sem logon interativo), os privilégios da conta virtual do `mestre` para iniciar processos com ela e as ACLs nas pastas dos workspaces ([Windows](../arquitetura/windows.md)).
  - **Laboratório maior:** a pesada monta as composições a partir de duas VMs ([testes](../qualidade/testes.md#laboratório)).
  - **Agentes não usam programas com janela.** Trabalho que exigiria tela é feito pelas ferramentas do provedor (MCP) ou não é feito pelo Mestre.
  - **Criação de App depende de um clique do usuário**, por link no Telegram ou no painel; até lá a classe usa o App "Mestre" ([identidades e Apps](../github/identidades-e-apps.md)).
