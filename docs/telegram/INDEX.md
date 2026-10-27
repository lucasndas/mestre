# Telegram

> **Assunto:** índice do canal Telegram com os usuários autorizados · **Componente:** Motor · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [bot.md](bot.md) | long polling, tipos de mensagem aceitos, comandos rápidos, mensagens de saída (motor e Grão-Mestre), botões, ritmo de envio, explicação de termos na primeira vez | implementar o bot ou mudar o que ele responde |
| [roteamento.md](roteamento.md) | para qual campanha vai cada mensagem, resposta encadeada, campanha ativa, cabeçalho das mensagens, mensagens "Sistema" | mexer em como as mensagens chegam ao Grão-Mestre |
| [usuarios-e-niveis.md](usuarios-e-niveis.md) | fonte única dos níveis (Senhor da Guilda, Administrador, Aprovador, Observador) e do que cada um pode, cadastro, pareamento por código, PIN, suspensão, transferência de Senhor da Guilda | qualquer checagem de permissão de usuário |
| [decretos.md](decretos.md) | exceção pontual e regra permanente, vínculo ao estado, validade, conflito entre níveis, revogação, PIN, registro | criar, aplicar ou revogar decretos |
| [seguranca-do-canal.md](seguranca-do-canal.md) | filtro de entrada (só autorizados, só chat privado), alerta de intrusos, `/travar` e `/destravar`, segredos fora do Telegram | mexer na entrada do bot ou em emergências |
| [anexos-e-transcricao.md](anexos-e-transcricao.md) | anexos recebidos e enviados, limites da Bot API, transcrição local com whisper.cpp, falhas | áudio, imagens e documentos |
