package registry

import (
	"fmt"
	"regexp"
)

// templateVarPattern matches placeholders like {variable_name} in template strings.
var templateVarPattern = regexp.MustCompile(`\{([a-zA-Z0-9_-]{1,64})\}`)

// ResolveTemplate replaces all {placeholder} occurrences in tmpl with values
// from the variables map. Returns an error if any placeholder has no matching variable.
func ResolveTemplate(tmpl string, variables map[string]string) (string, error) {
	var missingVar string
	result := templateVarPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		// Extract the variable name (strip the braces).
		varName := match[1 : len(match)-1]
		val, ok := variables[varName]
		if !ok {
			missingVar = varName
			return match
		}
		return val
	})
	if missingVar != "" {
		return "", fmt.Errorf("template variable %q is not defined", missingVar)
	}
	return result, nil
}

// ExtractTemplateVars returns the unique placeholder names found in a template string.
func ExtractTemplateVars(tmpl string) []string {
	matches := templateVarPattern.FindAllStringSubmatch(tmpl, -1)
	seen := map[string]bool{}
	var vars []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			vars = append(vars, name)
		}
	}
	return vars
}
