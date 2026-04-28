package monitor

import "strings"

func isGitLabRunnerUser(username string) bool {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if normalized == "" {
		return false
	}
	return strings.HasSuffix(normalized, "\\gitlab-runner") || normalized == "gitlab-runner"
}

func envListToMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, value := range values {
		k, v, found := strings.Cut(value, "=")
		if !found || k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func ciLookup(values map[string]string, target string) string {
	if v, ok := values[target]; ok {
		return v
	}
	t := strings.ToLower(target)
	for k, v := range values {
		if strings.ToLower(k) == t {
			return v
		}
	}
	return ""
}
