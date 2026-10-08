package runtime

import (
	"encoding/json"
	"reflect"
)

// Go-defined scalar types have the same LIP meaning as their underlying kind.
// json.Number is a numeric representation, despite its underlying string kind.
func scalarString(value Value) (string, bool) {
	if text, ok := value.(string); ok {
		return text, true
	}
	if _, number := value.(json.Number); number {
		return "", false
	}
	if v := reflect.ValueOf(value); v.IsValid() && v.Kind() == reflect.String {
		return v.String(), true
	}
	return "", false
}
