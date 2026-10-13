# ADR 0013: Sincronização de worktrees por git bundle pela conexão WSS

> **Assunto:** por que os commits entre Torre e Cidadela viajam como git bundle dentro da conexão da Torre · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda.

## Contexto

- Uma mesa pode ter agentes em máquinas diferentes. Cada máquina tem o próprio worktree da branch da mesa ([ADR 0004](0004-mesa-igual-worktree.md)).
- Os worktrees se sincronizam por commits através do **espelho da Cidadela**, nunca direto com o GitHub. Isso acontece automaticamente após cada `mestre done` e antes de cada etapa que rode em outra máquina.
- **Restrições:**
  - Não pode haver SSH.
  - A Torre não abre porta.
  - Só o motor envia para o GitHub.
- Já existe uma conexão autenticada e cifrada entre cada Torre e a Cidadela ([ADR 0002](0002-wss-iniciado-pelo-no.md)).

## Opções consideradas

1. **Git bundle enviado pela conexão WSS**, em partes, com hash.
   - Prós:
     - Reusa a conexão, a autenticação e o TLS que já existem.
     - Nenhuma porta ou protocolo novo.
     - O bundle é um formato nativo do git, verificável com `git bundle verify`.
   - Contras:
     - Transferência em partes, retomada e verificação são nossas.
     - É preciso saber que commits o outro lado já tem, para enviar só a diferença.
2. **Endpoint git na Cidadela (smart HTTP), autenticado com a credencial da Torre.**
   - Prós:
     - Protocolo git padrão, com negociação de objetos pronta (`git fetch` e `git push`).
     - Menos código próprio.
   - Contras:
     - Uma segunda superfície de rede e de autenticação na Cidadela.
     - Um segundo caminho que pode falhar diferente da conexão principal.
     - A Torre precisa guardar a credencial num formato que o git entenda.
3. **SSH até a Cidadela.**
   - Prós: padrão do git.
   - Contras: a spec proíbe; exige chaves e servidor SSH.
4. **Cada máquina envia direto ao GitHub.**
   - Prós: nada para construir.
   - Contras:
     - Viola a regra de que só o motor da Cidadela envia.
     - Exige credencial do GitHub em toda máquina.
     - Perde o espelho como fonte única.
5. **Copiar arquivos da pasta de trabalho** (estilo rsync).
   - Prós: sincroniza até mudanças sem commit.
   - Contras:
     - Perde o histórico.
     - Conflitos ficam silenciosos.
     - Não combina com commits por agente.

## Decisão

- **Git bundle pela conexão WSS da Torre**, em mensagens de transferência divididas em partes, cada uma com hash.
- **Bundle incremental:** o bundle leva só os commits que o outro lado não tem, usando como base as referências conhecidas. Quando a base não existe do outro lado, vai um bundle completo da branch.
- **Onde estão os detalhes:**
  - Quando sincroniza e o que acontece em conflito: [mesas](../orquestracao/mesas.md).
  - Formato das mensagens de transferência: [protocolo](../conectividade/protocolo.md).

## Consequências

- **Positivas:**
  - Uma só conexão, uma só credencial e uma só política de segurança por máquina.
  - Funciona fora da rede local do mesmo jeito.
  - Nada além do motor fala com o GitHub.
- **Negativas:**
  - **Transferência própria:** o motor precisa implementar divisão em partes, retomada depois de reconexão, verificação de hash e limpeza de transferências abandonadas.
  - **Concorrência com terminais:** bundles grandes dividem a conexão com o fluxo dos terminais. O protocolo precisa de controle de fluxo para não atrasar heartbeats ([ADR 0002](0002-wss-iniciado-pelo-no.md)).
  - **Só commits viajam:** o que não foi commitado não sincroniza. Os agentes precisam commitar antes de `mestre done`, e as skills ensinam isso ([skills geradas](../cli/skills-geradas.md)).
  - **Conflitos:** duas máquinas com commits divergentes na mesma branch geram conflito ao aplicar o bundle. O Mestre da mesa evita que duas máquinas editem os mesmos arquivos ao mesmo tempo, e o motor nunca resolve conflito sozinho.
  - **Testes:** a troca de bundles precisa de repositórios git reais em pastas temporárias. Por isso, só a lógica de partes e hash cabe na suíte rápida ([testes](../qualidade/testes.md)).
