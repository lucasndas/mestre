# Parear um nó

> **Assunto:** passo a passo para gerar o convite, parear um nó (Windows ou Ubuntu) com o Core na rede local, conferir e revogar · **Componente:** Node · **Atualizado:** 2026-10-12

Guia para quem usa. Só o Dono pareia e revoga nós. Como o pareamento funciona por dentro (prova mútua, certificados, validade do convite): [pareamento](../conectividade/pareamento.md). Como o nó acha o Core: [descoberta](../conectividade/descoberta.md).

Antes: o Core instalado e o [primeiro acesso](primeiro-acesso.md) feito (Dono criado).

## 1. Gerar o convite

1. No painel, abra **Saúde dos nós** e clique em **Gerar convite de nó**.
2. Dê um nome ao nó (ex.: `pc-lucas`) e confirme com o seu PIN.
3. O painel mostra o **código de convite**, no formato `XXXX-XXXX-XXXX-XXXX`. Ele é de uso único e vence ([prazo](../conectividade/pareamento.md#convite)). Não mande o código por canal aberto; o ideal é digitá-lo direto no PC.

## 2. Usar o convite no nó

O convite é pedido durante a instalação do nó, no Windows ou no Ubuntu ([instalar](instalar.md#escolha-o-uso)). O nó local do Core não precisa de convite: a instalação do Core já o pareia.

1. O instalador procura o Core na rede local sozinho e mostra o que achou (nome e endereço).
2. Cole o código de convite.
3. O instalador conclui o pareamento: o nó passa a ter credenciais próprias e a confiar na CA do Core, sem você conferir nada à mão.
4. Se aparecer **"convite inválido ou expirado"**, gere outro convite no painel e rode o instalador de novo (ou, num terminal de administrador, `mestre node pair --invite <código>`; [componentes](../arquitetura/componentes.md#mestre)). Ele não duplica nada: confere o que já existe e completa o que falta.

### Se o Core não for encontrado

| Verifique | Como |
|---|---|
| Mesma rede | O PC e o servidor estão na mesma rede local (mesmo roteador, sem rede de convidados). |
| Perfil de rede | Em cada máquina Windows (Core ou nó): Configurações → Rede e Internet → sua conexão → **Privada**. |
| Firewall do Core | Core no Ubuntu: `sudo ufw status` deve liberar as portas do Mestre; Core no Windows: o [checklist](../plataforma/checklist.md) do Core mostra as regras ([portas](../arquitetura/componentes.md#portas)). |
| Serviço do Core | Ubuntu: `systemctl status mestre-core` em `active (running)`; Windows: `Get-Service mestre-core` em `Running`. |
| Último recurso | Informe no instalador o endereço do Core (ex.: `servidor.local:7443` ou o IP). O nó continua redescobrindo se o IP mudar ([descoberta](../conectividade/descoberta.md)). |

## 3. Conferir

1. No painel, em **Saúde dos nós**, o nó aparece como **conectado**, com versões do motor e do Guardião e o último sinal de vida.
2. Na coluna **Capacidades**, confira o sistema (`os`) e os provedores encontrados.
3. Na coluna **Checklist**, resolva os alertas abertos com a correção oferecida ([checklist](../plataforma/checklist.md)).
4. Ajuste a **Prioridade** do nó, se tiver mais de um. Ela decide qual nó conduz a recuperação quando o Core cai ([camadas](../autocura/camadas.md#eleição-do-nó-condutor)).

O nó reconecta sozinho depois de reinícios e de mudanças de IP. Não há nada a refazer.

## Revogar um nó

Use quando a máquina for aposentada, trocada, perdida ou se suspeitar de que foi comprometida.

1. No painel, em **Saúde dos nós**, escolha o nó e clique em **Revogar**. Confirme com o PIN.
2. Na hora, o nó e o Guardião dele perdem o acesso ao Core, e os personagens que rodavam ali aparecem como **interrompidos** no canvas.
3. Para essa máquina voltar, gere um convite novo e rode o instalador de novo. Ela entra como um nó novo; o histórico do antigo fica guardado.
4. Se a máquina não vai mais ser usada, [desinstale](desinstalar.md#remover-um-nó) o Mestre dela.

## Nó fora da rede local

Informe no instalador o endereço do Core (nome ou IP e porta) que alcança a rede dele; a descoberta automática só funciona na rede local ([descoberta](../conectividade/descoberta.md#nó-fora-da-rede-local)).
