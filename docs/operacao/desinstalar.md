# Desinstalar

> **Assunto:** passo a passo para remover uma Torre, a Cidadela e o que o Mestre deixou no GitHub, no Telegram e nos aparelhos · **Componente:** Todos · **Atualizado:** 2026-10-19

Guia para quem usa. O que a remoção apaga por dentro está em [instalação e versões](../plataforma/instalacao-e-atualizacao.md#remoção-mestre-uninstall); onde ficam os dados, em [persistência](../arquitetura/persistencia.md#arquivos).

**Regra geral:** `mestre uninstall` remove serviços, a conta dos agentes no Windows, regras de firewall e programas. Numa **Torre**, apaga também a réplica (banco, arquivos e espelhos), a cópia do cofre, a chave privada do motor, o que o banco local da máquina (`maquina.db`) guardava da Guilda e, se a máquina já foi Cidadela, os bancos descartados, as cópias e as transferências que ela guardou ([lista](../guilda/replicacao.md#cofre-em-toda-máquina)): o que a réplica tem está na Cidadela, e o que os bancos descartados tinham de a mais só entra na auditoria se a máquina o relatou antes. Na **Cidadela sozinha**, mantém os dados (banco, backups, worktrees, logs, chave do cofre), a não ser que você peça para apagar tudo, para dar para reinstalar sem perder nada.

## Antes de começar

- Veja no painel as **mesas abertas**. Trabalho que ainda não foi enviado ao GitHub existe só nas máquinas do Mestre; encerre as mesas ou peça ao Grão-Mestre para enviar o que importa.
- Para guardar uma cópia, copie a pasta de backups da Cidadela antes de apagar dados (Ubuntu: `/var/lib/mestre/<instância>/backups/`; Windows: `%ProgramData%\Mestre\<instância>\backups\`). O cofre vai nela, cifrado; para restaurar, você precisa da **chave de recuperação** guardada na instalação ([cofre](../plataforma/cofre.md#backup-e-chave-de-recuperação)).

## Remover uma Torre

1. Na Torre, rode `mestre uninstall` como administrador (PowerShell **como administrador** no Windows; `sudo mestre uninstall` no Ubuntu, seguido de `sudo apt remove mestre`). Ele pede antes a saída da Guilda à Cidadela; com a Cidadela fora, desinstala assim mesmo e o painel mostra a máquina como "sumida": remova-a no painel ([remover uma máquina](entrar-na-guilda.md#remover-uma-máquina)).
2. Isso para e remove os serviços `mestre`, `mestre-launcher`, `mestre-guardiao` e `mestre-update`, a conta `mestre-agente` e o perfil dela (no Windows), a tarefa agendada `\Mestre\Operador` (se o Operador estava habilitado), as regras de firewall do Mestre e os programas. Os logins dos perfis de conta feitos por essa conta deixam de valer: numa reinstalação no Windows, refaça o login de cada perfil ([primeiro acesso](primeiro-acesso.md#4-configurar-os-perfis-de-conta)).
3. Confira: `Get-Service mestre*` e `Get-LocalUser mestre-agente` (Windows) ou `systemctl status mestre` (Ubuntu) não acham nada.
4. A réplica e a cópia do cofre já foram apagadas. O que sobra (logs, worktrees locais, `workspaces/` e perfis de conta com os logins, em `contas/`) fica em `%ProgramData%\Mestre\` ou em `/etc/mestre/`, `/var/lib/mestre/` e `/var/log/mestre/`. Se não vai reinstalar, rode `mestre uninstall --purge`, ou apague as pastas à mão.

## Remover a Cidadela

- **Com Torres na Guilda:** o `mestre uninstall` recusa. Antes, **passe a Cidadela** a uma Torre não excluída e na rede local, pelo painel, com o PIN ([Cidadela e Torres](../guilda/cidadela-e-torres.md)); a antiga vira Torre e sai como na seção acima. Para acabar com a Guilda inteira, remova todas as Torres primeiro.
- **Sozinha:** na máquina, como administrador, `sudo mestre uninstall` e depois `sudo apt remove mestre` (Ubuntu), ou `mestre uninstall` num PowerShell de administrador, ou "Desinstalar" em Aplicativos instalados (Windows). A Guilda acaba.
- Isso remove os mesmos serviços, conta e regras de uma Torre. Confira como na seção acima.
- Os dados ficam nas pastas de [persistência](../arquitetura/persistencia.md#arquivos): cofre, banco, espelhos, worktrees, anexos, perfis de conta dos provedores e a CA. Se não vai reinstalar, use `mestre uninstall --purge` ou apague essas pastas. Sem a chave do cofre, os segredos guardados não podem mais ser lidos.

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
- Se você escolheu **versionar `.mestre/`** no projeto ([sessão zero da campanha](../sessao-zero/workspace.md)), a pasta continua no repositório. Remova por PR, se quiser.
- O `AGENTS.md` é do projeto e fica como está.
