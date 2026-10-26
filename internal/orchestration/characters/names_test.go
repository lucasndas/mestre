package characters_test

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// CA-PERS-11: the senders of the engine and the reserved names cannot be taken,
// not even with a letter drawn empty after them, which the header would drop and
// so show the reserved name exactly.
func TestTheNamesOfTheEngineAndTheReservedOnesCannotBeTakenEvenWithAFiller(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Motor", "sistema", "SISTEMA", "Grão-Mestre", "Grão-Mestre\U00003164", "Mestre\U00003164", "Me\U0000115Fstre", "Mestre\U0000FFA0"} {
		if err := characters.ValidName(name); !errors.Is(err, characters.ErrInvalidName) {
			t.Errorf("%q: err = %v, want an invalid name", name, err)
		}
	}
	for _, name := range []string{"Motoristas", "Sistema Novo", "Dev 2"} {
		if err := characters.ValidName(name); err != nil {
			t.Errorf("%q: err = %v, want it accepted", name, err)
		}
	}
}
