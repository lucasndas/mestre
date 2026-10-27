package sessionzero

// DefaultPolicyYAML is the politica-de-aprovacao.yaml the session zero
// writes: the default content of docs/aprovacoes/arquivo-de-politica.md,
// which owns the format (the approval flow, milestone 5, reads and checks it;
// a test compares this text with the document).
const DefaultPolicyYAML = `versao: 1
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
`
