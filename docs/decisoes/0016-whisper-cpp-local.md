# ADR 0016: Transcrição local com whisper.cpp

> **Assunto:** por que a transcrição de áudio usa o `whisper-server` do whisper.cpp como processo auxiliar · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- **O requisito:** áudios e mensagens de voz do Telegram são transcritos **localmente** com o modelo Whisper large-v3-turbo. Nada sai da máquina, e o idioma padrão é português (MVP-10).
- **A spec:** sugeria faster-whisper (CTranslate2).
- **A experiência do usuário:** ele já roda o large-v3-turbo **por CPU** e funciona.
- **O motor:** é Go sem CGO ([ADR 0001](0001-stack-go.md)).
- **As máquinas do usuário:** não há nó com GPU; a transcrição roda no Core.
- **O modelo turbo:** reduz o decodificador de 32 para 4 camadas (809M contra 1,55B de parâmetros).

## Opções consideradas

1. **faster-whisper (Python + CTranslate2, int8).**
   - Prós:
     - Cerca de 2x mais rápido em lote por CPU em arquivos longos.
     - Sugerido na spec.
   - Contras:
     - Exige distribuir Python e CTranslate2 (empacotado, mais de 200 MB).
     - É um segundo runtime para atualizar e corrigir.
2. **whisper.cpp `whisper-server` como processo auxiliar.**
   - Prós:
     - Um binário nativo por plataforma, sem Python.
     - API HTTP local parecida com a da OpenAI.
     - Suporta large-v3-turbo quantizado (q5_0, q8_0), SIMD (AVX2/NEON), VAD (`--vad`) e aceleração OpenVINO opcional.
   - Contras:
     - Mais um processo para supervisionar e distribuir.
     - Em lote, é mais lento que o faster-whisper.
3. **whisper.cpp dentro do processo Go (bindings).**
   - Prós: sem processo extra.
   - Contras:
     - Exige CGO, o que quebra a compilação cruzada simples e o binário único ([ADR 0001](0001-stack-go.md)).
     - Uma falha no código C derruba o motor.
4. **API de transcrição na nuvem.**
   - Prós: zero infraestrutura.
   - Contras: o áudio sai da máquina, o que viola o requisito.

## Decisão

- **Ferramenta:** **whisper.cpp `whisper-server`** como processo auxiliar **supervisionado pelo motor**, com API HTTP local.
- **Modelo:**
  - `large-v3-turbo` quantizado, **q5_0 ou q8_0**, escolhido por benchmark na máquina durante a instalação.
  - O processo fica com o modelo carregado.
- **Onde roda:**
  - No nó com a capacidade de transcrição (`transcricao: true`); se houver mais de um, o que anuncia `gpu: true`.
  - **Padrão: o Core, por CPU.**
- **Onde estão os detalhes:**
  - Comportamento, idioma e falhas: [anexos e transcrição](../telegram/anexos-e-transcricao.md).
  - Download do modelo e do binário: [instalação e atualização](../plataforma/instalacao-e-atualizacao.md).

## Consequências

- **Positivas:**
  - Sem Python no sistema.
  - O motor continua sem CGO.
  - Uma falha na transcrição não derruba o motor.
  - A instalação é a mesma em Linux e Windows.
- **Negativas:**
  - **Distribuição:** a instalação baixa o binário do whisper.cpp e o modelo (estimativa: 0,5 a 0,9 GB) e roda o benchmark para escolher a quantização.
  - **Supervisão:** o motor supervisiona mais um processo, com checagem de saúde, reinício e porta local.
  - **Desempenho:** áudios longos transcrevem mais devagar que com faster-whisper em lote. Para mensagens de voz curtas isso é irrelevante: na CPU de um desktop moderno com 8 núcleos, o turbo q5 deve ficar bem abaixo do tempo real (estimativa; o benchmark da instalação mede na máquina).
  - **Checklist:** o checklist precisa conferir o modelo baixado e uma transcrição de teste dentro do tempo esperado ([checklist](../plataforma/checklist.md)).

## Fontes

- whisper.cpp: https://github.com/ggml-org/whisper.cpp
- faster-whisper e números de CPU: https://github.com/SYSTRAN/faster-whisper
- large-v3-turbo: https://whispernotes.app/blog/introducing-whisper-large-v3-turbo
