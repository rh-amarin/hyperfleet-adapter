package converter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
)

// ConditionToJQ converts a single condition to a jq expression.
// Maps adapter config operators to jq equivalents.
func ConditionToJQ(field, operator string, value interface{}) string {
	// Ensure field has a dot prefix if not already
	if !strings.HasPrefix(field, ".") {
		field = "." + field
	}

	switch strings.ToLower(operator) {
	case "equals", "eq", "==":
		return fmt.Sprintf("(%s == %s)", field, formatJQValue(value))

	case "notequals", "neq", "!=":
		return fmt.Sprintf("(%s != %s)", field, formatJQValue(value))

	case "in":
		// value should be an array
		values := toStringSlice(value)
		if len(values) == 0 {
			return "false"
		}
		var conditions []string
		for _, v := range values {
			conditions = append(conditions, fmt.Sprintf("(%s == %s)", field, formatJQValue(v)))
		}
		return "(" + strings.Join(conditions, " or ") + ")"

	case "notin":
		values := toStringSlice(value)
		if len(values) == 0 {
			return "true"
		}
		var conditions []string
		for _, v := range values {
			conditions = append(conditions, fmt.Sprintf("(%s != %s)", field, formatJQValue(v)))
		}
		return "(" + strings.Join(conditions, " and ") + ")"

	case "contains":
		return fmt.Sprintf("(%s | contains(%s))", field, formatJQValue(value))

	case "notcontains":
		return fmt.Sprintf("(%s | contains(%s) | not)", field, formatJQValue(value))

	case "startswith":
		return fmt.Sprintf("(%s | startswith(%s))", field, formatJQValue(value))

	case "endswith":
		return fmt.Sprintf("(%s | endswith(%s))", field, formatJQValue(value))

	case "greaterthan", "gt", ">":
		return fmt.Sprintf("(%s > %s)", field, formatJQValue(value))

	case "greaterthanorequals", "gte", ">=":
		return fmt.Sprintf("(%s >= %s)", field, formatJQValue(value))

	case "lessthan", "lt", "<":
		return fmt.Sprintf("(%s < %s)", field, formatJQValue(value))

	case "lessthanorequals", "lte", "<=":
		return fmt.Sprintf("(%s <= %s)", field, formatJQValue(value))

	case "exists":
		return fmt.Sprintf("(%s != null)", field)

	case "notexists":
		return fmt.Sprintf("(%s == null)", field)

	case "empty":
		return fmt.Sprintf("((%s == null) or (%s == \"\") or (%s == []))", field, field, field)

	case "notempty":
		return fmt.Sprintf("((%s != null) and (%s != \"\") and (%s != []))", field, field, field)

	case "matches":
		// Regex match - jq uses test() for regex
		return fmt.Sprintf("(%s | test(%s))", field, formatJQValue(value))

	default:
		// Default to equals
		return fmt.Sprintf("(%s == %s)", field, formatJQValue(value))
	}
}

// ConditionsToJQ combines multiple conditions with AND logic.
func ConditionsToJQ(conditions []config_loader.Condition) string {
	if len(conditions) == 0 {
		return "true"
	}

	var jqConditions []string
	for _, c := range conditions {
		jqConditions = append(jqConditions, ConditionToJQ(c.Field, c.Operator, c.Value))
	}

	return strings.Join(jqConditions, " and ")
}

// CaptureToJQ builds a jq expression to capture fields from an API response.
// Returns an expression that creates an object with the captured values.
func CaptureToJQ(captures []config_loader.CaptureField) string {
	if len(captures) == 0 {
		return "{}"
	}

	var parts []string
	for _, c := range captures {
		if c.Expression != "" {
			// CEL expression - we'll convert to jq approximation
			parts = append(parts, fmt.Sprintf("%s: %s", c.Name, celToJQ(c.Expression)))
		} else if c.Field != "" {
			// Field path extraction
			fieldPath := normalizeFieldPath(c.Field)
			parts = append(parts, fmt.Sprintf("%s: .content%s", c.Name, fieldPath))
		}
	}

	return "{ " + strings.Join(parts, ", ") + " }"
}

// BuildPreconditionExportExpr builds the export expression for a precondition.
// This expression captures the response and evaluates the condition in one pass.
func BuildPreconditionExportExpr(precondName string, captures []config_loader.CaptureField, conditions []config_loader.Condition) string {
	var parts []string

	// Store the full response under the precondition name
	parts = append(parts, fmt.Sprintf("%s: .content", precondName))

	// Add captured fields
	for _, c := range captures {
		if c.Expression != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", c.Name, celToJQ(c.Expression)))
		} else if c.Field != "" {
			fieldPath := normalizeFieldPath(c.Field)
			parts = append(parts, fmt.Sprintf("%s: .content%s", c.Name, fieldPath))
		}
	}

	// Add the _ok flag based on conditions
	okField := toSnakeCase(precondName) + "_ok"
	if len(conditions) > 0 {
		condExpr := buildConditionExprForExport(conditions)
		parts = append(parts, fmt.Sprintf("%s: %s", okField, condExpr))
	} else {
		// No conditions means always ok (API call succeeded)
		parts = append(parts, fmt.Sprintf("%s: true", okField))
	}

	return "${ . + { " + strings.Join(parts, ", ") + " } }"
}

// buildConditionExprForExport builds condition expressions that work in export context.
// In export context, captured fields are accessed from .content
func buildConditionExprForExport(conditions []config_loader.Condition) string {
	if len(conditions) == 0 {
		return "true"
	}

	var jqConditions []string
	for _, c := range conditions {
		field := c.Field
		// In export context, fields come from .content
		if !strings.HasPrefix(field, ".") {
			field = ".content." + field
		} else if !strings.HasPrefix(field, ".content") {
			field = ".content" + field
		}
		jqConditions = append(jqConditions, ConditionToJQ(field, c.Operator, c.Value))
	}

	return "(" + strings.Join(jqConditions, " and ") + ")"
}

// BuildAllMatchedExpr builds the final allMatched expression from all precondition _ok flags.
func BuildAllMatchedExpr(precondNames []string) string {
	if len(precondNames) == 0 {
		return "true"
	}

	var okChecks []string
	for _, name := range precondNames {
		okField := toSnakeCase(name) + "_ok"
		okChecks = append(okChecks, fmt.Sprintf("(.%s // false)", okField))
	}

	return strings.Join(okChecks, " and ")
}

// BuildNotMetReasonExpr builds the notMetReason expression that identifies which precondition failed.
func BuildNotMetReasonExpr(precondNames []string) string {
	if len(precondNames) == 0 {
		return `""`
	}

	// Build a cascading if-elif-else expression
	var expr strings.Builder
	expr.WriteString("(")

	for i, name := range precondNames {
		okField := toSnakeCase(name) + "_ok"
		if i > 0 {
			expr.WriteString(" else ")
		}
		expr.WriteString(fmt.Sprintf("if (.%s // false) == false then \"%s failed\"", okField, name))
	}

	expr.WriteString(" else \"\" end)")
	return expr.String()
}

// ConvertGoTemplateToJQ converts Go template syntax ({{ .field }}) to jq expressions (${ .field }).
func ConvertGoTemplateToJQ(template string) string {
	// Pattern to match Go template expressions
	re := regexp.MustCompile(`\{\{\s*\.([^}]+)\s*\}\}`)

	return re.ReplaceAllStringFunc(template, func(match string) string {
		// Extract the field path
		matches := re.FindStringSubmatch(match)
		if len(matches) < 2 {
			return match
		}
		fieldPath := strings.TrimSpace(matches[1])
		return fmt.Sprintf("${ .params.%s }", fieldPath)
	})
}

// formatJQValue formats a value for use in a jq expression.
func formatJQValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		// Escape quotes in string
		escaped := strings.ReplaceAll(v, `"`, `\"`)
		return fmt.Sprintf(`"%s"`, escaped)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("%v", v)
	default:
		// For complex types, convert to string
		return fmt.Sprintf(`"%v"`, v)
	}
}

// toStringSlice converts an interface{} to a slice of strings.
func toStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []interface{}:
		var result []string
		for _, item := range v {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	case string:
		return []string{v}
	default:
		return nil
	}
}

// normalizeFieldPath ensures a field path starts with a dot.
func normalizeFieldPath(field string) string {
	// Handle JSONPath-style paths like {.status.phase}
	field = strings.TrimPrefix(field, "{")
	field = strings.TrimSuffix(field, "}")

	if !strings.HasPrefix(field, ".") {
		return "." + field
	}
	return field
}

// celToJQ converts a CEL expression to a jq approximation.
// This is a best-effort conversion for common patterns.
func celToJQ(celExpr string) string {
	// Simple field access: response.status.phase -> .response.status.phase
	if !strings.Contains(celExpr, "(") && !strings.Contains(celExpr, " ") && !strings.Contains(celExpr, "?") {
		return "." + celExpr
	}

	// Try to convert common CEL patterns to jq
	return convertCELToJQ(celExpr)
}

// convertCELToJQ converts common CEL expression patterns to jq equivalents.
// Handles:
//   - Optional chaining: foo.?bar.?baz -> .foo.bar.baz // null
//   - orValue(): foo.orValue("default") -> .foo // "default"
//   - Ternary: condition ? trueVal : falseVal -> if condition then trueVal else falseVal end
//   - Comparison operators: ==, !=, <, >, <=, >=
func convertCELToJQ(celExpr string) string {
	expr := strings.TrimSpace(celExpr)

	// Handle ternary expressions: condition ? trueVal : falseVal
	if ternaryResult := convertCELTernary(expr); ternaryResult != "" {
		return ternaryResult
	}

	// Handle .orValue() pattern: field.orValue("default")
	expr = convertCELOrValue(expr)

	// Handle optional chaining: foo.?bar -> foo.bar
	// In jq, we'll handle nulls with // operator
	expr = convertCELOptionalChaining(expr)

	// Add leading dot if it's a field path
	if len(expr) > 0 && expr[0] != '.' && expr[0] != '(' && expr[0] != '"' {
		expr = "." + expr
	}

	return expr
}

// convertCELTernary converts CEL ternary expressions to jq if-then-else.
// Pattern: condition ? trueVal : falseVal -> if condition then trueVal else falseVal end
func convertCELTernary(expr string) string {
	// Find the ? and : that form the ternary
	questionIdx := findTernaryQuestion(expr)
	if questionIdx == -1 {
		return ""
	}

	colonIdx := findTernaryColon(expr, questionIdx)
	if colonIdx == -1 {
		return ""
	}

	condition := strings.TrimSpace(expr[:questionIdx])
	trueVal := strings.TrimSpace(expr[questionIdx+1 : colonIdx])
	falseVal := strings.TrimSpace(expr[colonIdx+1:])

	// Recursively convert each part
	conditionJQ := convertCELToJQ(condition)
	trueValJQ := convertCELToJQ(trueVal)
	falseValJQ := convertCELToJQ(falseVal)

	return fmt.Sprintf("(if %s then %s else %s end)", conditionJQ, trueValJQ, falseValJQ)
}

// findTernaryQuestion finds the index of ? in a ternary expression, ignoring ?. optional chaining.
func findTernaryQuestion(expr string) int {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '?':
			// Check if this is optional chaining (?.)
			if i+1 < len(expr) && expr[i+1] == '.' {
				continue
			}
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// findTernaryColon finds the matching : for a ternary expression.
func findTernaryColon(expr string, questionIdx int) int {
	depth := 0
	for i := questionIdx + 1; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '?':
			// Nested ternary - skip to its colon
			if i+1 < len(expr) && expr[i+1] != '.' {
				depth++
			}
		case ':':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// convertCELOrValue converts .orValue("default") to jq's // operator.
// Pattern: field.orValue("default") -> (field // "default")
func convertCELOrValue(expr string) string {
	re := regexp.MustCompile(`\.orValue\(([^)]+)\)`)
	return re.ReplaceAllStringFunc(expr, func(match string) string {
		// Extract the default value
		matches := re.FindStringSubmatch(match)
		if len(matches) < 2 {
			return match
		}
		defaultVal := strings.TrimSpace(matches[1])
		return " // " + defaultVal
	})
}

// convertCELOptionalChaining converts CEL's ?. to regular field access.
// In jq, we handle null safety with // operator at the end of the chain.
func convertCELOptionalChaining(expr string) string {
	// Replace .? with . (jq handles null traversal differently)
	result := strings.ReplaceAll(expr, ".?", ".")

	// If the expression had optional chaining, wrap with null coalescing
	if strings.Contains(expr, ".?") && !strings.Contains(result, " // ") {
		// Don't add // null if there's already a default
		result = "(" + result + " // null)"
	}

	return result
}

// ConvertCELExpressionToJQ is the public entry point for CEL to jq conversion.
// Returns the jq expression and a boolean indicating if conversion was successful.
func ConvertCELExpressionToJQ(celExpr string) (string, bool) {
	// Check for patterns we can't handle
	unsupportedPatterns := []string{".filter(", ".map(", ".exists(", ".all(", ".size()", "has("}
	for _, pattern := range unsupportedPatterns {
		if strings.Contains(celExpr, pattern) {
			return "", false
		}
	}

	jqExpr := convertCELToJQ(celExpr)
	return jqExpr, true
}

// BuildPayloadFieldExpr builds a jq expression for a payload field.
// Handles both simple field references and CEL expressions.
func BuildPayloadFieldExpr(fieldDef map[string]interface{}) (string, bool) {
	// Check for field reference
	if field, ok := fieldDef["field"].(string); ok && field != "" {
		defaultVal := fieldDef["default"]
		jqField := "." + strings.TrimPrefix(field, ".")
		if defaultVal != nil {
			return fmt.Sprintf("(%s // %s)", jqField, formatJQValue(defaultVal)), true
		}
		return jqField, true
	}

	// Check for expression
	if expr, ok := fieldDef["expression"].(string); ok && expr != "" {
		jqExpr, ok := ConvertCELExpressionToJQ(expr)
		if !ok {
			return "", false
		}
		defaultVal := fieldDef["default"]
		if defaultVal != nil {
			return fmt.Sprintf("(%s // %s)", jqExpr, formatJQValue(defaultVal)), true
		}
		return jqExpr, true
	}

	return "", false
}

// toSnakeCase converts a string to snake_case.
func toSnakeCase(s string) string {
	// Replace hyphens with underscores
	s = strings.ReplaceAll(s, "-", "_")

	// Insert underscores before uppercase letters and convert to lowercase
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		if r >= 'A' && r <= 'Z' {
			result.WriteRune(r + 32) // Convert to lowercase
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
