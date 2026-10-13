# Anexos e transcrição

> **Assunto:** imagens, documentos e áudios recebidos, arquivos enviados de volta e a transcrição local com whisper.cpp · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Deixar o usuário mandar áudio, imagem ou documento pelo Telegram (ou pelo painel) e fazer isso chegar ao Grão-Mestre e à mesa certa. O áudio é transcrito localmente, sem nada sair das máquinas do Mestre. Também permite ao Grão-Mestre devolver arquivos.

## Comportamento

### Recebimento de anexos

- **Tamanho:**
  - O limite é configurável. O padrão é **20 MB**, que é também o teto para o Telegram, porque a Bot API na nuvem só baixa arquivos de até 20 MB.
  - O motor confere o `file_size` **antes** de chamar `getFile`. Acima do limite, responde com o limite e não baixa nada.
- **Download:** `getFile` e depois o link de arquivo, que vale pelo menos 1 hora. De uma foto, o motor baixa o maior tamanho disponível.
- **Onde fica:** `.mestre/anexos/<AAAA>/<MM>/<id>-<nome>` no clone principal do workspace, na Cidadela.
  - O nome é saneado: sem separadores de caminho nem caracteres de controle, com até 100 caracteres. Uma foto sem nome vira `foto-<id>.jpg`.
  - A pasta fica fora do git ([sessão zero do workspace](../sessao-zero/workspace.md)).
- **Registro no banco:**
  - ID e workspace;
  - autor e data;
  - origem: canal, `chat_id` e `message_id`, ou sessão do painel;
  - nome original e tipo MIME (detectado pelo conteúdo e comparado com o declarado);
  - tamanho e SHA-256;
  - legenda e caminho;
  - transcrição, se houver;
  - mesas para onde o anexo foi copiado.
- **Painel:** anexos enviados pela [conversa do painel](../painel/conversa-e-aprovacoes.md) seguem as mesmas regras, sem a parte do Telegram.
- **Entrega ao Grão-Mestre:** depois da linha de origem ([roteamento](roteamento.md#entrega-ao-grão-mestre)), vêm a legenda (como texto) e uma linha por arquivo:
  `[anexo] .mestre/anexos/2026/10/a1b2-relatorio.pdf · application/pdf · 1,2 MB`
- **Repasse à mesa:** o Grão-Mestre repassa o pedido ao Mestre da mesa com o texto original e os caminhos dos anexos ([Grão-Mestre](../orquestracao/grao-mestre.md)).
  - Quando o motor encontra um caminho em `.mestre/anexos/` numa mensagem para um personagem de mesa, ele copia o arquivo para os worktrees daquela mesa, inclusive nas Torres, pela conexão de cada Torre ([protocolo](../conectividade/protocolo.md#transferência-em-partes)).
  - Onde a cópia fica está em [sessão zero da mesa](../sessao-zero/mesa.md).
- **Nunca executar:** anexos são conteúdo trazido pelo usuário. Os agentes leem e usam, mas nunca executam. É regra fixa do Mestre: o `mestre hook` nega executar ou dar permissão de execução a arquivos de `.mestre/anexos/` ([permissões](../classes/permissoes.md)).
- **Limpeza:** não há limpeza automática; os anexos ficam até alguém apagá-los.

### Envio de arquivos (`mestre reply --attach`)

- O arquivo precisa estar dentro do clone principal do workspace ou de um worktree de mesa. Qualquer outro caminho é recusado, para evitar vazar arquivos da máquina.
- **Como sai:**
  - Imagens JPG, PNG ou WebP de até 10 MB saem com `sendPhoto`.
  - O resto sai com `sendDocument`, até **50 MB** (limite da Bot API).
  - Acima disso, a CLI recebe erro com o limite.
- A legenda tem até 1.024 caracteres. Texto maior vai como mensagem separada.
- O envio fica registrado no banco como anexo de saída.
- A sintaxe está em [CLI de comunicação](../cli/comunicacao.md).

### Transcrição local

- **Motor de transcrição:** whisper.cpp, `whisper-server`, com o modelo `large-v3-turbo` quantizado ([ADR 0016](../decisoes/0016-whisper-cpp-local.md)).
  - A quantização é q5_0 ou q8_0, escolhida por benchmark na máquina durante a instalação.
  - O modelo é baixado na instalação de toda máquina que pode ser Cidadela ou quando uma máquina passa a transcrever ([Whisper na instalação](../plataforma/instalacao-e-atualizacao.md#whisper)).
- **Processo auxiliar:**
  - O motor da máquina que transcreve sobe o `whisper-server` e o supervisiona.
  - Opções: `--host 127.0.0.1 --port 7447` (porta fixa, só local; [componentes](../arquitetura/componentes.md)), `-t <núcleos físicos>`, `-l auto` e um `--prompt` curto em português. O idioma pode ser fixado em `pt` na configuração.
  - O modelo fica carregado (servidor quente).
  - Se o processo cair, o motor o reinicia com backoff de 1 s a 60 s. Com 5 quedas em 10 minutos, o motor gera um alerta e o item fica com erro no [checklist](../plataforma/checklist.md).
- **Qual máquina transcreve:**
  - O `whisper-server` roda em qualquer máquina da Guilda, Linux ou Windows, cuja [ficha da máquina](../guilda/ficha-da-maquina.md) diz que transcreve; havendo mais de uma, a com GPU é preferida.
  - **Padrão: a própria Cidadela, por CPU**, no próprio motor (serviço `mestre`, [componentes](../arquitetura/componentes.md)).
  - Se a máquina preferida estiver offline, a próxima capaz assume.
  - O motor da Cidadela manda o áudio já convertido ao motor da máquina escolhida pela conexão WSS (`transcribe.request`) e recebe o texto de volta (`transcribe.result`) ([protocolo](../conectividade/protocolo.md)); o motor dela chama o `whisper-server` local. Quando é a própria Cidadela, a chamada é direta, sem WSS.
  - Numa troca de Cidadela, o padrão passa a ser a nova Cidadela.
- **Conversão:**
  - A voz do Telegram vem em OGG/Opus, e o `whisper-server` aceita WAV.
  - O motor **da Cidadela** converte com `ffmpeg` (dependência do pacote no Ubuntu e no instalador do Windows, presente em toda máquina que pode ser Cidadela) para WAV 16 kHz mono antes de enviar à máquina que transcreve, seja ela qual for. Assim as outras Torres não precisam do `ffmpeg`, nem uma máquina que passe a transcrever. O servidor não roda com `--convert`.
- **Fila e tempo:**
  - Uma transcrição por vez por máquina, em ordem de chegada.
  - O tempo limite é o maior entre 60 s e 2 vezes a duração do áudio (padrão).
- **Privacidade:**
  - O servidor só escuta em `127.0.0.1`.
  - O áudio e o texto viajam só pela conexão WSS com mTLS entre a Cidadela e a máquina que transcreve.
  - Nada sai das máquinas.

```mermaid
sequenceDiagram
  participant T as Telegram
  participant M as Motor da Cidadela
  participant N as Motor da máquina de transcrição
  participant W as whisper-server (127.0.0.1:7447)
  participant G as Grão-Mestre
  T->>M: voice (OGG/Opus)
  M->>M: filtro, roteamento, download, anexo
  M->>M: ffmpeg → WAV 16 kHz mono
  M->>N: transcribe.request (WAV, pela WSS)
  N->>W: POST /inference
  W-->>N: texto
  N-->>M: transcribe.result
  M->>G: [áudio transcrito] texto + [anexo] arquivo original
```

- **Entrega ao Grão-Mestre:** `[áudio transcrito · 0:42 · idioma pt] <texto>`, seguido da linha `[anexo]` do arquivo original.
  - Antes de agir sobre uma transcrição que vire decreto ou aprovação, vale a confirmação descrita em [decretos](decretos.md#criação).
- **Falha** (servidor fora, conversão com erro, tempo esgotado, texto vazio ou só ruído):
  - o Grão-Mestre recebe `[áudio sem transcrição: <motivo>]` e o anexo;
  - pelas instruções dele, avisa o usuário e pede o pedido em texto.

## Regras e erros

- Um anexo que falha no download é tentado 3 vezes. Depois disso, o bot avisa o usuário e o Grão-Mestre não recebe nada.
- O tipo MIME detectado prevalece sobre o declarado. A divergência fica registrada.
- Sem modelo baixado ou sem máquina capaz, todo áudio cai no caminho de falha e o checklist acusa.

## Critérios de aceite

- **CA-ANEXO-01** · *rápida* — **Dado** um PDF de 5 MB com legenda, **quando** ele chega, **então** é salvo em `.mestre/anexos/`, registrado com SHA-256 e entregue ao Grão-Mestre com caminho, tipo e legenda.
- **CA-ANEXO-02** · *rápida* — **Dado** um documento com `file_size` de 30 MB, **quando** ele chega, **então** `getFile` não é chamado e o usuário recebe o limite.
- **CA-ANEXO-03** · *rápida* — **Dado** uma voz e um whisper falso, **quando** ela chega, **então** o Grão-Mestre recebe o texto marcado como transcrito e o anexo original.
- **CA-ANEXO-04** · *rápida* — **Dado** o whisper falso que não responde, **quando** o relógio falso passa do tempo limite, **então** o Grão-Mestre recebe o áudio com o motivo da falha.
- **CA-ANEXO-05** · *rápida* — **Dado** uma Torre com GPU e transcrição e a Cidadela com transcrição, **quando** a Torre está offline, **então** o áudio é transcrito pela própria Cidadela.
- **CA-ANEXO-06** · *rápida* — **Dado** `mestre reply --attach /etc/passwd`, **quando** o motor processa, **então** recusa por caminho fora do workspace.
- **CA-ANEXO-07** · *rápida* — **Dado** uma mensagem do Grão-Mestre ao Mestre da mesa citando um anexo, **quando** ela é entregue, **então** o arquivo já existe nos worktrees da mesa.
- **CA-ANEXO-08** · *pesada* — **Dado** o sistema instalado, **quando** mando um áudio e depois um PDF pelo Telegram (dublê), **então** o áudio é transcrito localmente pelo `whisper-server` real e respondido no Telegram, e o PDF chega à mesa certa e é usado na tarefa (MVP-10).

## Depende de

- [Bot](bot.md)
- [Roteamento](roteamento.md)
- [Protocolo](../conectividade/protocolo.md)
- [Componentes](../arquitetura/componentes.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)

## Verificar na implementação

- **Verificar na implementação:** o `--prompt` curto em português melhora a detecção em áudios curtos sem atrapalhar outros idiomas. **Padrão:** usar o `--prompt` com `-l auto`. **Se falhar:** o servidor sobe sem `--prompt`; quem fala só português fixa o idioma em `pt` na configuração. **Onde:** à mão na implementação, com áudios curtos em pt e en no modelo escolhido; as amostras ficam para repetir a cada troca de modelo.

## Fontes

- Telegram Bot API (`getFile` até 20 MB, envio até 50 MB, foto até 10 MB, servidor local até 2.000 MB): https://core.telegram.org/bots/api
- whisper.cpp server (opções, `/inference`, `--convert` exige ffmpeg): https://github.com/ggml-org/whisper.cpp/tree/master/examples/server
- whisper.cpp: https://github.com/ggml-org/whisper.cpp
