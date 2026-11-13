// Package yamlx is a minimal YAML parser for Aether spec files.
// It uses zero external dependencies — pure Go stdlib.
// Supports: string scalars, bool, float, int, maps, sequences.
package yamlx

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Unmarshal parses YAML bytes into a map[string]interface{}.
func Unmarshal(data []byte) (map[string]interface{}, error) {
	lines := splitLines(string(data))
	result, _, err := parseMap(lines, 0, 0)
	return result, err
}

func splitLines(s string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func indentOf(line string) int {
	for i, c := range line {
		if c != ' ' {
			return i
		}
	}
	return len(line)
}

func isEmpty(line string) bool {
	return strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#")
}

// parseMap reads key:value pairs at a given indent level.
func parseMap(lines []string, start, indent int) (map[string]interface{}, int, error) {
	result := make(map[string]interface{})
	i := start
	for i < len(lines) {
		line := lines[i]
		if isEmpty(line) {
			i++
			continue
		}
		// Tabs are not valid YAML indentation; our space-based indent counter
		// would silently misparse them, so reject them explicitly.
		if strings.HasPrefix(line, "\t") {
			return result, i, fmt.Errorf("line %d: tab indentation is not allowed; use spaces", i+1)
		}
		ind := indentOf(line)
		if ind < indent {
			break
		}
		if ind > indent {
			i++
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Sequence item at this level? (starts with -)
		if strings.HasPrefix(trimmed, "- ") {
			break
		}

		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			return result, i, fmt.Errorf("line %d: expected 'key: value', got %q", i+1, trimmed)
		}
		key := strings.TrimSpace(trimmed[:colonIdx])
		rest := strings.TrimSpace(trimmed[colonIdx+1:])
		if _, dup := result[key]; dup {
			return result, i, fmt.Errorf("line %d: duplicate key %q", i+1, key)
		}

		if rest != "" && rest != "|" && rest != ">" {
			// Inline value
			result[key] = parseScalar(rest)
			i++
		} else if rest == "|" || rest == ">" {
			// Block scalar (literal or folded)
			i++
			var sb strings.Builder
			for i < len(lines) {
				l := lines[i]
				if isEmpty(l) {
					sb.WriteString("\n")
					i++
					continue
				}
				ind2 := indentOf(l)
				if ind2 <= indent {
					break
				}
				sb.WriteString(strings.TrimSpace(l) + "\n")
				i++
			}
			result[key] = sb.String()
		} else {
			// Look at next non-empty line to determine type
			i++
			nextI := i
			for nextI < len(lines) && isEmpty(lines[nextI]) {
				nextI++
			}
			if nextI >= len(lines) {
				result[key] = nil
				continue
			}
			nextLine := lines[nextI]
			nextInd := indentOf(nextLine)
			nextTrimmed := strings.TrimSpace(nextLine)

			if nextInd > indent && strings.HasPrefix(nextTrimmed, "- ") {
				// sequence
				seq, newI, err := parseSequence(lines, nextI, nextInd)
				if err != nil {
					return result, newI, err
				}
				result[key] = seq
				i = newI
			} else if nextInd > indent {
				// nested map
				subMap, newI, err := parseMap(lines, nextI, nextInd)
				if err != nil {
					return result, newI, err
				}
				result[key] = subMap
				i = newI
			} else {
				result[key] = nil
			}
		}
	}
	return result, i, nil
}

// parseSequence reads a YAML sequence starting with "- ".
func parseSequence(lines []string, start, indent int) ([]interface{}, int, error) {
	var result []interface{}
	i := start
	for i < len(lines) {
		line := lines[i]
		if isEmpty(line) {
			i++
			continue
		}
		ind := indentOf(line)
		if ind < indent {
			break
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		itemStr := strings.TrimSpace(trimmed[2:])
		i++
		// Check if the item has sub-keys (map item)
		nextI := i
		for nextI < len(lines) && isEmpty(lines[nextI]) {
			nextI++
		}
		if nextI < len(lines) {
			nextInd := indentOf(lines[nextI])
			nextTrimmed := strings.TrimSpace(lines[nextI])
			if nextInd > indent && strings.Contains(nextTrimmed, ":") && !strings.HasPrefix(nextTrimmed, "- ") {
				// The sequence item is a map
				subMap, newI, err := parseMap(lines, i, nextInd)
				if err != nil {
					return result, newI, err
				}
				// Merge the first key from itemStr into subMap if it has a colon
				if strings.Contains(itemStr, ":") {
					colonIdx := strings.Index(itemStr, ":")
					k := strings.TrimSpace(itemStr[:colonIdx])
					v := strings.TrimSpace(itemStr[colonIdx+1:])
					if _, exists := subMap[k]; !exists {
						subMap[k] = parseScalar(v)
					}
				} else if itemStr != "" {
					subMap["_value"] = parseScalar(itemStr)
				}
				result = append(result, subMap)
				i = newI
				continue
			}
		}
		// Check if itemStr itself is "key: value"
		if strings.Contains(itemStr, ":") {
			colonIdx := strings.Index(itemStr, ":")
			k := strings.TrimSpace(itemStr[:colonIdx])
			v := strings.TrimSpace(itemStr[colonIdx+1:])
			m := map[string]interface{}{k: parseScalar(v)}
			// Try to parse following keys at deeper indent as part of this map item
			if nextI < len(lines) && indentOf(lines[nextI]) > indent {
				subMap, newI, _ := parseMap(lines, i, indentOf(lines[nextI]))
				for sk, sv := range subMap {
					m[sk] = sv
				}
				i = newI
			}
			result = append(result, m)
		} else {
			result = append(result, parseScalar(itemStr))
		}
	}
	return result, i, nil
}

func parseScalar(s string) interface{} {
	s = strings.TrimSpace(s)
	// Remove quotes
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		return s[1 : len(s)-1]
	}
	// Bool
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	case "null", "~", "":
		return nil
	}
	// Flow mapping {k: v, ...} and flow sequence [a, b, ...].
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		return parseFlowMap(s[1 : len(s)-1])
	}
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return parseFlowSeq(s[1 : len(s)-1])
	}
	// Int
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return int(i)
	}
	// Float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// parseFlowMap parses an inline mapping body (the text between { and }), e.g.
// "minimum: 0.9" or "enabled: true, max_attempts: 2". Values are parsed
// recursively, so nested flow collections work.
func parseFlowMap(body string) map[string]interface{} {
	m := make(map[string]interface{})
	for _, part := range splitFlow(body) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ci := strings.Index(part, ":")
		if ci < 0 {
			continue
		}
		k := strings.TrimSpace(part[:ci])
		v := strings.TrimSpace(part[ci+1:])
		m[k] = parseScalar(v)
	}
	return m
}

// parseFlowSeq parses an inline sequence body (the text between [ and ]).
func parseFlowSeq(body string) []interface{} {
	var out []interface{}
	for _, part := range splitFlow(body) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, parseScalar(part))
	}
	return out
}

// splitFlow splits a flow-collection body on top-level commas, respecting nested
// {}/[] and single/double quotes so commas inside them don't split.
func splitFlow(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	inS, inD := false, false
	for _, r := range s {
		switch {
		case inS:
			cur.WriteRune(r)
			if r == '\'' {
				inS = false
			}
		case inD:
			cur.WriteRune(r)
			if r == '"' {
				inD = false
			}
		case r == '\'':
			inS = true
			cur.WriteRune(r)
		case r == '"':
			inD = true
			cur.WriteRune(r)
		case r == '{' || r == '[':
			depth++
			cur.WriteRune(r)
		case r == '}' || r == ']':
			depth--
			cur.WriteRune(r)
		case r == ',' && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// GetString safely extracts a string from a parsed map.
func GetString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case nil:
			return ""
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// GetBool safely extracts a bool.
func GetBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// GetInt safely extracts an int.
func GetInt(m map[string]interface{}, key string, def int) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case float64:
			return int(t)
		}
	}
	return def
}

// GetFloat safely extracts a float64.
func GetFloat(m map[string]interface{}, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case float64:
			return t
		case int:
			return float64(t)
		}
	}
	return def
}

// GetMap safely extracts a nested map.
func GetMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok {
		if sub, ok := v.(map[string]interface{}); ok {
			return sub
		}
	}
	return nil
}

// GetSlice safely extracts a slice.
func GetSlice(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key]; ok {
		if s, ok := v.([]interface{}); ok {
			return s
		}
	}
	return nil
}
