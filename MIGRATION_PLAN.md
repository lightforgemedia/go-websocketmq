# Migration Plan: Remove Functional Options

## Objective
Remove all functional options code and standardize on the Options pattern exclusively to eliminate confusion.

## Items to Remove
1. **Functional Option Functions**:
   - `WithLogger()`
   - `WithAcceptOptions()`
   - `WithClientSendBuffer()`
   - `WithPingInterval()`
   - `WithWriteTimeout()`
   - `WithReadTimeout()`
   - `WithServerRequestTimeout()`
   - `Option` type definition

2. **Old Constructor**:
   - `broker.New()` function

## Files to Update
1. **Callers of broker.New()**:
   - `pkg/hotreload/hotreload_test.go`
   - `cmd/server/main.go`
   - `cmd/hotreload_and_debug/main.go`
   - `pkg/hotreload/hotreload_simple_test.go`
   - `pkg/testutil/broker.go`

2. **Test Files Using Functional Options**:
   - Any test that uses `broker.With*()` functions

3. **Documentation**:
   - `docs/options-and-generics.md` - Remove references to mixing patterns
   - Any code comments referring to functional options

## Migration Steps
1. Update all callers to use `broker.NewWithOptions()`
2. Remove functional option functions
3. Remove `broker.New()` function
4. Update documentation
5. Verify no remaining references

## Example Migration

**Before**:
```go
b, err := broker.New(
    broker.WithLogger(logger),
    broker.WithPingInterval(15*time.Second),
    broker.WithClientSendBuffer(32),
)
```

**After**:
```go
opts := broker.DefaultOptions()
opts.Logger = logger
opts.PingInterval = 15 * time.Second
opts.ClientSendBuffer = 32

b, err := broker.NewWithOptions(opts)
```