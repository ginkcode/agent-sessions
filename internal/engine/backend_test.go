package engine

import (
	"context"
	"reflect"
	"testing"
)

func TestBackend_InterfaceContract(t *testing.T) {
	backendType := reflect.TypeOf((*Backend)(nil)).Elem()
	engineType := reflect.TypeOf((*Engine)(nil))

	if !engineType.Implements(backendType) {
		t.Fatalf("Engine does not implement Backend interface")
	}

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errType := reflect.TypeOf((*error)(nil)).Elem()

	for i := 0; i < backendType.NumMethod(); i++ {
		method := backendType.Method(i)
		mType := method.Type

		// Every Backend method must take at least context.Context as first param
		if mType.NumIn() < 1 {
			t.Errorf("method %s: has no parameters, expected at least ctx context.Context", method.Name)
			continue
		}
		if mType.In(0) != ctxType {
			t.Errorf("method %s: first parameter is %v, want context.Context", method.Name, mType.In(0))
		}

		// Every Backend method must return at least error as its final return value
		if mType.NumOut() < 1 {
			t.Errorf("method %s: has no return values, expected error", method.Name)
			continue
		}
		lastOut := mType.Out(mType.NumOut() - 1)
		if lastOut != errType {
			t.Errorf("method %s: final return value is %v, want error", method.Name, lastOut)
		}
	}
}
