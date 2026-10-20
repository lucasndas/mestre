//go:build !linux

package leakcheck

func find([]string) []Proc { return nil }
func kill([]Proc)          {}
func alive([]Proc) []Proc  { return nil }
