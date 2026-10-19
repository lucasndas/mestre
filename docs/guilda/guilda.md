# Guilda

> **Assunto:** formação da Guilda (instalar → descobrir → entrar ou criar), entrada de uma máquina com código e aprovação do Dono, credenciais da máquina, máquina excluída, saída e remoção · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Toda máquina recebe a mesma instalação do Mestre, em Linux ou Windows, e se junta às outras sem configuração: a primeira cria a **Guilda** e vira a [Cidadela](cidadela-e-torres.md); as seguintes a encontram na rede local e pedem para entrar, com um único passo do Dono (aprovar e conferir um código). A partir daí, cada máquina usa só credenciais próprias, que o Dono pode revogar a qualquer momento, e recebe tudo replicado, inclusive o cofre cifrado, salvo se o Dono a excluir. Uma máquina pode ter várias [instâncias](../arquitetura/componentes.md#instâncias), cada uma uma máquina da Guilda. O passo a passo para quem usa está em [instalar](../operacao/instalar.md) e [entrar na Guilda](../operacao/entrar-na-guilda.md).

## Comportamento

### Formação

1. **Instalar:** o instalador único (o mesmo em Linux e Windows) registra os serviços `mestre`, `mestre-guardiao` e `mestre-update` e as contas ([componentes](../arquitetura/componentes.md#serviços-e-contas-padrão); passos por sistema em [instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). Não pergunta papel.
2. **Descobrir:** procura uma Guilda na rede local por broadcast UDP e mDNS, com `guild_id` vazio, por até 3 s, três vezes ([descoberta](../conectividade/descoberta.md)). Com `mestre install --join <endereço>`, usa só esse endereço (máquina fora da rede local).
3. **Entrar ou criar:**
   - achou **uma** Guilda: pede para entrar (abaixo);
   - achou **mais de uma**: o instalador lista as Guildas (nome da Cidadela e quantas máquinas) e pergunta em qual entrar;
   - achou uma Guilda **sem Cidadela no ar** (só Torres responderam): não cria outra; avisa "a Guilda existe, mas a Cidadela está fora; tente de novo depois" e sai com erro;
   - **não achou nenhuma**: cria a Guilda nesta máquina, que vira a Cidadela da época 1.
- **Criar a Guilda:** gera o `guild_id` (ULID), o cofre e, dentro dele, a [CA da Guilda](replicacao.md#ca-da-guilda), emite os certificados desta máquina, grava a [certidão](replicacao.md#certidão-de-época) da época 1, cria o banco principal e grava o [fuso da Guilda](../painel/administracao.md#fuso-da-guilda) (o do sistema desta máquina). Mostra o endereço do painel e o código de configuração do Dono ([login](../painel/login.md)).
- `mestre install --new-guild` cria uma Guilda nova mesmo que outra responda (duas Guildas independentes na mesma rede).

### Entrada na Guilda

A máquina nova ainda não conhece a CA da Guilda, e a Cidadela ainda não conhece a máquina. Um **código curto de comparação**, calculado dos dois lados, liga o pedido à conexão TLS que o carregou, sem segredo trocado antes.

```mermaid
sequenceDiagram
  participant N as Máquina nova
  participant C as Cidadela (/join)
  participant D as Dono (Telegram ou painel)
  N->>C: TLS sem validar a cadeia; N anota fp = SHA-256 do certificado visto
  N->>C: join.request: nome, ficha da máquina, CSRs, h = SHA-256(nonce_n)
  C-->>N: join_id, nonce_c
  N->>C: nonce_n (C confere h)
  Note over N,C: os dois calculam código = 6 dígitos de SHA-256("mestre-join" ‖ fp ‖ SHA-256(CSRs) ‖ nonce_n ‖ nonce_c)
  N->>N: mostra "Código de entrada: 123 456"
  C->>D: "Aceitar <máquina> (<sistema>) na Guilda? Código 123 456" — Aceitar / Recusar, com PIN
  D-->>C: Aceitar + PIN
  C-->>N: certificados, CA, guild_id, machine_id, certidão de época, configuração da Guilda
  N->>C: conecta em /ws/tower com mTLS (protocolo normal)
```

- **Rotas** (HTTPS na porta do motor, fora do mTLS, `internal/guild`): `POST /join` (nome e sistema, nome do computador, IPs, porta do motor, ficha, os dois CSRs e `h`; o sistema é `linux` ou `windows` e a instância, se vem, segue a [regra do nome](../arquitetura/componentes.md#instâncias), porque a rota é aberta e esses campos vão para a pergunta ao Dono e para o registro que toda Torre replica; a ficha, se vem, é um objeto de até 16 KiB, e cada CSR tem até 1 KiB e chave ECDSA P-256), `POST /join/<join_id>/reveal` (o `nonce_n`) e `GET /join/<join_id>?wait=<s>` (espera longa, até 60 s: `pending`, `approved` com o pacote, `refused` ou `expired`). Respostas: `429` acima dos limites, `503` na Cidadela suspensa, `410` no pedido vencido. O `fp` é o do certificado de motor que a Cidadela apresenta; a máquina nova confere que toda conexão da entrada mostra o mesmo.
- **Por que o código protege:** um intermediário mostra à máquina nova outro certificado, então `fp` muda e os dois códigos ficam diferentes. Como a máquina nova se compromete com `nonce_n` antes de ver `nonce_c`, o intermediário não consegue escolher valores que façam os códigos baterem (uma chance em um milhão por pedido). Cada pedido revelado vira uma pergunta ao Dono; um que não revela o `nonce_n` em **90 s** expira, não ocupa vaga e conta como recusa no alerta abaixo, então tentar códigos não passa despercebido.
- **O Dono compara** o código da tela do instalador com o da mensagem; só aceita se forem iguais. O pedido (tipo `entrada_na_guilda`, só o Dono, com PIN; [pedidos da Guilda](../aprovacoes/fluxo.md#pedidos-da-guilda)) aparece no Telegram ([bot](../telegram/bot.md)) e no painel ([Guilda e saúde das máquinas](../painel/administracao.md#guilda-e-saúde-das-máquinas)). Antes de o Dono existir, o pedido aparece no painel depois do assistente do Dono, dentro do mesmo prazo. A pergunta só sai quando a máquina revela o `nonce_n`, porque é aí que o código existe. A Guilda só pergunta (`Approver`) e recebe a resposta (`Citadel.Decide`); o botão e o PIN são do bot e do painel.
- **Na aceitação** o Dono pode já marcar a máquina como [excluída](#máquina-excluída) (padrão: não).
- **Espera:** o instalador acompanha o pedido em `GET /join/<join_id>` (espera longa) e mostra o andamento. Sem resposta em **1 hora** do pedido, ele expira e a pergunta sai do Telegram e do painel. Depois de aceita, o pacote (certificados públicos, CA, certidão e configuração; nenhum segredo) continua disponível por mais 1 hora, para uma entrada interrompida.
- **Depois de aceita:** a máquina conecta como Torre, manda a [ficha da máquina](ficha-da-maquina.md) no `hello` e recebe a cópia completa do banco, os arquivos e, se não é excluída, o cofre ([replicação](replicacao.md)).
- **Entrada manual:** `mestre join <endereço>` repete a entrada numa máquina já instalada que está fora da Guilda (removida, ou que criou uma Guilda por engano e ainda não tem nada); só root ou administrador local ([componentes](../arquitetura/componentes.md#mestre)).

### Credenciais

- **Chaves na máquina:** a máquina gera localmente dois pares ECDSA P-256: o do **motor** (cliente e servidor) e o do **Guardião** (cliente, cura cruzada e página reserva). As chaves privadas nunca saem da máquina: no Linux, `0600` com dono `mestre` (a do Guardião, na pasta do Guardião, dono root); no Windows, com ACL só para a conta do serviço dono e SYSTEM.
- **Certificados** emitidos pela CA da Guilda, 1 ano (padrão), com o SAN `mestre://machine/<machine_id>` no do motor e `mestre://guardian/<machine_id>` no do Guardião, mais `<host>.local` e os IPs da rede local, conferidos antes de emitir ([nomes dos certificados](../arquitetura/seguranca.md#nomes-dos-certificados)). Nenhum certificado de máquina traz `mestre.local`: só o da Cidadela em exercício, de 7 dias, que ela mesma emite, com os nomes extras que o Dono configurou para a Guilda. Tabela completa em [segurança](../arquitetura/seguranca.md#tls-e-ca-interna).
- **Renovação:** automática pela conexão já autenticada, **30 dias antes de vencer** (padrão), sempre com chave nova (`cert.renew`, com o CSR e o certificado em PEM); quando o nome ou os IPs da máquina mudam, na hora. No `mestre run`, cada Torre confere a cada minuto e a cada `guild.config` (as mudanças de nomes vêm do registro na réplica) e a Cidadela renova o próprio com a CA dela; a chave nova fica num arquivo pendente até o certificado ser guardado, para uma parada no meio não deixar chave sem certificado. Quem emite é a Cidadela atual, com os nomes que o registro da máquina guarda (a máquina manda a ficha nova antes de pedir) e a mesma identidade; o certificado antigo vale até vencer. Certificado vencido (máquina desligada por mais de um ano) exige entrada nova.
- **Registro da máquina** no banco principal (`machines`): `machine_id` e nome, prioridade (usada na escolha da [Torre condutora](cidadela-e-torres.md#recuperação-e-aviso-ao-dono), editável no painel), se é excluída, a última ficha, os nomes dos certificados (nome do computador e IPs), a porta do motor (de `guild.config`), estado (`active`, `removed` pelo Dono, `left` ao desinstalar) e datas de entrada e de remoção. Em `machine_credentials`, um registro por certificado emitido (`motor`, `guardiao` ou `cidadela`, o de 7 dias da Cidadela): número de série, o certificado público (para a renovação saber o que substitui), validade e data de revogação. A máquina guarda em `machine_identity` (banco local) o `machine_id`, o `guild_id`, a CA, os certificados e a maior certidão vista; o papel não é gravado, é a máquina que a certidão nomeia.
- **Confiança:** cada máquina confia só na CA da Guilda para falar com as outras, ignora o repositório de certificados do sistema e confere o SAN e a lista de máquinas da Guilda ([checagem de identidade](../conectividade/descoberta.md#checagem-de-identidade)). O navegador confia na CA uma vez ([primeiro acesso](../operacao/primeiro-acesso.md)).
- **Recusa:** o handshake recusa os números de série revogados e **todo** certificado de máquina removida ou que saiu, até um que o registro não conhece (uma renovação perdida numa troca). É uma lista de recusas, não de permissões: uma Torre com a réplica atrasada aceita o certificado novo da Cidadela de que replica. O motor só abre a porta depois de ler essa lista do banco no disco, e não sobe se não consegue; a Torre soma a de `guild.config` (números de série revogados e máquinas removidas ou que saíram), que a Cidadela manda a cada conexão e a cada remoção, e derruba na hora (`4002`) as conexões que passam a ser recusadas.

### Máquina excluída

- O Dono pode **excluir** uma máquina específica, no painel ou pelo `/guilda` no Telegram, com PIN ([administração](../painel/administracao.md#guilda-e-saúde-das-máquinas)); desfazer é igual. Não pode excluir a Cidadela atual.
- A **máquina excluída** fica **sem segredos**: a Cidadela manda `vault.drop`, e ela apaga a cópia do cofre, o envelope e as tabelas do cofre da réplica, com recibo na auditoria; o que ela guardou do tempo de Cidadela (bancos descartados e cópias completas, cada um um banco inteiro com as tabelas do cofre) também some; a réplica do Guardião dela passa a levar só a lista de usuários, sem o token do bot ([réplica para os Guardiões](../plataforma/cofre.md#réplica-para-os-guardiões)). Os certificados não mudam: como os de toda Torre, já não têm `mestre.local`. A Guilda registra a exclusão e dispara o gancho `ExclusionChanged`; o `vault.drop` e o cofre de volta são da replicação.
- Fica **fora da troca de Cidadela**: não pode ser promovida nem conduzir a promoção ([Cidadela e Torres](cidadela-e-torres.md#promoção)). Continua rodando personagens e recebendo o banco e os arquivos.
- Desfeita a exclusão, a máquina volta a receber o cofre e pode de novo ser promovida.

### Saída e remoção

- **Remover pelo painel** (só o Dono, com PIN, [níveis](../telegram/usuarios-e-niveis.md)): os certificados da máquina entram na lista de revogados (números de série, guardados no banco; as Torres a recebem em `guild.config`), as conexões do motor e do Guardião caem com `4002`, toda conexão nova com eles é recusada e os personagens dela ficam interrompidos ([personagens](../orquestracao/personagens.md)). Se a máquina está online, a Cidadela manda antes `guild.leave`: ela apaga a réplica do banco e dos arquivos, a cópia do cofre, o que guardou do tempo em que foi Cidadela (bancos descartados, cópias completas e transferências; a lista em [replicação](replicacao.md#cofre-em-toda-máquina)), a configuração da Guilda (e o motor dela se encerra 2 s depois, para a resposta chegar à Cidadela), a **chave privada do motor** (`motor.key`, e a `motor.key.nova` de uma renovação em andamento) e o que o banco da máquina (`maquina.db`) guardava da Guilda (fila de saída, comandos já processados, endereços achados, regras e dados dos personagens) e fica "fora da Guilda", com os serviços parados até nova entrada: a fila de saída velha nunca chega a uma Cidadela nova. **Ficam**, por serem da máquina e não da Guilda: os perfis de conta e `contas/` (os logins dos provedores), os terminais registrados (o iniciador os encontra por ali), os worktrees e as pastas de `workspaces/` (o código vem do GitHub; no clone principal de uma ex-Cidadela, as partes só locais ficam também) e os logs; quem não os quer, apaga com o `mestre uninstall` pedindo para apagar tudo ([desinstalar](../operacao/desinstalar.md)). A Cidadela espera a resposta por 10 s e revoga a máquina de qualquer jeito; a máquina termina de sair mesmo que a conexão caia no meio.
- **`mestre uninstall` numa Torre:** pede a saída à Cidadela (`guild.leave` com `reason: uninstall`), apaga o mesmo (a réplica, os arquivos replicados, a cópia do cofre, a chave do motor, o que guardou do tempo de Cidadela e o que o `maquina.db` guardava da Guilda) e remove os serviços; com a Cidadela fora, apaga do mesmo jeito, desinstala assim mesmo e o painel mostra a máquina como "sumida", para o Dono remover.
- **A Cidadela não se desinstala com Torres na Guilda:** o instalador recusa e explica que antes é preciso passar a Cidadela para outra máquina ([troca planejada](cidadela-e-torres.md#troca-planejada)). Sozinha, a Cidadela desinstala e a Guilda acaba.
- **Máquina removida offline** (não excluída): a cópia cifrada do cofre fica no disco dela, e quem tem a máquina tem o mesmo risco da Cidadela ([segurança](../arquitetura/seguranca.md#ameaças-e-proteções)); o painel recomenda substituir os segredos do cofre ([cofre](../plataforma/cofre.md)).
- **Voltar:** entrada nova, com `machine_id` novo; o histórico da antiga fica no banco.

## Regras e erros

- **Pedido recusado, expirado ou com código que não confere:** a máquina mostra "entrada recusada ou expirada", não guarda nenhuma CA e nada é criado na Guilda.
- **Nomes que um certificado não pode ter** (nome com ponto, `mestre`, IP público, mais de 16 IPs; [nomes dos certificados](../arquitetura/seguranca.md#nomes-dos-certificados)): o pedido é recusado (`400`) com o motivo, sem guardar nada e sem perguntar ao Dono; a mesma conferência vale para os nomes que chegam pela ficha e na renovação, e o mesmo `400` vale para um sistema que não seja `linux` nem `windows`, para um nome de instância fora da [regra](../arquitetura/componentes.md#instâncias), para uma ficha que não seja um objeto ou passe de 16 KiB e para um CSR de mais de 1 KiB ou de chave que não seja ECDSA P-256; a resposta não repete o que a máquina mandou, e o registro do pedido guarda o nome limpo, o mesmo da pergunta ao Dono.
- **Ficha do pedido:** tem de ser JSON e passar na mesma conferência do `hello` ([ficha da máquina](ficha-da-maquina.md): campos obrigatórios e tipos); senão o pedido é recusado (`400`) sem guardar nada. A conferência é injetada na Cidadela (`CheckSheet`) porque o pacote da ficha importa este.
- **Limites:** o corpo do `POST /join` tem no máximo 64 KiB (acima, `400`) e o nome da máquina perde os caracteres não imprimíveis, é cortado em 64 runas e, vazio, vira o nome do computador (vazio também, `400`); até 5 pedidos pendentes ao mesmo tempo (do pedido à resposta) e 10 pedidos por IP por hora (padrão); acima disso, recusa sem perguntar ao Dono (`429`) e sem guardar o pedido. Com 20 pedidos recusados em 24 horas (os que não revelaram o `nonce_n` contam), o Dono recebe um alerta.
- **Nome repetido:** a Cidadela acrescenta um sufixo; quem identifica a máquina é o `machine_id`.
- **Entrada interrompida** depois dos certificados e antes da primeira conexão: a máquina tenta conectar com o que recebeu.
- Só a Cidadela da época atual aceita entradas; uma Cidadela suspensa, uma Torre e uma Cidadela que ainda não abriu a CA respondem `503` em `/join` (as rotas existem desde a subida do motor; rota ausente daria `404`, que o instalador leria como entrada recusada) ([Cidadela suspensa](cidadela-e-torres.md#cidadela-suspensa)).

## Critérios de aceite

- **CA-GUILDA-01** · *rápida* — **Dado** uma rede falsa sem nenhuma Guilda, **quando** a instalação descobre, **então** cria a Guilda com `guild_id`, CA, certidão da época 1 e papel Cidadela; **dado** só uma Torre respondendo, **então** não cria nada e sai com o aviso de Cidadela fora.
- **CA-GUILDA-02** · *rápida* — **Dado** uma Cidadela e uma máquina nova, **quando** a máquina pede entrada, **então** os dois lados calculam o mesmo código; aceito pelo Dono com PIN, ela recebe os certificados com os SANs certos, e sem aceitação nada é emitido.
- **CA-GUILDA-03** · *rápida* — **Dado** um intermediário com outro certificado TLS, **quando** a máquina pede entrada através dele, **então** o código da máquina e o da Cidadela são diferentes.
- **CA-GUILDA-04** · *rápida* — **Dado** um pedido com 1 h e 1 min (relógio falso), **quando** o Dono tenta aceitar, **então** o pedido está expirado e nada é criado.
- **CA-GUILDA-05** · *rápida* — **Dado** 10 pedidos de um IP numa hora, **quando** chega o 11º, **então** é recusado sem mensagem ao Dono.
- **CA-GUILDA-06** · *rápida* — **Dado** uma máquina removida pelo Dono, **quando** o motor ou o Guardião dela conectam, **então** o handshake é recusado; **dado** que ela estava online, **então** recebeu `guild.leave` e apagou réplica, arquivos, cofre e configuração.
- **CA-GUILDA-07** · *rápida* — **Dado** um certificado a 30 dias de vencer (relógio falso), **quando** a máquina está conectada, **então** pede e instala um novo sem cair; **dado** a mudança de IP, **então** recebe certificados com o IP novo.
- **CA-GUILDA-08** · *rápida* — **Dado** a Cidadela com uma Torre na Guilda, **quando** roda `mestre uninstall` nela, **então** a desinstalação é recusada com a explicação da troca planejada.
- **CA-GUILDA-09** · *completa* — **Dado** duas [instâncias](../arquitetura/componentes.md#instâncias) na mesma máquina, a primeira já Cidadela, **quando** a segunda é iniciada como processo comum, **então** acha a Guilda por broadcast, entra com a aprovação pelo Telegram falso, conecta só por mTLS e reconecta depois de reiniciar.
- **CA-GUILDA-10** · *pesada* — **Dado** a instalação no Linux e o Dono criado, **quando** instalo no Windows, **então** a instalação acha a Guilda sozinha na rede local, o pedido chega ao Telegram com o código igual ao da tela, e depois de aprovado a máquina aparece online no painel com a ficha dela (MVP-1).
- **CA-GUILDA-11** · *pesada* — **Dado** cada uma das quatro composições (só Linux, Cidadela Linux + Torre Windows, Cidadela Windows + Torre Linux, só Windows), **quando** instalo do zero, **então** a primeira máquina cria a Guilda, a segunda (se houver) entra, os serviços `mestre` e `mestre-guardiao` estão no ar em todas, o painel abre, o Dono é criado, as Torres aparecem conectadas e um personagem de teste roda em cada máquina, inclusive na Cidadela (MVP-13).

- **CA-GUILDA-12** · *rápida* — **Dado** uma Torre com o cofre, **quando** o Dono a exclui com PIN, **então** ela apaga o cofre, o envelope e as tabelas do cofre da réplica, os certificados dela seguem sem `mestre.local`, e uma promoção dela é recusada; **dado** a exclusão desfeita, **então** recebe o cofre de novo.
- **CA-GUILDA-13** · *rápida* — **Dado** um pedido de entrada com o sistema fora de `linux` e `windows` (ou vazio, com quebra de linha, de 60 KiB) ou com o nome de instância fora da [regra](../arquitetura/componentes.md#instâncias) (com espaço, quebra de linha, mais de 32 caracteres, 60 KiB), ou com a ficha em lista, número, `null` ou de 20 KiB, ou um CSR de 2 KiB ou de chave RSA, **quando** chega a `POST /join`, **então** a Cidadela responde `400` sem registrar o pedido nem perguntar ao Dono; **dado** `linux` ou `windows` com instância vazia, `principal` ou de 32 caracteres, **então** o pedido segue.
- **CA-GUILDA-14** · *rápida* — **Dado** uma máquina online removida pelo Dono cuja saída demora mais do que os 10 s que a Cidadela espera, **quando** a Cidadela desiste e fecha a conexão, **então** a máquina termina de apagar e esquece a identidade.
- **CA-GUILDA-15** · *rápida* — **Dado** uma Torre com a chave do motor e uma `motor.key.nova` de renovação, **quando** sai da Guilda (removida pelo Dono ou por `mestre uninstall`), **então** os dois arquivos somem depois de o `Wiper` terminar; **dado** que o `Wiper` falha, **então** as chaves ficam, com a identidade, para a nova tentativa.
- **CA-GUILDA-16** · *rápida* — **Dado** uma Torre com fila de saída para a Cidadela, comandos processados, endereços achados e um perfil de conta no `maquina.db`, **quando** sai da Guilda, **então** a fila, os comandos e os endereços (e as demais tabelas da Guilda) ficam vazios e o perfil de conta fica; toda tabela do `maquina.db` está numa das duas listas, para a migração nova ter de escolher.

## Depende de

- [Protocolo](../conectividade/protocolo.md) · [Descoberta](../conectividade/descoberta.md)
- [Cofre](../plataforma/cofre.md) (a CA vive dentro dele)
- [Segurança](../arquitetura/seguranca.md) · [Componentes](../arquitetura/componentes.md)
