//go:build !linux && !windows

package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
)

func platformRun(context.Context, io.Writer, options) error {
	return fmt.Errorf("the real-account tests run on Linux and Windows, not on %s", runtime.GOOS)
}
