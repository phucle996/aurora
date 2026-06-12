package codec

import (
	"encoding"
	"encoding/json"
	"reflect"

	"google.golang.org/protobuf/proto"
)

// MarshalData tự động mã hóa dữ liệu động sang byte slice:
// 1. Nếu là proto.Message, sử dụng Protobuf để đạt hiệu năng nhị phân tối đa.
// 2. Nếu là encoding.BinaryMarshaler, sử dụng MarshalBinary.
// 3. Các trường hợp khác fallback về json.Marshal.
func MarshalData(data interface{}) ([]byte, error) {
	if data == nil {
		return nil, nil
	}

	// Kiểm tra trực tiếp proto.Message
	if pm, ok := data.(proto.Message); ok {
		return proto.Marshal(pm)
	}

	// Giải quyết con trỏ trỏ đến con trỏ (**T)
	val := reflect.ValueOf(data)
	if val.Kind() == reflect.Ptr && !val.IsNil() {
		elem := val.Elem()
		if elem.Kind() == reflect.Ptr && !elem.IsNil() {
			if pm, ok := elem.Interface().(proto.Message); ok {
				return proto.Marshal(pm)
			}
		}
	}

	// Kiểm tra nếu là encoding.BinaryMarshaler
	if bm, ok := data.(encoding.BinaryMarshaler); ok {
		return bm.MarshalBinary()
	}

	// Fallback về JSON
	return json.Marshal(data)
}

// UnmarshalData tự động giải mã byte slice vào target động:
// 1. Nếu target hoặc kiểu thực thể bên dưới target implements proto.Message, sử dụng Protobuf.
// 2. Nếu target implements encoding.BinaryUnmarshaler, sử dụng UnmarshalBinary.
// 3. Các trường hợp khác fallback về json.Unmarshal.
func UnmarshalData(payload []byte, target interface{}) error {
	if len(payload) == 0 {
		return nil
	}

	// Kiểm tra trực tiếp proto.Message
	if pm, ok := target.(proto.Message); ok {
		return proto.Unmarshal(payload, pm)
	}

	// Giải quyết trường hợp target là pointer-to-pointer (**T) phục vụ Factory trong Registry
	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Ptr && !val.IsNil() {
		elem := val.Elem()
		if elem.Kind() == reflect.Ptr {
			// Khởi tạo con trỏ rỗng nếu nó là nil
			if elem.IsNil() {
				newObj := reflect.New(elem.Type().Elem())
				elem.Set(newObj)
			}
			if pm, ok := elem.Interface().(proto.Message); ok {
				return proto.Unmarshal(payload, pm)
			}
		}
	}

	// Kiểm tra nếu là encoding.BinaryUnmarshaler
	if bu, ok := target.(encoding.BinaryUnmarshaler); ok {
		return bu.UnmarshalBinary(payload)
	}

	// Fallback về JSON
	return json.Unmarshal(payload, target)
}
