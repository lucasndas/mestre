# Administração: regras e cadeia

> **Assunto:** implementação das telas de decretos, delegações e regras temporárias, supervisão da cadeia, teto, política de aprovação e instruções extras (rotas, níveis, PIN, auditoria e arquivos) · **Componente:** Painel · **Atualizado:** 2026-10-27

Documento de referência. As telas aparecem sob **Configurações › Regras e consumo** ([estrutura](design.md#estrutura-da-tela)); as rotas e os arquivos abaixo não mudam. Diz como as seis telas de [administração](administracao.md#telas) que mexem em regras e na cadeia foram feitas, para quem for mudá-las. O que cada tela mostra está em [administração](administracao.md); as regras de cada assunto ficam no documento dono, linkado lá.

## Implementação

Código: `internal/panel/routes_decrees.go`, `routes_delegations.go`, `routes_chain.go` e `routes_workspace_rules.go` (tipos em `types_rules.go`, gerados em `web/src/gen/panel.ts`), as portas em `rules_ports.go` e as telas em `web/src/screens/admin/` (`Decrees`, `Delegations`, `Chain`, `Ceiling`, `Policy`, `Instructions`, com `rulesKit.tsx`). O painel não repete nenhuma regra: nível, campanhas, PIN e recusas são do serviço de [decretos](../telegram/decretos.md) (`internal/decrees`), da supervisão (`chain.Supervisor`) e das regras temporárias (`tablerules`), os mesmos do Telegram. O motor entrega essas peças por `Panel.SetRules`, depois do painel, porque a orquestração sobe depois dele; antes disso as telas respondem que não estão disponíveis.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/decrees` (`?all=1`) | Observador ou acima | decretos em vigor (e, com `all`, os que acabaram) das campanhas de quem pede, com autor, ordem original, validade, os últimos usos e se o nível revoga e se pede PIN |
| `POST /api/decrees` | Aprovador ou acima | proposta de regra permanente (permissão ou comportamento) ou de exceção pontual; devolve o texto do "Entendi: …" e se pede PIN; **não é decreto** |
| `POST /api/decrees/proposals/{id}/confirm` | o autor da proposta | o clique; com o PIN no cabeçalho quando a proposta pede (sem ele, `pin_required`); só aí nasce o decreto |
| `POST /api/decrees/{n}/revoke` | por nível, como no Telegram | revoga; pede PIN só se o decreto restringia algo |
| `GET /api/delegations` | Observador ou acima | delegações, subdelegações e regras temporárias em vigor, com as decisões tomadas com base em cada uma |
| `POST /api/decrees/{n}/renew` | Senhor da Guilda | proposta de renovação da delegação (o Senhor da Guilda a confirma com PIN, como qualquer decreto) |
| `POST /api/subdelegations/{id}/revoke`, `POST /api/table-rules/{id}/revoke` | Administrador ou acima | revoga uma subdelegação ou uma regra temporária |
| `POST /api/delegations/revoke-all` | Senhor da Guilda | **Revogar todas as delegações**; só restringe, então não pede PIN ([delegação](../aprovacoes/delegacao.md#revogar-tudo)) |
| `GET /api/chain` | Observador ou acima | por mesa: a pausa (quem, quando, motivo) e as filas (abertos, o mais antigo, espera média) por nível |
| `POST /api/chain/{workspace}/tables/{mesa}/pause` e `/resume` | Aprovador ou acima | corpo `{reason}` (obrigatório); sem PIN, só restringe; retomar a pausa do Grão-Mestre é recusado com `grand_master_only` |
| `GET /api/workspaces/{ws}/ceiling` | Observador ou acima | o arquivo do teto (só leitura) e os decretos `ceiling` em vigor |
| `POST /api/workspaces/{ws}/ceiling/widen` | Administrador ou acima | abre a **proposta** de decreto `ceiling`; nunca edita o arquivo, e o teto em vigor só muda depois da confirmação com PIN |
| `GET`, `PUT /api/workspaces/{ws}/policy` | Senhor da Guilda | lê e grava `politica-de-aprovacao.yaml` |
| `POST /api/workspaces/{ws}/policy/validate` | Senhor da Guilda | **Validar**: confere o texto com a mesma regra do `PUT` (mesmo erro, com a linha) e responde `204`; não grava nem escreve auditoria |
| `GET`, `PUT /api/workspaces/{ws}/instructions` | Administrador ou acima | lê e grava `instrucoes/grao-mestre.md` e `instrucoes/mestre-da-mesa.md` (até 4 KiB cada) |

- **Pausa do Grão-Mestre:** o motor deixa o Grão-Mestre ou um Aprovador retomar a pausa dele ([supervisão](../orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia)); o painel é mais estrito, como pede a [administração](administracao.md#telas): quem usa a tela nunca retoma uma pausa do Grão-Mestre. A recusa é do próprio supervisor, sob o mesmo lock que lê a pausa (`PauseRequest.RefuseCharacterPause`, `chain.ErrCharacterPause`), então uma pausa que muda de dono no meio não escapa.
- **Arquivos:** ficam em `.mestre/regras/` do clone principal, lidos e gravados por `safefile` (um link ou arquivo que não é comum é recusado com `file_refused`). A política é validada inteira por `chain.ParsePolicyFile` antes de gravar; um arquivo inválido não grava nada e o erro traz a `line` (a do YAML ou, numa regra da política, a da chave). Quem salva manda a `version` (hash) que abriu: arquivo que mudou depois dá `file_changed`. A gravação também leva o `Guard` do `safefile` (tamanho e hora do arquivo lido), conferido logo antes do rename: uma edição à mão entre a leitura e a gravação nunca é sobrescrita e dá o mesmo `file_changed`.
- **Auditoria:** `panel.decree.{proposed,created,revoked,renewal_proposed}`, `panel.subdelegation.revoked`, `panel.table_rule.revoked`, `panel.delegations.revoked_all`, `panel.chain.{paused,resumed}`, `panel.ceiling.widen_proposed`, `panel.policy.{saved,refused}` e `panel.instructions.saved`, todas com `user_id`, `workspace` e `before`/`after` (na política, as linhas que saíram e as que entraram). Nenhuma leva PIN.
- Uma campanha que não é de quem pede responde `not_found`, como um que não existe.

## Apresentação

Cada tela segue o [padrão de tela de administração](design.md#tela-de-administração) (decidido em 2026-10-27, [telas de Configurações](administracao-telas.md)). Rotas, níveis, PIN e arquivos acima não mudam; muda o texto e a ordem.

| Tela | Para que serve (frase da tela) | Faixa de situação | Vazio, com saída |
|---|---|---|---|
| Decretos | "Ordens suas que valem acima das regras: liberar algo uma única vez, ou criar uma regra que vale até você cancelar." | "N decretos em vigor", e o que vence hoje; **Criar decreto** | "Para que serve um decreto?": o exemplo "pode rodar npm test sem pedir" e o passo de ler o "Entendi: …" antes de confirmar |
| Delegações | "Permissão para o Grão-Mestre ou o Mestre da mesa decidirem pedidos no seu lugar, por tempo limitado. Revogar corta na hora." | "N delegações ativas", a que vence antes | "Ninguém decide por você": delegar é um decreto, com link para Decretos |
| Supervisão da cadeia | "Quando um agente pede permissão, o pedido passa por uma fila de quem aprova. Aqui você vê se a fila anda. **Pausar autonomia** só tira do Mestre da mesa o poder de decidir sozinho: os pedidos sobem ao Grão-Mestre e o trabalho continua. Para parar a mesa de vez, use **Pausar mesa**, dentro dela." | "N mesas com fila, M pausadas" e o pedido mais antigo | "A fila aparece com a primeira mesa", com **Falar com o Grão-Mestre** |
| Teto da campanha | "O máximo que qualquer classe da campanha pode ter de permissão e de custo. Só sobe por decreto." | "Teto em vigor" ou "A campanha não tem teto próprio" | sem arquivo: valem só as regras fixas; **Pedir um teto maior** abre o decreto |
| Política de aprovação | "O arquivo que diz o que o Mestre aprova sozinho e o que sempre pede uma pessoa." | "Política válida", última gravação | sempre há arquivo; o editor ganha **Validar** antes de **Salvar** |
| Instruções extras | "Recados seus para o Grão-Mestre e para os Mestres de mesa…" | "Sem instruções extras" | um exemplo de recado no campo |
| Consumo de uso | "Quanto os agentes gastaram e quanto falta antes de bater cada limite." | o limite mais próximo ("62% usado") | "Nada gasto ainda"; "sem dado" explicado |
| Auditoria | "O registro de quem mudou o quê e quando." | filtros no alto | frase por ação |

- **Revogar todas as delegações** vai para uma seção "Emergência" no fim da tela, nunca no alto; **Renovar** fica no cartão da delegação.
- Cada decreto mostra "Você disse" (a ordem original), a validade e quantas vezes foi usado; a tabela da supervisão vira uma frase por mesa com a pausa (quem, quando, motivo).
- O arquivo do teto e o texto bruto do servidor ficam em "Detalhes" recolhidos.

## Provas

Os critérios são os da [administração](administracao.md#critérios-de-aceite): CA-ADM-04 (teto), CA-ADM-07 (política), CA-ADM-12 (delegações), CA-ADM-13 (supervisão da cadeia) e, para a apresentação, CA-ADM-17, 18, 23, 24 e 25, provados em `internal/panel/rules_test.go` e `web/src/screens/admin/rules.test.tsx`. O smoke do navegador faz e revoga um decreto. Que os pedidos abertos sobem ao Grão-Mestre na pausa é do motor (CA-SUPCAD-02); a tela só pede a pausa e mostra o resultado.

## Depende de

- [Administração](administracao.md)
- [Decretos](../telegram/decretos.md)
- [Delegação](../aprovacoes/delegacao.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
