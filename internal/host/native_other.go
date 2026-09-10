//go:build !darwin

package host

// Every platform but the one that needs a native reader. See
// native_darwin.go for why exactly one does.
//
// These exist so the composition root compiles everywhere while calling for
// a reading only Darwin can take. Nothing reaches them: the call is made
// only when the target is this machine and the probe found no /proc, and a
// machine with neither is not one this runs on.

import (
	"context"
	"errors"
	"runtime"
)

// ErrNoNativeReader is a machine this cannot read directly. It is not a
// failure anybody sees — on this platform the readings come off /proc, which
// is where the probe will have sent them.
var ErrNoNativeReader = errors.New(
	"no native machine reader on " + runtime.GOOS + "; the readings come from /proc")

func Sample(context.Context) (Metrics, error) {
	return Metrics{}, ErrNoNativeReader
}

func SampleProcesses(context.Context) (ProcessSample, error) {
	return ProcessSample{}, ErrNoNativeReader
}
