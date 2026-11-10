package web

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSanitizeJSONValueReplacesNaNAndInf(t *testing.T) {
	payload := map[string]interface{}{
		"valid":      12.5,
		"nanValue":   math.NaN(),
		"infValue":   math.Inf(1),
		"nested": map[string]interface{}{
			"negInf": math.Inf(-1),
			"list":   []interface{}{math.NaN(), 3.5},
		},
	}

	sanitized := sanitizeJSONValue(payload).(map[string]interface{})
	if _, err := json.Marshal(sanitized); err != nil {
		t.Fatalf("sanitized payload should marshal cleanly: %v", err)
	}
	if sanitized["nanValue"] != 0.0 {
		t.Fatalf("nanValue should be sanitized to 0.0, got %#v", sanitized["nanValue"])
	}
	if sanitized["infValue"] != 0.0 {
		t.Fatalf("infValue should be sanitized to 0.0, got %#v", sanitized["infValue"])
	}
	nested := sanitized["nested"].(map[string]interface{})
	if nested["negInf"] != 0.0 {
		t.Fatalf("negInf should be sanitized to 0.0, got %#v", nested["negInf"])
	}
	list := nested["list"].([]interface{})
	if list[0] != 0.0 {
		t.Fatalf("list[0] should be sanitized to 0.0, got %#v", list[0])
	}
	if list[1] != 3.5 {
		t.Fatalf("list[1] should remain unchanged, got %#v", list[1])
	}
}
