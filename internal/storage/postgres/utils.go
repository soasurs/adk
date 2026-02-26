package postgres

import (
	"encoding/json"
)

func marshalJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func unmarshalJSON(data []byte) map[string]any {
	result := make(map[string]any)
	if len(data) == 0 {
		return result
	}
	json.Unmarshal(data, &result)
	return result
}
