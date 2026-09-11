package jsonvalue

import "testing"

func TestValidateSchema(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		typeName string
		wantErr  bool
	}{
		{name: "string", value: "value", typeName: "string"},
		{name: "wrong string", value: 1, typeName: "string", wantErr: true},
		{name: "number", value: 1.2, typeName: "number"},
		{name: "array", value: []interface{}{"value"}, typeName: "array"},
		{name: "wrong object", value: "value", typeName: "object", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSchema(test.value, map[string]interface{}{"type": test.typeName})
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
