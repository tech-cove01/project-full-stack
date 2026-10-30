package app

import (
	"errors"
	"reflect"
	"testing"
)

type closeFunc func() error

func (close closeFunc) Close() error { return close() }

func TestDiagnosticListenerStopsBeforePoolsAndFailureStillClosesPools(t *testing.T) {
	var calls []string
	profileTimeout := errors.New("controlled profiler timeout")
	owned := applicationResources{
		diagnostics: closeFunc(func() error { calls = append(calls, "diagnostics"); return profileTimeout }),
		pools:       closeFunc(func() error { calls = append(calls, "pools"); return nil }),
	}
	if err := owned.Close(); !errors.Is(err, profileTimeout) {
		t.Fatal("diagnostic shutdown error was lost")
	}
	if !reflect.DeepEqual(calls, []string{"diagnostics", "pools"}) {
		t.Fatal("diagnostic request lifetime overlapped released dependency pools")
	}
}
