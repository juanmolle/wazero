package interpreter

import (
	"errors"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/internal/testing/require"
	"github.com/tetratelabs/wazero/internal/wasm"
)

// A lowering failure must be reported on every call, not only the first one.
func TestEnsureLowered_ReRaisesErrorOnEveryCall(t *testing.T) {
	module := &wasm.Module{
		TypeSection:     []wasm.FunctionType{{}},
		FunctionSection: []wasm.Index{0},
		// Out-of-range local index: invalid, so lowering fails. Real modules
		// are validated before this point; this one deliberately is not.
		CodeSection: []wasm.Code{{Body: []byte{wasm.OpcodeLocalGet, 5, wasm.OpcodeEnd}}},
	}
	e := NewEngine(nil, 0, nil).(*engine)
	require.NoError(t, e.CompileModule(nil, module, nil, false))
	funcs, ok := e.getCompiledFunctions(module)
	require.True(t, ok)

	lower := func() (err error) {
		defer func() {
			if v := recover(); v != nil {
				err = v.(error)
			}
		}()
		funcs[0].ensureLowered()
		return nil
	}
	first := lower()
	require.Error(t, first)
	second := lower()
	require.Error(t, second)
	require.True(t, errors.Is(second, first) || second.Error() == first.Error())
	require.Nil(t, funcs[0].body)
}

// Lowering uses the engine-wide label cache, so first calls into functions of
// different modules compiled on the same engine must not lower concurrently.
func TestEnsureLowered_ConcurrentAcrossModules(t *testing.T) {
	const functions = 50
	// block; br 0; end: lowering emits labels, exercising the label cache.
	body := []byte{wasm.OpcodeBlock, 0x40, wasm.OpcodeBr, 0, wasm.OpcodeEnd, wasm.OpcodeEnd}
	newModule := func(id byte) *wasm.Module {
		// The engine keys compiled modules by ID.
		module := &wasm.Module{ID: wasm.ModuleID{id}, TypeSection: []wasm.FunctionType{{}}}
		for range make([]struct{}, functions) {
			module.FunctionSection = append(module.FunctionSection, 0)
			module.CodeSection = append(module.CodeSection, wasm.Code{Body: body})
		}
		return module
	}
	e := NewEngine(nil, 0, nil).(*engine)
	var all [][]compiledFunction
	for id := byte(0); id < 4; id++ {
		module := newModule(id)
		require.NoError(t, e.CompileModule(nil, module, nil, false))
		funcs, ok := e.getCompiledFunctions(module)
		require.True(t, ok)
		all = append(all, funcs)
	}

	var wg sync.WaitGroup
	for _, funcs := range all {
		wg.Add(1)
		go func(funcs []compiledFunction) {
			defer wg.Done()
			for i := range funcs {
				funcs[i].ensureLowered()
			}
		}(funcs)
	}
	wg.Wait()
	for _, funcs := range all {
		for i := range funcs {
			require.NotEqual(t, 0, len(funcs[i].body))
		}
	}
}
