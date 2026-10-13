# ADR 0019: Autoatualização por Releases do GitHub, distribuída pela Cidadela

> **Assunto:** por que o Mestre se atualiza a partir das Releases do GitHub, com binários assinados, distribuídos pela Cidadela e aplicados alternando motor e Guardião · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. Decisão completada em 2026-10-12: a chave privada de assinatura fica só no computador do autor, e cada versão é uma release única com os dois binários. **Revista em 2026-10-13:** (1) pelo [ADR 0022](0022-guilda-cidadela-e-torres.md), Core e nó viram Cidadela e Torre: quem consulta as Releases e distribui é a Cidadela atual, e a ordem é Cidadela primeiro, depois uma Torre por vez; (2) **os CLIs dos provedores saem desta decisão:** eles se atualizam sozinhos, o Mestre não desliga a autoatualização deles e não controla mais a versão deles (não aprova, não instala, não volta versão); só detecta a versão nova e roda a conformidade depois ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).

## Contexto

- **O pedido do usuário:** o Mestre deve se atualizar sozinho, motor e Guardião, depois de uma aprovação de Dono ou Administrador, sem ninguém entrar nas máquinas.
- **O código** fica num repositório público no GitHub ([ADR 0018](0018-licenca-polyform-noncommercial.md)).
- **A rede:**
  - nenhuma porta aberta na internet;
  - as Torres só falam com a Cidadela, por WSS iniciada por elas ([ADR 0002](0002-wss-iniciado-pelo-no.md));
  - tudo roda na rede local.
- **A autocura** depende de o Guardião continuar de pé quando o motor quebra ([ADR 0003](0003-autocura-com-guardiao-sem-ssh.md)), e nenhuma ação de cura pode alterar o Guardião. Mas o Guardião também precisa receber correções de vez em quando.
- **O risco:** um binário trocado é o pior ataque possível, porque roda como serviço, e o Guardião roda como root/LocalSystem. Só HTTPS e SHA-256 não protegem contra uma release ou uma Cidadela comprometidas.

## Opções consideradas

### De onde vem e como chega

1. **Releases do GitHub, consultadas pela Cidadela e distribuídas às Torres pela WSS.**
   - Prós:
     - uma única saída para a internet;
     - as Torres continuam sem falar com ninguém além da Cidadela;
     - reaproveita a transferência em partes que já existe para os git bundles;
     - a Cidadela controla a ordem e a trava.
   - Contras:
     - a Cidadela vira ponto de passagem dos binários, o que pede verificação também na Torre;
     - a Cidadela precisa guardar os binários de todas as plataformas em uso.
2. **Cada máquina baixa direto do GitHub.**
   - Prós: a Cidadela não carrega binários.
   - Contras:
     - toda máquina precisa de saída para o GitHub;
     - a ordem e "um por vez" ficam mais difíceis de garantir;
     - várias consultas iguais.
3. **Atualização manual** (baixar e rodar o instalador em cada máquina).
   - Prós: simples e sem automação para quebrar.
   - Contras:
     - contraria o pedido do usuário;
     - exige acesso a cada máquina;
     - não tem volta automática;
     - na prática, as máquinas ficam em versões velhas.

### Como garantir que o binário é o certo

1. **Só HTTPS + SHA-256 publicado na release:** não protege se a release ou a conta forem comprometidas.
2. **GPG:** maduro, mas a verificação em Go exige biblioteca pesada e o formato é complexo.
3. **Atestados do GitHub / Sigstore:** amarram ao workflow que gerou o binário, mas a verificação depende de serviço online e de bibliotecas grandes.
4. **minisign (Ed25519) sobre um manifesto com os hashes:**
   - formato pequeno e estável;
   - verificação offline com `crypto/ed25519` da biblioteca padrão;
   - chave pública embutida no binário;
   - uma assinatura cobre todos os binários.

### Como atualizar o motor e o Guardião

1. **O Guardião atualiza os dois:** o Guardião ficaria grande e teria de se substituir sozinho, sem ninguém vigiando.
2. **Os dois juntos:** se a versão nova falhar, não sobra ninguém para voltar.
3. **Guardião só manual:** o Guardião ficaria sem correções.
4. **Alternado:**
   - o Guardião vigia e desfaz a troca do motor;
   - o motor saudável troca e desfaz a do Guardião;
   - nunca os dois ao mesmo tempo.

## Decisão

- **Origem e distribuição:** Releases do GitHub, consultadas só pela Cidadela (consulta periódica condicional) e entregues às Torres pela WSS, com transferência `kind: release`.
- **Assinatura:**
  - **minisign/Ed25519** sobre o manifesto `mestre-release.json`, que lista o SHA-256 de cada binário;
  - as chaves públicas (atual e próxima) ficam embutidas nos binários;
  - a verificação acontece na Cidadela, na máquina de destino e no ajudante de troca;
  - versão menor que a instalada é recusada;
  - a chave privada fica **só no computador do autor**, num arquivo cifrado com senha (formato nativo do minisign), fora do repositório e do GitHub Actions; as versões são assinadas na máquina do autor antes de publicar.
- **Release única:** cada versão é uma release com tag `vX.Y.Z` e os dois binários (`mestre` e `mestre-guardiao`); a versão do Guardião vai só no manifesto, sem tags próprias.
- **Aprovação:** o fluxo normal de aprovação, com Dono ou Administrador. O Grão-Mestre não aprova sozinho. Depois da aprovação, o Mestre aplica e reinicia sozinho.
- **Ordem:**
  - Cidadela primeiro, depois uma Torre por vez;
  - em cada máquina, primeiro o motor, depois o Guardião;
  - uma trava única: nunca duas peças ao mesmo tempo, nem Cidadela e Torre juntas.
- **Alternância:** opção 4. A atualização do Guardião é uma **ação de atualização aprovada**, não uma ação de cura. Só começa com o motor saudável.
- **Detalhes:** o comportamento completo está em [autoatualização](../plataforma/autoatualizacao.md). Os CLIs dos provedores não seguem esta decisão: atualizam-se sozinhos, e o Mestre só detecta e confere ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).

## Consequências

- **Positivas:**
  - Nenhuma porta nova. As Torres continuam sem acesso ao GitHub.
  - Uma Cidadela comprometida não consegue instalar um binário não assinado numa Torre.
  - Toda troca tem volta automática, vigiada por quem não está sendo trocado.
  - A regra "nenhuma ação de cura altera o Guardião" continua valendo.
- **Negativas:**
  - É preciso guardar e girar uma chave privada de assinatura fora do repositório.
  - É preciso um ajudante de troca com privilégio (root/LocalSystem) que só a conta do motor pode iniciar.
  - As migrações do banco precisam ser aditivas para a volta funcionar sem restaurar o banco; restaurar o banco é ação rara, nunca automática, com Dono ou Administrador e PIN ([Guardião](../autocura/guardiao.md#rollback-com-restauração-do-banco)).
  - Uma atualização completa leva tempo: um passo por vez, com janela de confirmação e espera de ponto seguro dos terminais.
- **Riscos:**
  - **Perder as duas chaves privadas** (a atual e a próxima, ambas no computador do autor) obriga a uma atualização manual para trocar as chaves embutidas; perder só a atual não, porque a próxima assume.
  - **Máquina desligada por muito tempo** pode sair da faixa de protocolo e precisar de atualização manual.

## Fontes

- Última release (GitHub REST): https://docs.github.com/en/rest/releases/releases#get-the-latest-release
- Requisições condicionais: https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- minisign: https://jedisct1.github.io/minisign/ · implementação em Go: https://github.com/aead/minisign
- Ed25519 na biblioteca padrão do Go: https://pkg.go.dev/crypto/ed25519
