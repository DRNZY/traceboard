package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestApplyRedactsBuiltInSecretsRecursively(t *testing.T) {
	privateKey := strings.Join([]string{
		"-----BEGIN " + "PRIVATE KEY-----",
		"synthetic-private-material",
		"-----END PRIVATE KEY-----",
	}, "\n")
	secrets := []string{
		"sk-live-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
		"Bearer synthetic.payload-0123456789abcdef",
		privateKey,
		"postgresql://trace:synthetic-password@127.0.0.1:5432/trace",
		syntheticGitHubToken(),
	}
	input := map[string]any{
		"api_key":       secrets[0],
		"authorization": secrets[1],
		"private_key":   secrets[2],
		"database_url":  secrets[3],
		"items":         []any{secrets[4], "visible"},
		"enabled":       true,
		"count":         float64(2),
		"empty":         nil,
	}

	redacted, matches := Default().Apply(input)
	output := redacted.(map[string]any)
	if got := output["api_key"]; got != "[REDACTED:api_key]" {
		t.Fatalf("api key = %v", got)
	}
	if got := output["authorization"]; got != "[REDACTED:bearer_token]" {
		t.Fatalf("authorization = %v", got)
	}
	if got := output["private_key"]; got != "[REDACTED:private_key]" {
		t.Fatalf("private key = %v", got)
	}
	if got := output["database_url"]; got != "[REDACTED:database_url]" {
		t.Fatalf("database URL = %v", got)
	}
	if got := output["items"].([]any)[0]; got != "[REDACTED:api_key]" {
		t.Fatalf("nested API key = %v", got)
	}
	if got := output["items"].([]any)[1]; got != "visible" {
		t.Fatalf("ordinary nested value = %v", got)
	}
	if output["enabled"] != true || output["count"] != float64(2) || output["empty"] != nil {
		t.Fatal("non-string JSON values changed")
	}

	rendered := renderJSON(t, redacted)
	matchJSON := renderJSON(t, matches)
	for _, secret := range secrets {
		for name, value := range map[string]string{"output": rendered, "matches": matchJSON} {
			if strings.Contains(value, secret) {
				t.Fatalf("%s retained full secret %q", name, secret)
			}
		}
	}

	assertMatch(t, matches, "$.api_key", "api_key")
	assertMatch(t, matches, "$.authorization", "bearer_token")
	assertMatch(t, matches, "$.private_key", "private_key")
	assertMatch(t, matches, "$.database_url", "database_url")
	assertMatch(t, matches, "$.items[0]", "api_key")
}

func TestApplyUsesConfiguredEnvironmentValuesAndCustomPatterns(t *testing.T) {
	environmentSecret := "configured-environment-secret"
	customSecret := "internal-abc12345"
	redactor, err := New(Options{
		EnvironmentValues: []string{environmentSecret},
		Patterns: []Pattern{
			{Name: "internal_identifier", Expression: `internal-[a-z0-9]{8}`},
		},
	})
	if err != nil {
		t.Fatalf("create redactor: %v", err)
	}

	redacted, matches := redactor.Apply([]any{
		map[string]any{
			"environment": "prefix " + environmentSecret + " suffix",
			"custom":      customSecret,
		},
	})
	output := redacted.([]any)[0].(map[string]any)
	if got := output["environment"]; got != "prefix [REDACTED:configured_environment] suffix" {
		t.Fatalf("environment value = %v", got)
	}
	if got := output["custom"]; got != "[REDACTED:internal_identifier]" {
		t.Fatalf("custom value = %v", got)
	}
	assertMatch(t, matches, "$[0].environment", "configured_environment")
	assertMatch(t, matches, "$[0].custom", "internal_identifier")
}

func TestApplyTraversesTypedJSONCompatibleMapsAndSlices(t *testing.T) {
	input := map[string][]string{
		"tokens": {syntheticGitHubToken(), "visible"},
	}

	redacted, matches := Default().Apply(input)
	output := redacted.(map[string]any)
	tokens := output["tokens"].([]any)
	if got := tokens[0]; got != "[REDACTED:api_key]" {
		t.Fatalf("typed slice secret = %v", got)
	}
	if got := tokens[1]; got != "visible" {
		t.Fatalf("typed slice ordinary value = %v", got)
	}
	assertMatch(t, matches, "$.tokens[0]", "api_key")
}

func TestApplyMapKeyRedactionIsCollisionSafeAndDeterministic(t *testing.T) {
	githubSecret := syntheticGitHubToken()
	openAISecret := "sk-live-" + strings.Repeat("b", 32)
	input := map[string]any{
		githubSecret: "first",
		openAISecret: "second",
		"visible":    "value",
	}

	var expectedOutput any
	var expectedMatches []Match
	for iteration := 0; iteration < 25; iteration++ {
		output, matches := Default().Apply(input)
		if iteration == 0 {
			expectedOutput = output
			expectedMatches = matches
			continue
		}
		if !reflect.DeepEqual(output, expectedOutput) || !reflect.DeepEqual(matches, expectedMatches) {
			t.Fatalf("redaction changed on iteration %d", iteration)
		}
	}

	output := expectedOutput.(map[string]any)
	if len(output) != 3 {
		t.Fatalf("redacted map length = %d, want 3", len(output))
	}
	if got := output["[REDACTED:api_key]"]; got != "first" {
		t.Fatalf("first colliding value = %v", got)
	}
	if got := output["[REDACTED:api_key]~2"]; got != "second" {
		t.Fatalf("second colliding value = %v in %#v", got, output)
	}
	if got := output["visible"]; got != "value" {
		t.Fatalf("ordinary key = %v", got)
	}
	rendered := renderJSON(t, expectedOutput)
	for _, secret := range []string{githubSecret, openAISecret} {
		if strings.Contains(rendered, secret) {
			t.Fatal("redacted map retained full secret key")
		}
	}
}

func TestNewRejectsInvalidCustomPattern(t *testing.T) {
	if _, err := New(Options{Patterns: []Pattern{{Name: "broken", Expression: `[`}}}); err == nil {
		t.Fatal("expected invalid custom pattern error")
	}
}

func assertMatch(t *testing.T, matches []Match, field, rule string) {
	t.Helper()
	for _, match := range matches {
		if match.Field == field && match.Rule == rule {
			return
		}
	}
	t.Fatalf("missing match field=%q rule=%q in %#v", field, rule, matches)
}

func syntheticGitHubToken() string {
	return "ghp_" + strings.Repeat("a", 36)
}

func renderJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return string(encoded)
}
