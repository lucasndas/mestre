# Entrar na Guilda

> **Assunto:** passo a passo para uma máquina nova (Windows ou Ubuntu) entrar na Guilda: comparar o código de entrada, aceitar com PIN, conferir, marcar "pode ser Cidadela" e remover uma máquina · **Componente:** Todos · **Atualizado:** 2026-10-13

Guia para quem usa. Só o Dono aceita e remove máquinas. Como a entrada funciona por dentro (código de comparação, certificados, prazo): [entrada na Guilda](../guilda/guilda.md#entrada-na-guilda). Como a máquina acha a Guilda: [descoberta](../conectividade/descoberta.md).

Antes: a primeira máquina instalada (a Cidadela) e o [primeiro acesso](primeiro-acesso.md) feito (Dono criado). Um pedido feito antes de o Dono existir espera e aparece no painel depois do assistente do Dono.

## 1. Instalar na máquina nova

Rode o instalador normal ([instalar](instalar.md)). Ele procura a Guilda na rede local sozinho e mostra o que achou (nome da Cidadela e quantas máquinas). Se achar mais de uma Guilda, pergunta em qual entrar.

No fim, o instalador mostra o **código de entrada**, com 6 dígitos (ex.: `123 456`), e fica esperando a resposta do Dono. Deixe a janela aberta.

## 2. Aceitar no Telegram ou no painel

1. O Dono recebe no Telegram (e vê no painel, em **Guilda e saúde das máquinas**) a pergunta "Aceitar `<máquina>` (`<sistema>`) na Guilda? Código 123 456".
2. **Compare o código** da mensagem com o da tela do instalador. Só aceite se forem iguais; diferentes indicam que alguém está no meio da conexão: recuse.
3. Toque em **Aceitar** e responda o seu PIN. Na mesma hora, marque se a máquina **pode ser Cidadela** (padrão: não; dá para mudar depois). Só as marcadas recebem a cópia do cofre e podem assumir se a Cidadela cair ([replicação](../guilda/replicacao.md#cofre-só-nas-candidatas)).
4. O instalador termina sozinho: a máquina recebe a configuração, passa a confiar na CA da Guilda e conecta como Torre.

O pedido expira se ninguém responder a tempo ([prazo](../guilda/guilda.md#entrada-na-guilda)): rode o instalador de novo. Ele não duplica nada: confere o que já existe e completa o que falta.

### Se a Guilda não for encontrada

| Verifique | Como |
|---|---|
| Mesma rede | O PC e a Cidadela estão na mesma rede local (mesmo roteador, sem rede de convidados). |
| Perfil de rede | Em cada máquina Windows: Configurações → Rede e Internet → sua conexão → **Privada**. |
| Firewall da Cidadela | Ubuntu: `sudo ufw status` deve liberar as portas do Mestre; Windows: o [checklist](../plataforma/checklist.md) da Cidadela mostra as regras ([portas](../arquitetura/componentes.md#portas)). |
| Serviço da Cidadela | Ubuntu: `systemctl status mestre` em `active (running)`; Windows: `Get-Service mestre` em `Running`. |
| Cidadela fora | Se o instalador disser que a Guilda existe mas a Cidadela está fora, ele não cria outra Guilda: espere a Cidadela voltar e rode de novo. |
| Último recurso | Informe o endereço da Cidadela (ex.: `mestre.local:7443` ou o IP), como na seção abaixo. A máquina continua redescobrindo se o IP mudar. |

**Criou uma Guilda por engano?** Se a máquina não achou a Guilda e criou outra, e ainda não tem nada, rode num terminal de administrador `mestre join <endereço da Cidadela>` ([componentes](../arquitetura/componentes.md#mestre)).

## 3. Conferir

1. No painel, em **Guilda e saúde das máquinas**, a máquina aparece como Torre **conectada**, com versões do motor e do Guardião e o último sinal de vida.
2. Na coluna **Ficha da máquina**, confira o sistema e os provedores encontrados ([ficha da máquina](../guilda/ficha-da-maquina.md)).
3. Na coluna **Checklist**, resolva os alertas abertos com a correção oferecida ([checklist](../plataforma/checklist.md)).
4. Configure os **perfis de conta** da máquina: instalação do CLI, login de cada conta e, no Codex, a aprovação dos hooks do Mestre ([primeiro acesso](primeiro-acesso.md#4-configurar-os-perfis-de-conta)). Sem isso, a máquina não recebe agentes daquele perfil.
5. Ajuste a **Prioridade** da máquina, se a Guilda tiver mais de uma Torre. Ela decide qual Torre conduz a recuperação e propõe assumir quando a Cidadela cai ([camadas](../autocura/camadas.md#eleição-da-torre-condutora)).

A máquina reconecta sozinha depois de reinícios, de mudanças de IP e de uma troca de Cidadela. Não há nada a refazer.

## Remover uma máquina

Use quando a máquina for aposentada, trocada, perdida ou se suspeitar de que foi comprometida.

1. No painel, em **Guilda e saúde das máquinas**, escolha a máquina e clique em **Remover**. Confirme com o PIN.
2. Na hora, a máquina e o Guardião dela perdem o acesso à Guilda, e os personagens que rodavam ali aparecem como **interrompidos** no canvas. Se ela era candidata e estava offline, o painel recomenda substituir os segredos do cofre.
3. Para essa máquina voltar, rode o instalador de novo e aceite a entrada. Ela entra como uma máquina nova; o histórico da antiga fica guardado.
4. Se a máquina não vai mais ser usada, [desinstale](desinstalar.md#remover-uma-torre) o Mestre dela.

A Cidadela não é removida assim: antes, passe o comando a uma Torre candidata ([troca planejada](../guilda/cidadela-e-torres.md#troca-planejada)).

## Máquina fora da rede local

A descoberta automática só funciona na rede local. Informe o endereço da Cidadela (nome ou IP e porta) que alcança a rede dela: no Ubuntu, `sudo bash install.sh --join <endereço>`; no Windows, depois do instalador, num terminal de administrador, `mestre join <endereço>` ([formação](../guilda/guilda.md#formação)).
