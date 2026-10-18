# ADR 0021: Operador do PC, a única exceção à conta do usuário

> **Assunto:** por que existe uma base especial que roda, por tempo curto e com aprovação do Dono, um agente com a conta do usuário numa máquina Windows, e como o risco é contido · **Componente:** Todos · **Atualizado:** 2026-10-18

## Status

Aceita, 2026-10-12. Abre uma exceção ao princípio do [ADR 0020](0020-linux-e-windows-em-qualquer-papel.md) "nenhum processo do Mestre usa a conta do usuário". **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda. **Revista em 2026-10-18** pelo [ADR 0026](0026-mcp-por-classe.md): os MCPs do Operador vêm do catálogo pelo gateway, como os de toda classe, e o `--chrome` sai; o navegador do usuário vem de um servidor de controle do catálogo.

## Contexto

- Os personagens comuns rodam como `mestre-agente`, sem acesso aos arquivos, aos programas abertos nem às sessões de navegador do usuário, e navegam pelo próprio MCP de navegador headless ([segurança](../arquitetura/seguranca.md#navegação-web-e-login-em-sites)).
- Parte do trabalho só existe na área de trabalho do usuário: um site com captcha interativo, em que o login pelo [cofre](../plataforma/cofre.md#login-em-sites) falha e só o Chrome do usuário, já logado, entra; um programa com janela; uma configuração feita na tela.
- O Claude Code tem integração oficial com a extensão Claude in Chrome (`--chrome`), que age no Chrome do usuário, e existem MCPs de controle do computador (mouse, teclado, capturas). Os dois só funcionam na sessão interativa do usuário e com a conta dele.
- Com a conta do usuário, o agente alcança tudo o que o usuário alcança. Ações de mouse e teclado não passam pelo hook com o detalhe de um comando: o despachante vê "clicar em (x, y)", não o que o clique faz.

## Opções consideradas

1. **Não oferecer.**
   - Prós: o princípio fica sem exceção.
   - Contras: login com segundo fator e trabalho na tela ficam sempre com o usuário; o Mestre não ajuda onde ajudaria mais.
2. **Personagens comuns com acesso ao navegador ou à tela do usuário, liberado pela classe.**
   - Prós: nenhuma peça nova.
   - Contras: qualquer classe com a ferramenta liberada age com a conta do usuário, a qualquer hora, sem prazo. Descartada.
3. **Base especial, só em máquina Windows, ativada por aprovação do Dono, com prazo curto, aviso visível e auditoria reforçada.**
   - Prós: o acesso existe só quando o Dono decide, por um tempo que ele vê correr, e cada sessão fica registrada com capturas de tela.
   - Contras: um processo do Mestre passa a rodar na sessão do usuário; mouse e teclado não são filtráveis pelo hook.

## Decisão

Opção 3, o **Operador do PC**. O comportamento detalhado está em [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc); a parte do Windows em [Windows](../arquitetura/windows.md#operador-do-pc).

- **Onde:** só em máquina Windows (Cidadela ou Torre), se a instalação habilitar. A tarefa agendada `\Mestre\Operador` inicia `mestre operator-session` no logon do usuário configurado, com a conta dele e privilégios normais; esse processo fala com o `mestre` por named pipe e cria os terminais do Operador. A máquina anuncia `operador` na ficha da máquina só com esse processo ativo e a sessão desbloqueada.
- **Quem pode:** classes com `operator: true`, permitido pelo teto do workspace (`operator: false` por padrão; só o Dono liga) ([permissões](../classes/permissoes.md), [teto](../classes/teto-do-workspace.md)).
- **Ativação:** pedido `ativacao_operador`, aprovado só pelo Dono, com mesa, missão, tarefa e tempo (padrão 30 min). O Grão-Mestre só aprova com um decreto de delegação do Dono, com escopo e validade curta ([política](../aprovacoes/politica.md), [decretos](../telegram/decretos.md)).
- **Motivos típicos de ativação:** login em site que o motor não conclui sozinho: código de segundo fator, aprovação no celular e captcha de imagem o motor resolve com o usuário pelo Telegram; captcha interativo ou falha geral viram `login_requires_human`, e o Grão-Mestre propõe o Operador, que entra pelo Chrome do usuário já logado ([cofre](../plataforma/cofre.md#segundo-fator-e-captcha)); trabalho em programa com janela ou no Chrome do usuário.
- **Enquanto ativo:** aviso fixo com contagem regressiva e botão **Encerrar agora** no Telegram e no painel, faixa "Mestre operando — até HH:MM" com botão **Parar** na área de trabalho, `/operador encerrar`, e `/travar` derruba na hora. No fim do prazo o motor interrompe, espera 60 s e encerra a árvore de processos.
- **Auditoria reforçada:** ações completas pelos hooks e capturas de tela a cada 30 s e a cada ação de controle do computador, cifradas na Cidadela, vistas só pelo Dono, guardadas 30 dias ([persistência](../arquitetura/persistencia.md#operador-do-pc)).
- **Fora da base Operador, nunca:** servidores MCP de controle do computador e do navegador do usuário só servem à base Operador: o motor os julga pelo pacote ou endereço e pela base, qualquer que seja o nome ou o transporte; só o Dono os cadastra no catálogo, e eles chegam pelo gateway, como os de toda classe ([MCP por classe](../classes/mcp-por-classe.md)); `--chrome` é recusado em toda base ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Cofre e pastas do Mestre:** com UAC ligado o processo roda com token filtrado; `%ProgramData%\Mestre` não dá acesso à conta do usuário e a chave do cofre só a `NT SERVICE\MestreCore`. Contra o Operador clicar "Sim" num UAC, a área de trabalho segura (UIPI bloqueia cliques sintéticos), exigida pelo [checklist](../plataforma/checklist.md); comandos de elevação são negados por regra fixa ([segurança](../arquitetura/seguranca.md#operador-do-pc)).

## Consequências

- **Positivas:**
  - O Mestre resolve o que antes ficava sempre com o usuário (captcha interativo, programas com janela), sem dar a nenhum personagem comum acesso à conta dele.
  - Cada sessão tem dono, prazo, motivo e registro visual; o usuário vê na própria tela que o Mestre está operando e pode parar.
- **Negativas:**
  - **Risco residual:** mouse e teclado não são filtrados pelo hook. A proteção é prazo curto, aprovação explícita do Dono, aviso visível, `/travar` e auditoria com capturas.
  - Um processo do Mestre roda na sessão do usuário enquanto o Operador estiver habilitado, e o Operador só funciona com o usuário logado e a tela desbloqueada.
  - Mais peças na máquina Windows: tarefa agendada, processo da sessão, pipe, aviso na área de trabalho; a suíte pesada ganha o MVP-14 ([testes](../qualidade/testes.md#mapa-do-mvp-para-a-suíte-pesada)).
  - Capturas de tela podem mostrar dados pessoais; por isso cifradas, só para o Dono e com retenção curta.

## Fontes

- Claude Code com Chrome (`--chrome`, substituído na revisão de 2026-10-18): https://code.claude.com/docs/en/chrome
- Playwright MCP, modo `--extension` (Chrome do usuário já logado): https://github.com/microsoft/playwright-mcp
- Controle de Conta de Usuário (área de trabalho segura, `PromptOnSecureDesktop`, `ConsentPromptBehaviorAdmin`): https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/settings-and-configuration
- UIPI (isolamento de privilégio da interface): https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/how-it-works
