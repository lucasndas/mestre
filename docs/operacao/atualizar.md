# Atualizar

> **Assunto:** passo a passo para aprovar e acompanhar a atualização do Mestre e dos CLIs dos provedores, e para voltar de versão · **Componente:** Todos · **Atualizado:** 2026-10-12

Guia para quem usa. O Mestre se atualiza sozinho, máquina por máquina, depois que alguém aprova; você não precisa entrar no servidor nem no PC. Como funciona por dentro: [autoatualização](../plataforma/autoatualizacao.md) e [instalação e versões](../plataforma/instalacao-e-atualizacao.md).

## Atualizar o Mestre

1. **Aviso.** Quando sai uma versão nova, o Dono e os Administradores recebem um pedido de aprovação marcado **"Sistema"**, no Telegram e na central de aprovações do painel. Ele traz as notas da versão e a ordem em que as máquinas serão atualizadas. Para procurar na hora, use **Verificar agora** no painel.
2. **Decidir.** Leia as notas e toque em **Aprovar** ou **Negar**. O Grão-Mestre pode explicar a versão, mas não aprova esse tipo de pedido. Um "Aprovar" basta.
3. **Esperar.** Depois de aprovar, não há mais nada a fazer. O Mestre atualiza uma peça por vez: motor do Core, Guardião do Core e depois cada nó (motor e Guardião). Numa máquina única (só Linux ou só Windows), são só as duas peças do Core; na máquina do Core (Ubuntu ou Windows), os dois serviços do motor reiniciam um depois do outro: primeiro `mestre-core`, depois o nó local `mestre-node`. Core e nós de versões diferentes convivem durante o processo.
4. **Agentes ocupados.** Antes de reiniciar o motor de uma máquina, o Mestre espera cada agente dali terminar o que está fazendo. Se demorar demais várias vezes, você recebe a escolha:
   - **Aplicar agora:** os agentes ocupados são interrompidos e retomados depois do reinício;
   - **Continuar esperando.**
5. **Conferir.** Em **Saúde dos nós**, a coluna **Motor e Guardião** mostra a versão de cada peça. Todas devem estar na versão nova.

Atualizações do Mestre não passam pelo `apt` nem pelo instalador do Windows: depois da primeira instalação, elas chegam só por esse fluxo.

### Se der errado

- **Volta sozinha.** Se o motor ou o Guardião novo não ficar saudável logo após a troca, a peça volta para a versão anterior sozinha, a atualização para ali e você recebe um alerta "Sistema" com o motivo. As máquinas que já foram atualizadas continuam funcionando.
- Aquela versão só é tentada de novo com uma aprovação nova.
- Se até a volta falhar, a autocura assume e avisa ([camadas](../autocura/camadas.md)).

### Voltar de versão por conta própria

1. Em **Saúde dos nós**, escolha a máquina e peça ao Guardião a ação **voltar versão** (`rollback`). Ela volta o motor para a versão anterior guardada, sem mexer no banco ([Guardião](../autocura/guardiao.md#lista-fechada-de-ações)).
2. Voltar **com restauração do banco** é raro e nunca acontece sozinho: só serve quando a versão anterior não funciona com o banco migrado. Apaga tudo o que aconteceu entre a atualização e a volta (o pedido mostra esse intervalo), por isso exige aprovação de Dono ou Administrador, com PIN ([Guardião](../autocura/guardiao.md#rollback-com-restauração-do-banco)).
3. Com o Core fora do ar, use a [página reserva](../autocura/pagina-reserva.md) de um nó (porta `7446`; numa máquina única, a do próprio Core), que tem a ação de recuperar o Core.

### Nó que ficou muito tempo desligado

Se um nó ficou desligado por tanto tempo que a versão dele não conversa mais com o Core, o painel mostra o nó como fora da versão aceita. Atualize esse nó à mão: rode o instalador mais novo do sistema dele (`mestre-<versão>-x64.msi` ou `install.sh`), como na [instalação](instalar.md#escolha-o-uso). Ele mantém o pareamento, a tarefa agendada e os dados.

## Atualizar os CLIs dos provedores

O Mestre também cuida das versões de `claude`, `codex` e `agy` usadas pelos agentes ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).

1. Por padrão, uma versão nova de um CLI vira um pedido de aprovação "Sistema", como o do Mestre. Aprove ou negue.
2. O Mestre instala, roda o [teste de conformidade](../provedores/conformidade.md) e só então libera a versão para os agentes. Se o teste falhar, a versão anterior volta e você é avisado.
3. Os agentes são reiniciados em momento seguro e continuam a conversa de onde pararam.
4. Se você atualizar um CLI por fora (à mão), o Mestre percebe, roda o teste de conformidade e avisa o resultado.
5. Para não ser perguntado, mude a política daquele provedor para **automático** no painel.

## Depois de atualizar

- Veja a coluna **Checklist** em **Saúde dos nós**: uma versão nova pode trazer verificações novas.
- Nada muda nos aparelhos que abrem o painel: a CA e os endereços continuam os mesmos.
