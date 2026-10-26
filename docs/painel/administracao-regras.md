# Administração: regras e cadeia

> **Assunto:** implementação das telas de decretos, delegações e regras temporárias, supervisão da cadeia, teto, política de aprovação e instruções extras (rotas, níveis, PIN, auditoria e arquivos) · **Componente:** Painel · **Atualizado:** 2026-10-26

Documento de referência. Diz como as seis telas de [administração](administracao.md#telas) que mexem em regras e na cadeia foram feitas, para quem for mudá-las. O que cada tela mostra está em [administração](administracao.md); as regras de cada assunto ficam no documento dono, linkado lá.

## Implementação

Código: `internal/panel/routes_decrees.go`, `routes_delegations.go`, `routes_chain.go` e `routes_workspace_rules.go` (tipos em `types_rules.go`, gerados em `web/src/gen/panel.ts`), as portas em `rules_ports.go` e as telas em `web/src/screens/admin/` (`Decrees`, `Delegations`, `Chain`, `Ceiling`, `Policy`, `Instructions`, com `rulesKit.tsx`). O painel não repete nenhuma regra: nível, workspaces, PIN e recusas são do serviço de [decretos](../telegram/decretos.md) (`internal/decrees`), da supervisão (`chain.Supervisor`) e das regras temporárias (`tablerules`), os mesmos do Telegram. O motor entrega essas peças por `Panel.SetRules`, depois do painel, porque a orquestração sobe depois dele; antes disso as telas respondem que não estão disponíveis.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/decrees` (`?all=1`) | Observador ou acima | decretos em vigor (e, com `all`, os que acabaram) dos workspaces de quem pede, com autor, ordem original, validade, os últimos usos e se o nível revoga e se pede PIN |
| `POST /api/decrees` | Aprovador ou acima | proposta de regra permanente (permissão ou comportamento) ou de exceção pontual; devolve o texto do "Entendi: …" e se pede PIN; **não é decreto** |
| `POST /api/decrees/proposals/{id}/confirm` | o autor da proposta | o clique; com o PIN no cabeçalho quando a proposta pede (sem ele, `pin_required`); só aí nasce o decreto |
| `POST /api/decrees/{n}/revoke` | por nível, como no Telegram | revoga; pede PIN só se o decreto restringia algo |
| `GET /api/delegations` | Observador ou acima | delegações, subdelegações e regras temporárias em vigor, com as decisões tomadas com base em cada uma |
| `POST /api/decrees/{n}/renew` | Dono | proposta de renovação da delegação (o Dono a confirma com PIN, como qualquer decreto) |
| `POST /api/subdelegations/{id}/revoke`, `POST /api/table-rules/{id}/revoke` | Administrador ou acima | revoga uma subdelegação ou uma regra temporária |
| `POST /api/delegations/revoke-all` | Dono | **Revogar todas as delegações**; só restringe, então não pede PIN ([delegação](../aprovacoes/delegacao.md#revogar-tudo)) |
| `GET /api/chain` | Observador ou acima | por mesa: a pausa (quem, quando, motivo) e as filas (abertos, o mais antigo, espera média) por nível |
| `POST /api/chain/{workspace}/tables/{mesa}/pause` e `/resume` | Aprovador ou acima | corpo `{reason}` (obrigatório); sem PIN, só restringe; retomar a pausa do Grão-Mestre é recusado com `grand_master_only` |
| `GET /api/workspaces/{ws}/ceiling` | Observador ou acima | o arquivo do teto (só leitura) e os decretos `ceiling` em vigor |
| `POST /api/workspaces/{ws}/ceiling/widen` | Administrador ou acima | abre a **proposta** de decreto `ceiling`; nunca edita o arquivo, e o teto em vigor só muda depois da confirmação com PIN |
| `GET`, `PUT /api/workspaces/{ws}/policy` | Dono | lê e grava `politica-de-aprovacao.yaml` |
| `GET`, `PUT /api/workspaces/{ws}/instructions` | Administrador ou acima | lê e grava `instrucoes/grao-mestre.md` e `instrucoes/mestre-da-mesa.md` (até 4 KiB cada) |

- **Pausa do Grão-Mestre:** o motor deixa o Grão-Mestre ou um Aprovador retomar a pausa dele ([supervisão](../orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia)); o painel é mais estrito, como pede a [administração](administracao.md#telas): quem usa a tela nunca retoma uma pausa do Grão-Mestre.
- **Arquivos:** ficam em `.mestre/regras/` do clone principal, lidos e gravados por `safefile` (um link ou arquivo que não é comum é recusado com `file_refused`). A política é validada inteira por `chain.ParsePolicyFile` antes de gravar; um arquivo inválido não grava nada e o erro traz a `line` (a do YAML ou, numa regra da política, a da chave). Quem salva manda a `version` (hash) que abriu: arquivo que mudou depois dá `file_changed`.
- **Auditoria:** `panel.decree.{proposed,created,revoked,renewal_proposed}`, `panel.subdelegation.revoked`, `panel.table_rule.revoked`, `panel.delegations.revoked_all`, `panel.chain.{paused,resumed}`, `panel.ceiling.widen_proposed`, `panel.policy.{saved,refused}` e `panel.instructions.saved`, todas com `user_id`, `workspace` e `before`/`after` (na política, as linhas que saíram e as que entraram). Nenhuma leva PIN.
- Um workspace que não é de quem pede responde `not_found`, como um que não existe.

## Provas

Os critérios são os da [administração](administracao.md#critérios-de-aceite): CA-ADM-04 (teto), CA-ADM-07 (política), CA-ADM-12 (delegações) e CA-ADM-13 (supervisão da cadeia), provados em `internal/panel/rules_test.go` e `web/src/screens/admin/rules.test.tsx`. O smoke do navegador faz e revoga um decreto. Que os pedidos abertos sobem ao Grão-Mestre na pausa é do motor (CA-SUPCAD-02); a tela só pede a pausa e mostra o resultado.

## Depende de

- [Administração](administracao.md)
- [Decretos](../telegram/decretos.md)
- [Delegação](../aprovacoes/delegacao.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
