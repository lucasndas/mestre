//go:build !linux && !windows

package safefile

import (
	"errors"
	"os"
)

// The Mestre runs on Linux and Windows only.

func open(string, string) (*os.File, Info, error) { return nil, Info{}, errors.ErrUnsupported }

func stat(string, string) (Info, error) { return Info{}, errors.ErrUnsupported }

func write(string, string, []byte, Options) error { return errors.ErrUnsupported }

func mkdirAll(string) error { return errors.ErrUnsupported }

func remove(string, string, func(Info) error) error { return errors.ErrUnsupported }

func removeEntry(string, string) error { return errors.ErrUnsupported }

func removeDir(string, string) error { return errors.ErrUnsupported }
