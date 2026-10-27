# Glossário em inglês

> **Assunto:** versão em inglês dos termos, da ajuda curta e do "no mundo real" do glossário, para o painel em inglês · **Componente:** Projeto · **Atualizado:** 2026-10-26

Este documento é lido por máquina, como o [glossário](glossario.md): cada linha é indexada pelo termo em português, escrito exatamente como lá, e traz o nome, a ajuda curta e o "no mundo real" em inglês. O `glossario.md` é o dono do significado; aqui só há a tradução, na mesma ordem. Os nomes em inglês citados nas ajudas são os da coluna `Term`.

## Terms

| Termo | Term | Short help | In the real world |
|---|---|---|---|
| Mestre | Mestre | The whole system: the orchestrator that starts, connects and tracks the AI agents. | the whole program that coordinates your AI assistants |
| Motor | Engine | The invisible, always-on part of Mestre that does the work; closing the browser does not stop it. | the program that keeps running in the background and does the work |
| Guilda | Guild | The group of all machines with Mestre installed, working together. | the group of computers that work together |
| Cidadela | Citadel | The machine in charge of the Guild: keeps the state, serves the panel and runs the Grand Master. Only one at a time. | the main server of your AI team |
| Torre | Tower | Each other machine in the Guild: runs characters, keeps a copy of the state and watches the Citadel; it can take over. | a backup machine, ready to take over from the main one |
| Ficha da máquina | Machine sheet | What a machine has: system, screen, logged-in providers, browser, CPU, memory and GPU. | the spec sheet of a computer: what it has and what it can do |
| Época | Epoch | The number that says which Citadel counts; it goes up on every switch, and orders from an old epoch are ignored. | a turn counter: the highest number is the one in charge |
| Cidadela suspensa | Suspended Citadel | A Citadel that saw a newer epoch: it does not act outwardly and sends the panel to the current Citadel. | an old main server that lost command and only redirects |
| Máquina excluída | Excluded machine | A machine the Guildmaster left without secrets: it does not keep the vault and never becomes the Citadel. | a computer that holds no passwords and never becomes the main one |
| Guardião | Guardian | A small service on each machine that watches its engine and repairs it when it fails. | a watchman on each computer that restarts the program when it breaks |
| Página reserva | Fallback page | A minimal page served by the Guardian to see the status and approve repairs when the Citadel is down. | an emergency page for when the main server is down |
| Modo máquina única | Single-machine mode | A Guild with just one machine: if it goes down entirely, nobody outside notices. | using just one computer, with no backup team |
| Torre condutora | Leading Tower | The Tower that leads the Citadel's recovery, handles Telegram while it is down and proposes who takes over. | the backup computer chosen to coordinate the fix |
| Modo reduzido | Reduced mode | Telegram with the Citadel down: the Guardian takes the bot and handles only /status, repair approvals and promotion. | a limited mode with few functions, used while the main server is down |
| Campanha | Campaign | A GitHub repository looked after by one Grand Master; tables are the play sessions inside it. | a GitHub project with one AI assistant in charge |
| Grão-Mestre | Grand Master | The chief agent of the campaign: the only one who talks to users and handles GitHub. | the AI assistant that manages the project and talks to you |
| Mesa | Table | A request in progress: its own branch tied to an epic, with its own Master and canvas. | one of your requests in progress, with its own workspace |
| Épico | Epic | The GitHub issue that represents a table. | a tracking item on GitHub for a large request |
| Espelho | Mirror | A copy of the repository on each machine (the Citadel's is the main one), where table branches come from. | a local copy of the project on each computer |
| Mestre da mesa | Table Master | The agent that coordinates a table's team and reports to the Grand Master. | the AI assistant that coordinates the team on one request |
| Triagem | Triage | The Grand Master sorts the request, writes criteria and asks up to 3 questions before routing it or opening a table. | the first conversation where the request is understood and clarified |
| Aceite | Acceptance | The Table Master checks each criterion against the evidence before the PR; with none, the mission goes back to the team. | the final check of the work against what was asked |
| Mensagem de progresso | Progress message | A campaign summary by the engine: says what needs no one and gives one owned line per pending item. | an automatic summary of what is going on and what needs you |
| Personagem | Character | An AI agent (or terminal) working on a table, shown on the canvas. | an AI assistant carrying out a task |
| Terminal simples | Plain terminal | A terminal with no agent (a shell), created from a base. | a regular command window, with no AI assistant |
| Fixado | Pinned | A character a user pinned: it is never shut down automatically. | an assistant you locked in place so it is never switched off by itself |
| Efêmero | Ephemeral | A character shut down on its own when the task or mission that created it ends. | an assistant that goes away when its task is done |
| Persistente | Persistent | A character that stays alive between tasks until it has been idle too long. | an assistant that stays on between tasks until it sits idle too long |
| Renascido | Reborn | A character that could not resume its session and started over with a summary of the work. | an assistant that lost its earlier chat and restarted from a summary |
| Terminal | Terminal | Where each agent runs on the machine; it stays alive when the engine restarts or updates. | the command window where each assistant works on the computer |
| Ponto seguro | Safe point | A moment when a character can be shut down without losing work: idle and with the task handed in. | the moment when an assistant can be switched off without losing anything |
| Companhia | Party | The team of characters connected to this table. | the team of AI assistants working on one request |
| Base | Base | The setup of how a terminal starts: command, folder and which machines it can run on. | the template for how to open a command window |
| Ficha | Sheet | A ready-made kind of character: uses a base and sets agent, class, account and limits. | a ready-made assistant template with a defined job and limits |
| Perfil de conta | Account profile | Which provider subscription (Claude, Codex, Antigravity) a character uses on that machine. | the AI subscription the assistant uses to work |
| App do GitHub | GitHub App | Mestre's robot on GitHub: agent actions show under an App's name, and you can cut its access anytime. | a robot user on GitHub that Mestre uses to open PRs and comment |
| Operador do PC | PC Operator | An agent that uses your Windows PC and account, only with Guildmaster approval, briefly, with an on-screen notice. | an assistant that uses your Windows computer, with your OK and briefly |
| Classe | Class | A character's role (e.g. Dev, QA): its own instructions, permissions, hooks and skills. | the assistant's job on the team, such as programmer or tester |
| Classe sugerida | Suggested class | A development-team class that ships turned off; an Administrator enables the ones they want. | a ready-made programming-team job that starts switched off |
| Ficha leve | Light sheet | Of the two sheets of each suggested class, the cheaper-model one; recipes use it. | the cheaper version of an assistant, for simple tasks |
| Ficha pesada | Heavy sheet | Of the two sheets of each suggested class, the stronger-model one; the Table Master uses it for hard steps. | the more capable version of an assistant, for hard tasks |
| Skill de apoio | Support skill | An engineering skill a class brings with an on/off switch (TDD, code review); it grants no permission. | a good programming habit the assistant can follow, with no extra power |
| Gateway do Mestre | Mestre Gateway | The agent's only MCP server: lists just the tools of servers the class turns on; the engine judges each call. | the single door through which the assistant uses outside tools |
| Catálogo MCP | MCP catalog | List of MCP servers (like browser and docs) the campaign accepts; only an Administrator or above adds to it. | the list of outside tools the project allows |
| Servidor de controle | Control server | An MCP server that controls the computer or browser; only the Guildmaster adds it and only the PC Operator uses it. | a tool that can take control of your computer or browser |
| Teste de classe | Class test | Runs the class's cases with the real provider to check it does what it says; uses quota (Test and Test again buttons). | a trial run to see if the assistant does what it promises |
| Teto da campanha | Campaign ceiling | The maximum permissions and cost any class in this campaign may have. | the upper limit on what any assistant in the project may do and spend |
| Pergaminho | Scroll | A Markdown note on the canvas that connected agents read and write. | a text note shared between you and the assistants |
| Tomo | Tome | A stack of grouped scrolls. | a folder of grouped notes |
| Grimório | Grimoire | A saved team from the canvas, with flow and notes, to restore on another table. | a saved team you can reuse on another request |
| Receita | Recipe | A ready-made mission per request type (bug, feature, tweak, investigation) or UI refinement; used with --recipe. | a ready-made script for a type of request, such as fixing a bug |
| Calibração | Calibration | Tuning a class from what a mission showed: the retro step suggests, the Grand Master agrees, an Administrator approves. | a lesson learned, used to improve the assistant's job |
| Ritual | Ritual | A scheduled routine that sends a command or request to an agent at a set time. | an automatic task that runs at a set time |
| Vigia | Watch | A daily ritual of the suggested classes, off by default: looks for problems and logs them as candidate issues. | a daily patrol that looks for problems and notes what it finds |
| Missão | Mission | A sequence of steps where one agent hands the work to the next (A → B → C). | a list of steps where one assistant hands the work to the next |
| Etapa | Step | One step of a mission, done by a character. | each step on that list, done by one assistant |
| Prova | Proof | Evidence that a mission criterion was met: stated by the agent or checked by the engine. | the evidence that something asked for was really done |
| Devolução | Send-back | A step returns work to an earlier one, with the reason; rounds are capped and a repeated failure pauses the mission. | sending the work back to be redone, with the reason |
| Sincronia com a base | Base sync | The sync step: Dev merges the main branch into the table's, resolves conflicts and only finishes with green checks. | bringing the work up to date with the project's latest changes |
| Caixa de entrada | Inbox | A character's message queue, delivered only when it becomes free. | messages waiting until the assistant finishes what it is doing |
| Espera | Wait | When a character or table is waiting on something: another agent, an approval, a time or another table. | being on hold until something happens so work can continue |
| Aguardando cota | Awaiting quota | Stopped until quota returns: the character hit the subscription limit, or the table will not start a step near it. | paused because the subscription's usage limit has run out for now |
| Limiar de início | Start threshold | Plan-window usage (default 95%) from which the engine starts no new step on that provider and account. | the usage level at which no new work is started |
| Monitoramento reduzido | Reduced monitoring | This character's provider reports fewer events to Mestre; the engine compensates in other ways. | the assistant reports less, so the system checks on its own |
| Sessão zero | Session zero | The automatic setup done when a campaign, table or character is born. | the automatic setup done when something new is created |
| Decreto | Decree | An order from an authorized user that outranks the rules: a one-off exception or a standing rule. | an order from an authorized person that overrides the rules |
| Exceção pontual | One-off exception | A decree that allows one exact action once; every approval is one. | a permission given once, for one specific action |
| Regra permanente | Standing rule | A decree that applies from now on, until revoked or expired. | a new rule that stays until cancelled or expired |
| Aprovação | Approval | A request to allow an action; it climbs from the requester's superior up to the Grand Master and people. | the request for permission to do something |
| Pedido que bloqueia | Blocking request | A message asking the user for something because the table cannot go on without it: reason, evidence, action, owner. | a message that stops the work until you answer |
| Aviso de bloqueio | Block notice | A blocking request with no policy type: only says what is missing and the steps the user can take, no decision. | a heads-up about what is missing, with no decision to make |
| Cadeia de aprovação | Approval chain | A request's path: from the requester's superior to the Grand Master and people, each deciding up to a risk. | the order of people who approve a request, one after another |
| Superior | Superior | Whoever created the character; the first to decide its approval requests. | the direct manager who approves the assistant's requests first |
| Risco | Risk | How much a request could break: low, medium, high or fixed. The system decides it and it says who may approve. | how badly an action could go wrong |
| Regra temporária de mesa | Temporary table rule | A short permission on one table, given by an agent when approving; it never beats a person's rule. | a short permission for one request, given by an assistant |
| Pausa de autonomia | Autonomy pause | A per-table brake: the Table Master stops deciding medium risk, which goes to the Grand Master; low stays with it. | a brake that sends medium-sized decisions up to the boss |
| Disjuntor | Circuit breaker | Pauses a character after 3 denials in a row (default) of its requests and alerts people; an Administrator resumes it. | a safety switch that pauses the assistant after several refusals in a row |
| Entrega útil | Useful delivery | Work that landed: a new commit on the table's branch, a PR opened or updated, or a mission step completed. | work that is really finished and visible in the project |
| Mesa que gira | Spinning table | A table running 3 h (default) with no useful delivery, pause, quota or other table: alerts the Grand Master, approvers. | a request that stays busy for hours without delivering anything |
| Delegação | Delegation | A person's authorization for the Grand Master to decide some requests above its limit, for up to 30 days. | a permission you give the boss to decide for you for a limited time |
| Senhor da Guilda | Guildmaster | The highest level: the person who installed Mestre and decides what no one else can, like users, machines and the vault. | the general administrator of the system, who is in charge of everything |
| Nível | Level | What a user can do in Mestre: Guildmaster, Administrator, Approver or Observer. | what each person is allowed to do in the system |
| Cofre | Vault | An encrypted place where secrets are kept; only the Citadel opens it, and agents never read it. | a protected digital safe where passwords are kept |
| Impressão | Fingerprint | A code (hash) of a request, failure, problem, secret or the CA, used to tell whether it is the same or changed. | a code that identifies something and shows if it changed |
| Em criação | Being created | This class's GitHub App has not been created and installed yet; actions go out under the Mestre App. | an assistant's account that is still being set up |
| Leitura | Reading | The mode where you only watch the terminal: nothing you do reaches the agent. | looking at someone's screen without touching their keyboard |
| Mensagem | Message | The mode of typing in a box: the text goes to the agent when it is free, or waits in the queue. | writing a note the assistant reads when it can |
| Teclado direto | Direct keyboard | The mode where every key goes to the agent at once, for provider menus and questions. | using the assistant's keyboard as if you were sitting at it |
