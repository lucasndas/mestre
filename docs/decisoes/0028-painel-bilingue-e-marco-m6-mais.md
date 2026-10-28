# ADR 0028: Painel bilíngue e o marco M6+

> **Assunto:** o painel em português do Brasil e inglês no M6, a conversão das recusas do motor em código e parâmetros, a tradução do Telegram e da CLI e o Grão-Mestre no idioma do usuário (marco M6+) · **Componente:** Painel · **Atualizado:** 2026-10-28

## Status

Aceita, 2026-10-26, pelo Dono. **Amplia o [escopo do MVP](../produto/escopo-mvp.md)** por decisão dele nesta data: o MVP era só em português.

## Contexto

O painel é a primeira interface que o usuário vê em um navegador, e os textos que o motor devolve hoje (motivos de recusa, mensagens do Telegram e da CLI) são frases em português montadas no Go. Traduzir o painel sem mexer nisso deixaria as recusas num idioma só.

## Opções consideradas

1. **Só português no MVP, tradução depois.** Mais barato agora; cada texto novo do painel seria reescrito depois e as recusas do motor continuariam presas ao português.
2. **Painel bilíngue já no M6, e as mensagens do motor num marco próprio (M6+).** O painel nasce com catálogos; o M6+ muda o que o motor devolve sem atrasar as telas.
3. **Tudo bilíngue dentro do M6.** Mistura a conversão das recusas (motor, Telegram, CLI) com o painel e alarga demais o marco.

## Decisão

Opção 2.

1. **No M6** o painel é bilíngue (pt-BR e en) com `react-i18next`. Há um catálogo por idioma e uma verificação **da suíte rápida** de que os dois têm as mesmas chaves. Querem-se também explicações "no mundo real" para os termos de RPG ([glossário](../produto/glossario.md)); a forma e o modelo de dados delas (e qualquer mudança no formato do glossário) ficam para o trabalho de design da unidade 1C, ainda não decidido.
2. **Novo marco M6+**, como o M4+ e o M5+: sem documentos de funcionalidade, auditado antes do M7 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#marcos)). Ele:
   - converte os motivos de recusa do motor em **código e parâmetros** (o texto sai do cliente);
   - traduz as mensagens humanas do Telegram e da CLI;
   - faz o Grão-Mestre responder **no idioma do usuário**.
3. O M6 não espera o M6+: até ele, o painel mostra o texto do motor como vem quando não há código.

## Consequências

- Todo texto novo do painel nasce nos dois catálogos; chave faltando reprova a suíte rápida.
- O M7 só começa com o M6+ auditado.
- **Atualização de 2026-10-28:** no levantamento do M6+ o Dono pediu critérios testáveis para a auditoria, e o M6+ ganhou documentos de funcionalidade, ao contrário do M4+ e do M5+: [mensagens por código](../plataforma/mensagens-por-codigo.md) e [idioma por canal](../plataforma/idioma-por-canal.md).
