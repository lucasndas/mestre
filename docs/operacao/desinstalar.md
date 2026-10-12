# Desinstalar

> **Assunto:** passo a passo para remover um nó, o Core e o que o Mestre deixou no GitHub, no Telegram e nos aparelhos · **Componente:** Todos · **Atualizado:** 2026-10-12

Guia para quem usa. O que a remoção apaga por dentro está em [instalação e versões](../plataforma/instalacao-e-atualizacao.md#remoção-mestre-uninstall); onde ficam os dados, em [persistência](../arquitetura/persistencia.md#arquivos).

**Regra geral:** `mestre uninstall` remove serviços, tarefas, regras de firewall e programas, mas **mantém os dados** (banco, backups, worktrees, logs, chave do cofre), a não ser que você peça para apagar tudo. Assim dá para reinstalar sem perder nada.

## Antes de começar

- Veja no painel as **mesas abertas**. Trabalho que ainda não foi enviado ao GitHub existe só nas máquinas do Mestre; encerre as mesas ou peça ao Grão-Mestre para enviar o que importa.
- Para guardar uma cópia, copie a pasta de backups do banco e a chave do cofre antes de apagar dados (Core no Ubuntu: `/var/lib/mestre/backups/` e `/etc/mestre/cofre.key`; Core no Windows: `%ProgramData%\Mestre\backups\` e a cópia feita por `mestre core export-key`).

## Remover um nó

1. No painel, em **Saúde dos nós**, **revogue** o nó e confirme com o PIN ([parear nó](parear-no.md#revogar-um-nó)).
2. Na máquina do nó, rode `mestre uninstall` como administrador (PowerShell **como administrador** no Windows; `sudo mestre uninstall` no Ubuntu, seguido de `sudo apt remove mestre`).
3. Isso para e remove os serviços `mestre-node` e `mestre-guardiao`, a tarefa agendada `\Mestre\Diagnostico` (no Windows), as regras de firewall do Mestre e os programas.
4. Confira: `Get-Service mestre-*` (Windows) ou `systemctl status mestre-node` (Ubuntu) não acha nada.
5. Os dados ficam em `%ProgramData%\Mestre\` ou em `/etc/mestre/`, `/var/lib/mestre/` e `/var/log/mestre/`. Se não vai reinstalar, peça ao `mestre uninstall` para apagar tudo, ou apague as pastas à mão.

## Remover o Core

1. Remova antes todos os nós remotos (seção acima). Nós sem Core ficam tentando reconectar.
2. Na máquina do Core, como administrador:
   - **Ubuntu:** `sudo mestre uninstall` e depois `sudo apt remove mestre`;
   - **Windows:** `mestre uninstall` num PowerShell de administrador, ou "Desinstalar" em Aplicativos instalados.
3. Isso para e remove os serviços do Core (`mestre-core`, o nó local `mestre-node` e `mestre-guardiao`), a tarefa agendada no Windows e as regras de firewall.
4. Confira: `systemctl status mestre-core mestre-node` responde que as unidades não existem, ou `Get-Service mestre-*` não acha nada.
5. Os dados ficam nas pastas de [persistência](../arquitetura/persistencia.md#arquivos): cofre, banco, espelhos, worktrees, anexos, perfis de conta dos provedores e a CA. Se não vai reinstalar, peça para apagar tudo ou apague essas pastas. Sem a chave do cofre, os segredos guardados não podem mais ser lidos.

## Limpar fora das máquinas

### GitHub

- **Apps do Mestre** ("Grão-Mestre", "Mestre" e um por classe): para cada um, GitHub → Settings → Developer settings → GitHub Apps → o App → Advanced → **Delete GitHub App**. Isso também remove as instalações.
- **Sua credencial pessoal:** GitHub → Settings → Applications → Authorized GitHub Apps → "Grão-Mestre" → **Revoke** (se o App ainda existir).
- **Regras de proteção** que você aprovou para o Mestre (por exemplo, o App "Grão-Mestre" como exceção num ruleset): reveja em cada repositório, em Settings → Rules ([aprovação no GitHub](../github/aprovacao-no-github.md)).
- **O que fica no histórico:** épicos (issues com rótulo `epico`), sub-issues, PRs, comentários e commits continuam no GitHub. Branches `epico/...` que sobraram podem ser apagadas à mão.

### Telegram

- No @BotFather, envie `/deletebot` e escolha o bot do Mestre. Se for reinstalar e quiser manter o mesmo bot, use `/revoke` em vez disso e cole o token novo depois ([parear Telegram](parear-telegram.md#se-o-token-vazar)).

### Aparelhos

- Remova a CA do Mestre de cada aparelho onde você a instalou ([primeiro acesso](primeiro-acesso.md#1-confiar-na-ca-do-mestre)):
  - Windows: `certmgr.msc` → Autoridades de Certificação Raiz Confiáveis → Certificados → CA do Mestre → Excluir;
  - Firefox: Ver certificados → Autoridades → CA do Mestre → Excluir ou deixar de confiar;
  - Ubuntu: apague `/usr/local/share/ca-certificates/mestre-ca.crt` e rode `sudo update-ca-certificates --fresh`;
  - macOS: Acesso às Chaves → apague o certificado;
  - Android: Criptografia e credenciais → Credenciais confiáveis → Usuário → remover;
  - iPhone e iPad: Ajustes → Geral → VPN e Gerenciamento de Dispositivo → remover o perfil.
- Uma CA que não é mais usada não deve ficar instalada: quem tivesse a chave dela poderia se passar por sites nesses aparelhos.

### Repositórios

- Os arquivos gerados pelo Mestre (skills copiadas, hooks, `.mestre/agents/`) só existem nas cópias do Mestre e somem com os dados.
- Se você escolheu **versionar `.mestre/`** no projeto ([sessão zero do workspace](../sessao-zero/workspace.md)), a pasta continua no repositório. Remova por PR, se quiser.
- O `AGENTS.md` é do projeto e fica como está.
