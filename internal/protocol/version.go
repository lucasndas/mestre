package protocol

import (
	"fmt"
	"slices"
)

// The protocol of the motors starts at 1. Every machine accepts the current
// version and the previous one (docs/conectividade/protocolo.md#abertura-versão-e-capacidades);
// there is no previous one yet. The Guardian has a version of its own.
const (
	Version    = 1
	MinVersion = 1
)

// Range is a range of protocol versions, both ends included.
type Range struct {
	Min int `json:"min" proto:"req"`
	Max int `json:"max" proto:"req"`
}

// DefaultRange is the range this binary speaks.
func DefaultRange() Range { return Range{Min: MinVersion, Max: Version} }

// Valid says whether the range is well formed: versions start at 1.
func (r Range) Valid() bool { return r.Min >= 1 && r.Max >= r.Min }

func (r Range) String() string { return fmt.Sprintf("%d-%d", r.Min, r.Max) }

// Negotiate picks the highest version both ranges contain.
func Negotiate(a, b Range) (int, bool) {
	if !a.Valid() || !b.Valid() {
		return 0, false
	}
	v := min(a.Max, b.Max)
	if v < max(a.Min, b.Min) {
		return 0, false
	}
	return v, true
}

// Capabilities is a set of optional features a machine announces in its
// opening. A feature that both sides announced is on; an unknown one is
// ignored, so announcing a new one never needs a new version.
type Capabilities []string

// Intersect returns the capabilities in both sets, sorted.
func (c Capabilities) Intersect(o Capabilities) Capabilities {
	var out Capabilities
	for _, x := range c {
		if slices.Contains(o, x) && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// Has says whether the set contains name.
func (c Capabilities) Has(name string) bool { return slices.Contains(c, name) }
