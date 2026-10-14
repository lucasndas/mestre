package hook_test

import "errors"

func mkfifo(string) error { return errors.New("no FIFO on Windows") }
