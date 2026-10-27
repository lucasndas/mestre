# Atualizar

> **Assunto:** passo a passo para aprovar e acompanhar a atualização do Mestre, acompanhar a dos CLIs dos provedores, e voltar de versão · **Componente:** Todos · **Atualizado:** 2026-10-19

Guia para quem usa. O Mestre se atualiza sozinho, máquina por máquina, depois que alguém aprova; você não precisa entrar no servidor nem no PC. Como funciona por dentro: [autoatualização](../plataforma/autoatualizacao.md) e [instalação e versões](../plataforma/instalacao-e-atualizacao.md).

## Atualizar o Mestre

1. **Aviso.** Quando sai uma versão nova, o Senhor da Guilda e os Administradores recebem um pedido de aprovação marcado **"Sistema"**, no Telegram e na central de aprovações do painel. Ele traz as notas da versão e a ordem em que as máquinas serão atualizadas. Para procurar na hora, use **Verificar agora** no painel.
2. **Decidir.** Leia as notas e toque em **Aprovar** ou **Negar**. O Grão-Mestre pode explicar a versão, mas não aprova esse tipo de pedido. Um "Aprovar" basta.
3. **Esperar.** Depois de aprovar, não há mais nada a fazer. O Mestre atualiza uma peça por vez: cada Torre (motor e Guardião), a Torre condutora por último, e depois o motor e o Guardião da Cidadela. Numa máquina única (só Linux ou só Windows), são só as duas peças da Cidadela. Máquinas em versões diferentes convivem durante o processo.
4. **Agentes.** Eles continuam rodando durante a troca: o motor reinicia sem esperar ninguém e os personagens seguem no estado em que estavam ([terminais durante a atualização](../plataforma/autoatualizacao.md#terminais-durante-a-atualização)).
5. **Conferir.** Em **Guilda e saúde das máquinas**, a coluna **Motor e Guardião** mostra a versão de cada peça. Todas devem estar na versão nova.

Atualizações do Mestre não passam pelo `apt` nem pelo instalador do Windows: depois da primeira instalação, elas chegam só por esse fluxo.

### Se der errado

- **Volta sozinha.** Se o motor ou o Guardião novo não ficar saudável logo após a troca, a peça volta para a versão anterior sozinha, a atualização para ali e você recebe um alerta "Sistema" com o motivo. As máquinas que já foram atualizadas continuam funcionando.
- Aquela versão só é tentada de novo com uma aprovação nova.
- Se até a volta falhar, a autocura assume e avisa ([camadas](../autocura/camadas.md)).

### Voltar de versão por conta própria

1. Em **Guilda e saúde das máquinas**, escolha a máquina e peça ao Guardião a ação **voltar versão** (`rollback`). Ela volta o motor para a versão anterior guardada, sem mexer no banco ([Guardião](../autocura/guardiao.md#lista-fechada-de-ações)).
2. Voltar **com restauração do banco** é raro e nunca acontece sozinho: só serve quando a versão anterior não funciona com o banco migrado. Apaga tudo o que aconteceu entre a atualização e a volta (o pedido mostra esse intervalo), por isso exige aprovação de Senhor da Guilda ou Administrador, com PIN ([Guardião](../autocura/guardiao.md#rollback-com-restauração-do-banco)).
3. Com a Cidadela fora do ar, use a [página reserva](../autocura/pagina-reserva.md) de uma Torre (porta `7446`; numa máquina única, a da própria Cidadela), que tem a ação de recuperar a Cidadela.

### Torre que ficou muito tempo desligada

Se uma Torre ficou desligada por tanto tempo que a versão dela não conversa mais com a Cidadela, o painel mostra a máquina como fora da versão aceita. Atualize essa máquina à mão: rode o instalador mais novo do sistema dela (`mestre-<versão>-x64.msi` ou `install.sh`), como na [instalação](instalar.md). Ele mantém a entrada na Guilda e os dados (e, no Windows com o Operador habilitado, a tarefa agendada `\Mestre\Operador`).

## CLIs dos provedores

`claude`, `codex` e `agy` se atualizam sozinhos; o Mestre não pede aprovação nem instala versões deles ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).

1. Quando um CLI muda de versão numa máquina, o Mestre percebe e roda o [teste de conformidade](../provedores/conformidade.md) com a versão nova.
2. Passou: nada a fazer; os agentes novos já usam a versão nova, e os que estavam rodando seguem na deles até o próximo reinício natural.
3. Falhou: você recebe um aviso com o relatório, e aquele provedor deixa de receber agentes novos naquela máquina até uma versão que passe. Não há volta automática de versão.

## Depois de atualizar

- Veja a coluna **Checklist** em **Guilda e saúde das máquinas**: uma versão nova pode trazer verificações novas.
- Nada muda nos aparelhos que abrem o painel: a CA e os endereços continuam os mesmos.
