package api

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// Job observations retain only admitted inputs. Withhold the whole field when
// nested data contains credentials, rather than publishing a misleading partial input.
func jobParameters(op operation, values map[string]json.RawMessage) (map[string]json.RawMessage, []string) {
	parameters := make(map[string]json.RawMessage)
	var omitted []string
	for name, raw := range values {
		if _, admitted := op.Parameters[name]; !admitted {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil || privateJobParameter(name, value) {
			omitted = append(omitted, name)
			continue
		}
		parameters[name] = bytes.Clone(raw)
	}
	sort.Strings(omitted)
	return parameters, omitted
}

func privateJobParameter(name string, value any) bool {
	key := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(name))
	for _, sensitive := range []string{"token", "secret", "password", "passphrase", "credential", "authorization", "cookie", "header", "apikey"} {
		if strings.Contains(key, sensitive) {
			return true
		}
	}
	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			if privateJobParameter(key, nested) {
				return true
			}
		}
	case []any:
		for _, nested := range value {
			if privateJobParameter("", nested) {
				return true
			}
		}
	case string:
		for _, word := range strings.Fields(value) {
			if !strings.Contains(word, "://") {
				continue
			}
			parsed, err := url.Parse(word)
			if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return true
			}
		}
	}
	return false
}
