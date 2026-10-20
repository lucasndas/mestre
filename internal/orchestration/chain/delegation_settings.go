package chain

import (
	"bytes"
	"errors"
	"io"
	"time"

	"go.yaml.in/yaml/v3"
)

// MaxDelegationValidity is the longest validity of a delegation, whatever the
// file says (docs/aprovacoes/delegacao.md).
const MaxDelegationValidity = 30 * 24 * time.Hour

type delegationYAML struct {
	ValidadePadrao string `yaml:"validade_padrao"`
	AvisoAntes     string `yaml:"aviso_antes"`
}

// ParseDelegation reads the `delegacao` section: the validity a delegation gets
// when nobody gives one (up to 30 days) and how long before its end its author is
// reminded (less than the validity). Absent keys are the default (30 and 3 days);
// anything else makes the whole file invalid.
func ParseDelegation(data []byte) (validity, reminder time.Duration, err error) {
	validity, reminder = MaxDelegationValidity, 3*24*time.Hour
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var y delegationYAML
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, &SectionError{Item: "delegacao", Reason: err.Error()}
	}
	if y.ValidadePadrao != "" {
		d, err := ParseDuration(y.ValidadePadrao)
		if err != nil || d <= 0 || d > MaxDelegationValidity {
			return 0, 0, &SectionError{Item: "delegacao.validade_padrao", Reason: "precisa de uma duração positiva de até 30d"}
		}
		validity = d
	}
	if y.AvisoAntes != "" {
		d, err := ParseDuration(y.AvisoAntes)
		if err != nil || d <= 0 {
			return 0, 0, &SectionError{Item: "delegacao.aviso_antes", Reason: "precisa de uma duração positiva como 3d"}
		}
		reminder = d
	}
	if reminder >= validity {
		return 0, 0, &SectionError{Item: "delegacao.aviso_antes", Reason: "precisa ser menor que a validade padrão"}
	}
	return validity, reminder, nil
}
