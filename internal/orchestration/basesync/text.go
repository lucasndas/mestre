package basesync

import (
	"fmt"

	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// instructions is what the engine tells the Dev of the step sync
// (docs/orquestracao/sincronia-com-a-base.md#a-etapa).
func instructions(t tables.Table) string {
	return fmt.Sprintf(`A branch principal (origin/%[1]s) andou depois que a mesa começou, e a branch da mesa (%[2]s) ainda não a tem. Traga-a para o seu worktree:
1. Integre a base com merge: `+"`git merge origin/%[1]s`"+`. Nunca use rebase nem push forçado.
2. Resolva cada conflito mantendo a intenção dos dois lados. Conflito é trabalho seu, não do usuário; só pergunte (ao Mestre da mesa) se os dois lados do pedido não cabem juntos.
3. Rode a verificação do workspace (build e testes do projeto) e corrija o que quebrar: uma mescla sem marcadores de conflito pode não compilar.
4. Commite e só então dê `+"`mestre done`"+`. O motor roda a verificação por conta própria antes de concluir a etapa; você não faz git push, o envio é do motor.`, t.BaseBranch, t.Branch)
}

func createdText(t tables.Table, quest, dev string) string {
	return fmt.Sprintf("A branch principal (%s) andou depois da abertura da mesa %s. O motor criou a missão %s com a etapa sync para %s, que integra a base e prova que a verificação passa. Nada a decidir agora; o `mestre pr update` vem quando a etapa concluir.",
		t.BaseBranch, t.ID, quest, dev)
}

func noDevText(t tables.Table) string {
	return fmt.Sprintf("A branch principal (%s) andou depois da abertura da mesa %s e nenhum personagem vivo da mesa fez a última etapa que mudou código. Escolha quem integra a base: crie a missão `sync` com uma etapa para esse personagem (`mestre quest create`) e rode-a; as instruções são integrar `origin/%s` na branch %s com merge (nunca rebase nem push forçado), resolver os conflitos, rodar a verificação da campanha e dar `mestre done`.",
		t.BaseBranch, t.ID, t.BaseBranch, t.Branch)
}
