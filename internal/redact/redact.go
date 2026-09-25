package redact

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type Pattern struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

type Options struct {
	EnvironmentValues []string  `json:"environment_values,omitempty"`
	Patterns          []Pattern `json:"patterns,omitempty"`
}

type Match struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
}

type Redactor struct {
	rules []compiledRule
}

type compiledRule struct {
	name       string
	expression *regexp.Regexp
}

type mapEntry struct {
	key   string
	value any
}

type pendingMapEntry struct {
	key         string
	redactedKey string
	keyMatches  []Match
	value       any
}

var defaultRules = []compiledRule{
	{name: "bearer_token", expression: regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]{8,}`)},
	{name: "private_key", expression: regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )?PRIVATE KEY-----`)},
	{name: "database_url", expression: regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|rediss?|amqps?|clickhouse)://[^\s/@:]+:[^\s/@]+@[^\s]+`)},
	{name: "api_key", expression: regexp.MustCompile(`\b(?:sk-(?:proj-)?[A-Za-z0-9_-]{16,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})\b`)},
}

func Default() Redactor {
	return Redactor{rules: append([]compiledRule(nil), defaultRules...)}
}

func New(options Options) (Redactor, error) {
	rules := make([]compiledRule, 0, len(options.EnvironmentValues)+len(options.Patterns)+len(defaultRules))
	for _, value := range options.EnvironmentValues {
		if value == "" {
			continue
		}
		rules = append(rules, compiledRule{
			name:       "configured_environment",
			expression: regexp.MustCompile(regexp.QuoteMeta(value)),
		})
	}
	for _, pattern := range options.Patterns {
		if pattern.Name == "" {
			return Redactor{}, errors.New("custom redaction pattern name is required")
		}
		expression, err := regexp.Compile(pattern.Expression)
		if err != nil {
			return Redactor{}, fmt.Errorf("compile custom redaction pattern %q: %w", pattern.Name, err)
		}
		rules = append(rules, compiledRule{name: pattern.Name, expression: expression})
	}
	rules = append(rules, defaultRules...)
	return Redactor{rules: rules}, nil
}

func (redactor Redactor) Apply(value any) (any, []Match) {
	rules := redactor.rules
	if len(rules) == 0 {
		rules = defaultRules
	}
	return applyValue(value, "$", rules)
}

func applyValue(value any, field string, rules []compiledRule) (any, []Match) {
	switch typed := value.(type) {
	case string:
		return applyString(typed, field, rules)
	case map[string]any:
		if typed == nil {
			return typed, nil
		}
		entries := make([]mapEntry, 0, len(typed))
		for key, child := range typed {
			entries = append(entries, mapEntry{key: key, value: child})
		}
		return applyMapEntries(entries, field, rules)
	case []any:
		if typed == nil {
			return typed, nil
		}
		result := make([]any, len(typed))
		matches := make([]Match, 0)
		for index, child := range typed {
			redactedChild, childMatches := applyValue(child, fmt.Sprintf("%s[%d]", field, index), rules)
			result[index] = redactedChild
			matches = append(matches, childMatches...)
		}
		return result, matches
	default:
		return applyReflected(value, field, rules)
	}
}

func applyReflected(value any, field string, rules []compiledRule) (any, []Match) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return value, nil
	}
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String || reflected.IsNil() {
			return value, nil
		}
		entries := make([]mapEntry, 0, reflected.Len())
		iterator := reflected.MapRange()
		for iterator.Next() {
			entries = append(entries, mapEntry{key: iterator.Key().String(), value: iterator.Value().Interface()})
		}
		return applyMapEntries(entries, field, rules)
	case reflect.Array:
		return applyReflectedSequence(reflected, field, rules)
	case reflect.Slice:
		if reflected.IsNil() {
			return value, nil
		}
		return applyReflectedSequence(reflected, field, rules)
	default:
		return value, nil
	}
}

func applyMapEntries(entries []mapEntry, field string, rules []compiledRule) (any, []Match) {
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].key < entries[right].key
	})
	pending := make([]pendingMapEntry, 0, len(entries))
	reserved := make(map[string]struct{})
	for _, entry := range entries {
		redactedKey, keyMatches := applyString(entry.key, field, rules)
		pending = append(pending, pendingMapEntry{
			key:         entry.key,
			redactedKey: redactedKey,
			keyMatches:  keyMatches,
			value:       entry.value,
		})
		if redactedKey == entry.key {
			reserved[redactedKey] = struct{}{}
		}
	}

	result := make(map[string]any, len(entries))
	matches := make([]Match, 0)
	for _, entry := range pending {
		key := entry.redactedKey
		if key != entry.key {
			key = uniqueMapKey(result, reserved, key)
		}
		redactedChild, childMatches := applyValue(entry.value, childField(field, key), rules)
		result[key] = redactedChild
		matches = append(matches, entry.keyMatches...)
		matches = append(matches, childMatches...)
	}
	return result, matches
}

func uniqueMapKey(result map[string]any, reserved map[string]struct{}, key string) string {
	if _, exists := result[key]; !exists {
		if _, preserved := reserved[key]; !preserved {
			return key
		}
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s~%d", key, suffix)
		_, resultExists := result[candidate]
		_, reservedExists := reserved[candidate]
		if !resultExists && !reservedExists {
			return candidate
		}
	}
}

func applyReflectedSequence(values reflect.Value, field string, rules []compiledRule) (any, []Match) {
	result := make([]any, values.Len())
	matches := make([]Match, 0)
	for index := 0; index < values.Len(); index++ {
		redactedChild, childMatches := applyValue(values.Index(index).Interface(), fmt.Sprintf("%s[%d]", field, index), rules)
		result[index] = redactedChild
		matches = append(matches, childMatches...)
	}
	return result, matches
}

func applyString(value, field string, rules []compiledRule) (string, []Match) {
	result := value
	matches := make([]Match, 0)
	for _, rule := range rules {
		indices := rule.expression.FindAllStringIndex(result, -1)
		if len(indices) == 0 {
			continue
		}
		var builder strings.Builder
		last := 0
		for _, index := range indices {
			builder.WriteString(result[last:index[0]])
			builder.WriteString("[REDACTED:")
			builder.WriteString(rule.name)
			builder.WriteString("]")
			matches = append(matches, Match{Field: field, Rule: rule.name})
			last = index[1]
		}
		builder.WriteString(result[last:])
		result = builder.String()
	}
	return result, matches
}

func childField(parent, key string) string {
	return parent + "." + key
}
