// Package jsonvalue validates generic JSON values against simple JSON schemas.
package jsonvalue

import (
	"fmt"
	"reflect"
)

// ValidateSchema checks the JSON value shape for the schema types Jira uses in
// field metadata. It deliberately ignores Jira-specific object properties.
func ValidateSchema(value interface{}, schema map[string]interface{}) error {
	if value == nil {
		return nil
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "", "any":
		return nil
	case "string":
		if IsADFDocument(value) {
			return nil
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %s", kind(value))
		}
	case "number":
		if !isNumber(value) {
			return fmt.Errorf("expected number, got %s", kind(value))
		}
	case "integer":
		if !isInteger(value) {
			return fmt.Errorf("expected integer, got %s", kind(value))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %s", kind(value))
		}
	case "array":
		if reflect.ValueOf(value).Kind() != reflect.Slice {
			return fmt.Errorf("expected array, got %s", kind(value))
		}
	case "object":
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("expected object, got %s", kind(value))
		}
	}
	return nil
}

// IsADFDocument reports whether value has the required top-level shape of an
// Atlassian Document Format document. Jira-specific node validation remains
// the responsibility of Jira itself.
func IsADFDocument(value interface{}) bool {
	document, ok := value.(map[string]interface{})
	if !ok || document["type"] != "doc" || document["version"] != float64(1) {
		return false
	}
	content, ok := document["content"].([]interface{})
	return ok && content != nil
}

func isNumber(value interface{}) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}

func isInteger(value interface{}) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func kind(value interface{}) string {
	if value == nil {
		return "null"
	}
	return reflect.TypeOf(value).String()
}
