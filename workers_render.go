package platform

import (
	"fmt"
	"slices"
	"strings"
)

func shortID(id string) string {
	if len(id) <= shortIDLength {
		return id
	}
	return id[:shortIDLength]
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func yamlQuoted(v string) string {
	return fmt.Sprintf("%q", v)
}

func renderProjectConfigYAML(spec ProjectSpec) []byte {
	spec = normalizeProjectSpec(spec)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\n", spec.APIVersion)
	fmt.Fprintf(&b, "kind: %s\n", spec.Kind)
	fmt.Fprintf(&b, "name: %s\n", spec.Name)
	fmt.Fprintf(&b, "runtime: %s\n", spec.Runtime)
	if len(spec.Capabilities) > 0 {
		b.WriteString("capabilities:\n")
		for _, c := range spec.Capabilities {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	b.WriteString("environments:\n")
	for _, env := range sortedKeys(spec.Environments) {
		cfg := spec.Environments[env]
		fmt.Fprintf(&b, "  %s:\n", env)
		b.WriteString("    vars:\n")
		keys := sortedKeys(cfg.Vars)
		if len(keys) == 0 {
			b.WriteString("      {}\n")
		}
		for _, k := range keys {
			fmt.Fprintf(&b, "      %s: %s\n", k, yamlQuoted(cfg.Vars[k]))
		}
	}
	b.WriteString("networkPolicies:\n")
	fmt.Fprintf(&b, "  ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "  egress: %s\n", spec.NetworkPolicies.Egress)
	return []byte(b.String())
}

func preferredEnvironment(spec ProjectSpec) (string, map[string]string) {
	spec = normalizeProjectSpec(spec)
	if env, ok := spec.Environments["dev"]; ok {
		return "dev", env.Vars
	}
	names := sortedKeys(spec.Environments)
	if len(names) == 0 {
		return "default", map[string]string{}
	}
	first := names[0]
	return first, spec.Environments[first].Vars
}

func renderDeploymentManifest(spec ProjectSpec, image string) string {
	spec = normalizeProjectSpec(spec)
	envName, vars := preferredEnvironment(spec)
	name := safeName(spec.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: apps/v1\n")
	fmt.Fprintf(&b, "kind: Deployment\n")
	fmt.Fprintf(&b, "metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", name)
	fmt.Fprintf(&b, "spec:\n")
	fmt.Fprintf(&b, "  replicas: 1\n")
	fmt.Fprintf(&b, "  selector:\n")
	fmt.Fprintf(&b, "    matchLabels:\n")
	fmt.Fprintf(&b, "      app: %s\n", name)
	fmt.Fprintf(&b, "  template:\n")
	fmt.Fprintf(&b, "    metadata:\n")
	fmt.Fprintf(&b, "      labels:\n")
	fmt.Fprintf(&b, "        app: %s\n", name)
	fmt.Fprintf(&b, "      annotations:\n")
	fmt.Fprintf(&b, "        platform.example.com/environment: %s\n", envName)
	fmt.Fprintf(&b, "        platform.example.com/ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "        platform.example.com/egress: %s\n", spec.NetworkPolicies.Egress)
	fmt.Fprintf(&b, "    spec:\n")
	fmt.Fprintf(&b, "      containers:\n")
	fmt.Fprintf(&b, "      - name: app\n")
	fmt.Fprintf(&b, "        image: %s\n", image)
	fmt.Fprintf(&b, "        imagePullPolicy: IfNotPresent\n")
	fmt.Fprintf(&b, "        ports:\n")
	fmt.Fprintf(&b, "        - containerPort: 8080\n")
	keys := sortedKeys(vars)
	if len(keys) > 0 {
		fmt.Fprintf(&b, "        env:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "        - name: %s\n", k)
			fmt.Fprintf(&b, "          value: %s\n", yamlQuoted(vars[k]))
		}
	}
	return b.String()
}

func renderServiceManifest(spec ProjectSpec) string {
	spec = normalizeProjectSpec(spec)
	name := safeName(spec.Name)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
  - name: http
    port: 80
    targetPort: 8080
`, name, name)
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "project"
	}
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, '-')
		case r == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "project"
	}
	return string(out)
}
