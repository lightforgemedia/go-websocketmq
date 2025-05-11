// ergosockets/common.go
package ergosockets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"
)

// Cached error type for reflection.
var ErrType = reflect.TypeOf((*error)(nil)).Elem()

// GenerateID creates a new random hex string ID.
func GenerateID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%x", reflect.ValueOf(TimeNow()).Int()) // TimeNow from time.go
	}
	return hex.EncodeToString(bytes)
}

// HandlerWrapper stores reflection info for invoking user-provided handlers.
type HandlerWrapper struct {
	HandlerFunc reflect.Value // The user's handler function
	ReqType     reflect.Type  // Type of the request payload struct (for request handlers)
	RespType    reflect.Type  // Type of the response payload struct (for request handlers that return one)
	MsgType     reflect.Type  // Type of the message payload (for subscription handlers)
}

// NewHandlerWrapper inspects the handler function and extracts type information.
// Supported signatures:
// Server OnRequest: func(ClientHandle, ReqStruct) (RespStruct, error)
// Server OnRequest: func(ClientHandle, ReqStruct) error
// Client Subscribe: func(MsgStruct) error
// Client OnRequest: func(ReqStruct) (RespStruct, error)
// Client OnRequest: func(ReqStruct) error
func NewHandlerWrapper(handlerFunc interface{}) (*HandlerWrapper, error) {
	fv := reflect.ValueOf(handlerFunc)
	ft := fv.Type()

	if ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function, got %T", handlerFunc)
	}

	hw := &HandlerWrapper{HandlerFunc: fv}
	numIn := ft.NumIn()
	numOut := ft.NumOut()

	// Check common error return (last output arg must be error)
	if numOut == 0 || !ft.Out(numOut-1).Implements(ErrType) {
		return nil, fmt.Errorf("handler must return an error as its last argument, signature: %s", ft.String())
	}

	// Client Subscribe: func(MsgStruct) error
	if numIn == 1 && numOut == 1 {
		hw.MsgType = ft.In(0)
		// Ensure MsgStruct is not an interface or pointer to interface, etc.
		// For simplicity, we assume it's a concrete type or pointer to struct.
		if hw.MsgType.Kind() == reflect.Interface || (hw.MsgType.Kind() == reflect.Ptr && hw.MsgType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("subscription handler message type cannot be an interface: %s", ft.String())
		}
		return hw, nil
	}

	// Client OnRequest: func(ReqStruct) (RespStruct, error) or func(ReqStruct) error
	if numIn == 1 && (numOut == 1 || numOut == 2) {
		hw.ReqType = ft.In(0)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("client OnRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("client OnRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	// Server OnRequest: func(ClientHandle, ReqStruct) (RespStruct, error) or func(ClientHandle, ReqStruct) error
	if numIn == 2 && (numOut == 1 || numOut == 2) {
		// First arg should be ClientHandle (interface) - we don't strictly check its type here
		// as it's passed by the broker itself.
		hw.ReqType = ft.In(1)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("server OnRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ClientHandle, ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("server OnRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	return nil, fmt.Errorf("unsupported handler signature: %s. Expected e.g., func(ch ClientHandle, req ReqT) (RespT, error), func(msg MsgT) error, or func(req ReqT) (RespT, error)", ft.String())
}

// TimeNow is a wrapper for time.Now, useful for testing if time needs to be mocked.
// For this implementation, we'll use the real time.Now().
var TimeNow = time.Now
