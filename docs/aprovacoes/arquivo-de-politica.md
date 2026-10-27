# Arquivo de política

> **Assunto:** o arquivo `politica-de-aprovacao.yaml` de cada campanha: onde fica, quem edita, conteúdo padrão (condições, áreas e comandos sensíveis, tipos, quórum, prazos da cadeia, ajustes de risco, delegação e resumo) e regras de leitura · **Componente:** Motor · **Atualizado:** 2026-10-18

Documento de referência da [política de aprovação](politica.md), que é dona do comportamento e dos critérios de aceite. O sentido de cada seção nova está no documento dono dela: `cadeia` na [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) e na [supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md), `risco` na [classificação de risco](../classes/classificacao-de-risco.md#ajustes-do-senhor-da-guilda), `delegacao` na [delegação](delegacao.md) e `resumo` em [resumo e sugestões](resumo-e-sugestoes.md).

## Onde fica e quem edita

- Fica em `.mestre/regras/politica-de-aprovacao.yaml`, no clone principal da campanha na Cidadela. É criado na [sessão zero](../sessao-zero/workspace.md) com o conteúdo padrão abaixo.
- Editado só pelo Senhor da Guilda ([tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)), no painel ([administração](../painel/administracao.md)) ou direto no arquivo. Nenhum agente pode editá-lo: é regra fixa do Mestre e também permissão do sistema: o motor o grava 0644 (sem escrita para o grupo `mestre-agente`), como o teto e as instruções extras da pasta `regras/`, e o motor aperta os arquivos e pastas de `regras/` que o grupo consiga escrever (uma versão anterior, o checkout do avanço do clone, a sessão zero, ao achar a campanha e ao abrir a tela). Na leitura o motor falha fechado (`ErrUntrusted`: vale a última versão válida, com alerta): o arquivo, ou uma pasta de `.mestre/` até ele, que não seja do usuário do motor (ou root) ou que o grupo ou os outros possam escrever não vale; quem edita à mão precisa ser o usuário do motor (CA-SZ-WS-09).
- O motor lê o arquivo a cada uso, só pelo caminho seguro (um link, um arquivo que não é regular ou com mais de 256 KiB não vale) e valida ao carregar. Arquivo inválido: continua valendo a última versão válida e o Senhor da Guilda recebe alerta com o erro. Cada pedido e cada decisão guardam a versão (hash) do arquivo que valia ([auditoria](fluxo.md#auditoria)).

## Conteúdo padrão

É também o exemplo do formato:

```yaml
versao: 1
expiracao: 24h                 # prazo padrão das pessoas para um pedido (ver fluxo.md)

condicoes:                     # o motor confere antes de aceitar a aprovação do Grão-Mestre
  ci_verde: true
  revisao_por_outro_personagem: true
  max_arquivos: 25
  max_linhas: 600              # adições + remoções
  max_arquivos_apagados: 5
  dentro_do_teto_de_custo: true
  linhas_ignoradas: ["**/package-lock.json", "**/yarn.lock", "**/pnpm-lock.yaml", "**/go.sum", "**/Cargo.lock", "**/*.snap", "**/gen/**"]   # não contam em max_linhas nem na busca de palavras

areas_sensiveis:               # caminhos (glob); tocou, pergunta
  autenticacao: ["**/auth/**", "**/*login*", "**/*session*"]
  pagamentos: ["**/payment*/**", "**/billing/**", "**/checkout/**"]
  migracoes: ["**/migrations/**", "**/*.sql"]
  infraestrutura: ["infra/**", "terraform/**", "k8s/**", "**/Dockerfile", "**/docker-compose*.yml", ".github/**"]
  permissoes_e_segredos: ["**/.env*", "**/secrets/**", "**/*permission*", "**/*.pem", "**/*.key", "**/id_rsa*", "**/id_ed25519*", "**/id_ecdsa*", "**/.npmrc", "**/credentials*.json", "**/service-account*.json"]
  mestre: [".mestre/**", "AGENTS.md", "CLAUDE.md"]
  palavras_em_linhas_adicionadas: ["DROP", "TRUNCATE", "DELETE FROM"]   # chave reservada: não é uma área, é uma lista de palavras

comandos_sensiveis:            # para permissao_de_classe; casou, o Grão-Mestre pergunta
  - "rm -rf *"
  - "git push*"
  - "*DROP TABLE*"
  - "*TRUNCATE*"
  - "*DELETE FROM*"

tipos:                         # sozinho: se_condicoes | nunca (o que o Grão-Mestre decide no nível dele)
  pr: { sozinho: se_condicoes, nivel_minimo: aprovador }
  merge: { sozinho: se_condicoes, nivel_minimo: aprovador }
  ponto_de_missao: { sozinho: se_condicoes, nivel_minimo: aprovador }
  permissao_de_classe: { sozinho: se_condicoes, nivel_minimo: aprovador, expiracao: 2h }
  custo_extra: { sozinho: se_condicoes, nivel_minimo: aprovador }
  envio_workflows: { sozinho: nunca, nivel_minimo: administrador }
  configuracao_repositorio: { sozinho: nunca, nivel_minimo: dono, pin: true }
  classe_workspace: { sozinho: nunca, nivel_minimo: administrador }
  acao_destrutiva: { sozinho: nunca, nivel_minimo: administrador }
  proposta_agents_md: { sozinho: nunca, nivel_minimo: administrador }

quorum:                        # quando o pedido chega às pessoas
  padrao: { aprovacoes: 1 }
  regras: []                   # ex.: { tipo: merge, mesa: "epico/42-login", aprovacoes: 2, exigir_usuarios: [lucas] }

cadeia:                        # níveis de agentes (ver cadeia-de-aprovacao.md)
  prazo_agente: 2m             # superior agente e Mestre da mesa
  prazo_grao_mestre: 10m
  regra_temporaria_max: 24h    # validade máxima de uma regra temporária de mesa
  mesa_decide: [rede, apagar, conteineres_e_vms, pacotes_e_executores, interpretador_com_codigo, script_alterado, fora_da_lista]   # grupos de medio que o Mestre da mesa decide (ver cadeia-de-aprovacao.md#condições-da-mesa)
  negacoes_para_pausar: 3      # negações seguidas que pausam o personagem
  resumo_mesas: 30m            # resumo das decisões das mesas ao Grão-Mestre (ver supervisao-da-cadeia.md)
  alerta_fila: { abertos: 10, fator: 2 }   # fila que acumula: abertos, ou o mais antigo acima de fator × prazo

risco:                         # ajustes do Senhor da Guilda à tabela de risco (ver classificacao-de-risco.md)
  ajustes: []                  # ex.: { comando: "make deploy-teste", risco: alto }
  nivel_minimo: { alto: administrador }

delegacao:                     # ver delegacao.md
  validade_padrao: 30d
  aviso_antes: 3d

resumo:                        # resumo do que os agentes decidiram (ver resumo-e-sugestoes.md)
  frequencia: diario           # diario | semanal | desligado
  horario: "18:00"
  para: [dono]
  amostra: 5                   # ações aprovadas por agentes sorteadas para revisão, de cada nível (Mestres de mesa, Grão-Mestre)
  sugerir_apos: 3              # aprovações do mesmo padrão em 7 dias viram sugestão de regra
```

## Regras de leitura

- `quorum.regras`: cada regra casa por `tipo` e/ou `mesa` (aceita `*`). Uma regra que casa com o pedido **obriga a perguntar** às pessoas, mesmo que as condições passem e mesmo com delegação. Se várias casam, vale a mais exigente.
- `condicoes.linhas_ignoradas` e `areas_sensiveis.palavras_em_linhas_adicionadas` são lidos como a [política](politica.md#o-que-o-motor-lê-do-pr) descreve: glob malformado ou palavra vazia torna o arquivo inválido; uma lista vazia em `areas_sensiveis.permissoes_e_segredos` ou `mestre` também (apagaria a proteção: apague a chave para valer o padrão), e uma chave que o motor não conhece, em qualquer seção lida por ele (`cadeia`, `risco`, `tipos`), idem. Uma campanha que tem arquivos gerados próprios acrescenta os globs em `linhas_ignoradas`; o que não está na lista conta.
- `exigir_usuarios` lista usuários do cadastro ([usuários e níveis](../telegram/usuarios-e-niveis.md)) cuja aprovação é obrigatória.
- `sozinho` vale só para tipos de risco `medio`, o teto do Grão-Mestre; num tipo `alto`, `se_condicoes` torna o arquivo inválido: o `alto` só se abre ao Grão-Mestre por [delegação](delegacao.md).
- Prazos (`expiracao`, `cadeia.*`, `delegacao.*`) são durações positivas (`30s`, `10m`, `2h`, `30d`); `regra_temporaria_max` vai até 7 dias e `delegacao.validade_padrao` até 30 dias e `delegacao.aviso_antes` menor que ela; `negacoes_para_pausar` vai de 1 a 10; `alerta_fila.abertos`, de 1 a 1000, e `alerta_fila.fator`, de 1 a 10. `cadeia.mesa_decide` lista os grupos de `medio` que o Mestre da mesa decide, os da [tabela de grupos](../orquestracao/cadeia-de-aprovacao.md#condições-da-mesa) (fonte única); o Senhor da Guilda acrescenta ou tira qualquer um, e `[]` deixa só o `baixo` com ele. Nome que não é um desses grupos (`alto`, `fixo`, um desconhecido) torna o arquivo inválido. Nenhuma chave muda o teto de cada nível (`baixo`, `medio`) nem as condições de cada grupo, que são do motor.
- `resumo`: `frequencia` é `diario`, `semanal` ou `desligado`; `horario` é `HH:MM`; `para` é uma lista não vazia de `dono`, `administradores` ou nomes de usuários; `amostra` vai de 0 a 50 e `sugerir_apos`, de 2 a 20; qualquer outro valor, ou chave desconhecida, torna o arquivo inválido.
- Campos ausentes assumem o valor do conteúdo padrão. Um `tipo` desconhecido, um dos tipos de regra fixa (`ativacao_operador`, `entrada_na_guilda`, `promocao_cidadela`, `atualizacao_mestre`) ou um ajuste de risco inválido ([ajustes](../classes/classificacao-de-risco.md#ajustes-do-senhor-da-guilda)) torna o arquivo inválido.
- O comportamento de quórum e expiração está em [fluxo](fluxo.md); o do resumo, em [resumo e sugestões](resumo-e-sugestoes.md).
