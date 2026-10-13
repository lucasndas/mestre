# ADR 0022: Guilda com uma Cidadela e Torres

> **Assunto:** por que o Mestre passa de "Core + nós" fixos a uma Guilda de instalações iguais, com uma única Cidadela no comando por vez, Torres com réplica do banco e troca de Cidadela aprovada pelo Dono · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-13. Revê o [ADR 0002](0002-wss-iniciado-pela-torre.md) (a conexão parte da Torre até a Cidadela atual), o [ADR 0003](0003-autocura-com-guardiao-sem-ssh.md) (a autocura pode terminar numa troca de Cidadela), o [ADR 0016](0016-whisper-cpp-local.md) (a transcrição roda na Cidadela, que pode ser qualquer máquina), o [ADR 0019](0019-autoatualizacao-por-releases.md) (quem distribui é a Cidadela atual; Torres primeiro, Cidadela por último) e o [ADR 0020](0020-linux-e-windows-em-qualquer-papel.md) (um serviço `mestre` por máquina, sem papel na instalação; os quatro usos viram composições da Guilda). **Completada no mesmo dia** com as decisões do Dono sobre a entrada na Guilda, o cofre em toda máquina, a perda máxima, a promoção sempre manual e a trava no GitHub, registradas abaixo e nos documentos donos.

## Contexto

- O desenho anterior tinha um **Core** fixo (o servidor) e **nós** que só rodavam agentes, com instaladores e serviços diferentes por papel (`mestre-core`, `mestre-node` e o "nó local do Core").
- Se a máquina do Core morria de vez (disco, hardware), tudo parava: o estado, o Grão-Mestre, o Telegram e o GitHub viviam só nela. A autocura consertava o motor, não a perda da máquina.
- O Dono quer instalar a mesma coisa em qualquer máquina, sem escolher papel, e que a máquina que sobrou possa assumir o comando quando a outra não volta.
- Agentes e personagens já rodavam em qualquer máquina; o que mudava de máquina para máquina era só o que ela tem (sistema, tela, provedores logados, GPU).

## Opções consideradas

1. **Core + nós fixos** (o desenho anterior).
   - Prós: um só lugar com o estado; nenhuma chance de dois comandos ao mesmo tempo; mais simples de testar.
   - Contras: a perda da máquina do Core para tudo até restaurar um backup à mão; dois instaladores e dois serviços do motor; o Telegram, o painel e a transcrição só eram exigidos no sistema do Core.
2. **Guilda com uma Cidadela por vez** (instalação única; a Cidadela tem o comando e o banco principal; as Torres têm réplica e vigiam; troca aprovada pelo Dono).
   - Prós: a mesma instalação em toda máquina; a perda da Cidadela vira uma troca com perda de segundos; continua havendo um só lugar que decide e age para fora.
   - Contras: replicação do banco e do cofre; época e fencing para nunca haver duas Cidadelas; tudo o que a Cidadela faz precisa rodar nos dois sistemas; testes de troca.
3. **Multi-mestre** (todas as máquinas escrevem e agem para fora, com consenso, ex.: Raft entre as máquinas).
   - Prós: troca automática, sem pessoa no meio.
   - Contras: consenso exige três máquinas para tolerar uma falha, e o caso comum tem duas; ações externas (GitHub, Telegram) não se desfazem se dois lados agirem; muito mais código e testes para um projeto pequeno.

## Decisão

- **Opção 2.** Uma só instalação do Mestre por máquina (ou várias [instâncias](../arquitetura/componentes.md#instâncias), cada uma uma máquina da Guilda), mesmo binário e instalador em Linux e Windows. Ao instalar, ela procura a Guilda na rede local: se acha, pede para entrar (código de 6 dígitos nas duas máquinas e aprovação do Dono com PIN, que expira em 1 h); se não acha, cria a Guilda e vira a Cidadela ([Guilda](../guilda/guilda.md)).
- **Cidadela:** a única máquina no comando por vez, com o Grão-Mestre, o bot do Telegram, o GitHub, o painel, o banco principal e o cofre ativo. **Torres:** as demais, que rodam personagens, mantêm réplica do banco e vigiam a Cidadela. Grão-Mestre e Mestre da mesa são nomes de agentes, nunca de máquinas ([Cidadela e Torres](../guilda/cidadela-e-torres.md)).
- **Uma Cidadela por vez (fencing):** uma época crescente, com certidão assinada pela CA da Guilda, vai em toda mensagem; a testemunha é uma **trava no GitHub** (a ref `refs/mestre/epoca-N` num repositório privado da Guilda, que só uma máquina consegue criar), conferida antes de toda ação externa; a Cidadela sem época confirmada fica suspensa; o `409 Conflict` do Telegram é só sinal para conferir a trava ([trava no GitHub](../guilda/trava-no-github.md)).
- **Troca de Cidadela:** a Torre condutora tenta a autocura; se a Cidadela não volta em 15 min, avisa o Dono (de novo a cada 1 h), e a promoção é **sempre manual**, pelo Dono com PIN. A antiga, ao voltar, vira Torre sem disputar. Enquanto a Cidadela está fora, as Torres seguem e guardam numa fila de saída o que depende dela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)).
- **Ficha da máquina** no lugar das capacidades do nó; classes, bases e fichas declaram requisitos, e a Cidadela escolhe uma máquina que atenda ou avisa o Dono ([ficha da máquina](../guilda/ficha-da-maquina.md)).
- **Tudo replicado para todas as máquinas**, de forma contínua, com perda máxima de 5 s: banco (só de leitura nas Torres), arquivos, espelhos e o cofre cifrado, que só a Cidadela abre; a CA da Guilda vive dentro do cofre. Toda máquina pode assumir, salvo a **máquina excluída** pelo Dono, que fica sem segredos ([replicação](../guilda/replicacao.md)).
- **Serviços:** `mestre` (motor), `mestre-guardiao` e `mestre-update` em toda máquina; contas `mestre`/`mestre-agente` no Linux e `NT SERVICE\Mestre`/`mestre-agente` no Windows ([componentes](../arquitetura/componentes.md)).

## Consequências

- **Positivas:**
  - Um instalador, um serviço do motor e um caminho de código para todas as máquinas; sem "nó local".
  - A perda da máquina da Cidadela não para o Mestre: o Dono aprova e uma Torre assume com o estado quase em dia.
  - O comportamento continua determinístico: há sempre um só lugar que decide e age para fora.
- **Negativas e riscos:**
  - **Tudo roda nos dois sistemas:** painel, Telegram, Whisper e ffmpeg, banco, GitHub e cofre precisam funcionar e ser testados também no Windows ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#tudo-roda-nos-dois-sistemas)).
  - **Perda de dados na troca:** o que a Cidadela gravou e não replicou se perde (alvo: 5 s), com registro na auditoria.
  - **Segredos em toda máquina não excluída:** que só a Cidadela abra o cofre é regra do programa, não da criptografia; uma máquina roubada tem o mesmo risco da Cidadela e exige trocar os segredos. Quem não aceita esse risco numa máquina a exclui.
  - **Duas Cidadelas** só seriam possíveis com falha da trava; a trava, a certidão assinada e o `409` como sinal são testados na rápida e na completa, e a troca inteira na pesada (MVP-15, [testes](../qualidade/testes.md)).
  - **Depende do GitHub para trocar:** sem GitHub configurado, ou sem alcançá-lo, não há promoção, e a Cidadela que não o alcança não age no GitHub nem no Telegram.
  - **Depende do Dono:** sem aprovação, a Guilda fica sem Cidadela, com o Telegram em modo reduzido e a página reserva.
  - Um processo do motor guarda o cofre e também inicia os agentes (com outra conta): a separação entre cofre e agentes continua sendo de conta, não de serviço.
