package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
	"go.yaml.in/yaml/v3"
)

// Load keeps file and environment access at the entry point. Koanf owns merging
// and decoding; validation below enforces the service's strict input contract.
func Load(source io.Reader, environment []string) (Config, error) {
	defaults := Defaults()
	var schema yaml.Node
	if err := schema.Encode(defaults); err != nil {
		return Config{}, err
	}

	var initial map[string]any
	if err := schema.Decode(&initial); err != nil {
		return Config{}, err
	}

	leaves := make(map[string]reflect.Type)
	names := make(map[string]string)
	walkLeaves(reflect.ValueOf(defaults), "", func(path string, v reflect.Value) {
		leaves[path] = v.Type()
		names["MDS_"+strings.ToUpper(strings.ReplaceAll(path, ".", "_"))] = path
	})
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(initial, ""), nil); err != nil {
		return Config{}, err
	}

	if source != nil {
		values, err := readYAML(source, &schema, leaves)
		if err != nil {
			return Config{}, err
		}

		if err := k.Load(confmap.Provider(values, ""), nil); err != nil {
			return Config{}, err
		}
	}

	values, err := readEnvironment(environment, leaves, names)
	if err != nil {
		return Config{}, err
	}

	if err := k.Load(confmap.Provider(values, ""), nil); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			ErrorUnused:      true,
			WeaklyTypedInput: false,
			DecodeHook:       mapstructure.StringToTimeDurationHookFunc(),
		},
	}); err != nil {
		return Config{}, errors.New("cannot decode configuration")
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func readEnvironment(environment []string, leaves map[string]reflect.Type, names map[string]string) (map[string]any, error) {
	seen := make(map[string]bool)

	for _, entry := range environment {
		name, _, hasValue := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "MDS_") {
			continue
		}

		canonical := canonicalEnv(name)
		if _, ok := names[canonical]; !ok {
			return nil, fmt.Errorf("unknown environment setting %s", name)
		}

		if !hasValue || seen[canonical] {
			return nil, fmt.Errorf("invalid or duplicate environment setting %s", name)
		}

		seen[canonical] = true
	}

	var envError error
	provider := env.Provider(".", env.Opt{
		Prefix:      "MDS_",
		EnvironFunc: func() []string { return environment },
		TransformFunc: func(name, raw string) (string, any) {
			path := names[canonicalEnv(name)]
			value, err := parseEnvironment(raw, leaves[path])
			if err != nil && envError == nil {
				envError = fmt.Errorf("invalid environment setting %s", name)
			}

			return path, value
		},
	})
	values, err := provider.Read()
	if err != nil {
		return nil, err
	}

	if envError != nil {
		return nil, envError
	}

	return values, nil
}

func canonicalEnv(name string) string {
	if name == "MDS_SENTRY_DSN" {
		return "MDS_OBSERVABILITY_SENTRY_DSN"
	}

	return name
}

func parseEnvironment(raw string, typ reflect.Type) (any, error) {
	switch typ.Kind() {
	case reflect.Int:
		return strconv.Atoi(raw)
	case reflect.Bool:
		if raw != "true" && raw != "false" {
			return nil, errors.New("expected boolean")
		}
		return strconv.ParseBool(raw)
	case reflect.Float64:
		return strconv.ParseFloat(raw, 64)
	case reflect.Slice:
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil || values == nil {
			return nil, errors.New("expected JSON string array")
		}
		return values, nil
	default:
		if typ == reflect.TypeFor[time.Duration]() {
			if _, err := time.ParseDuration(raw); err != nil {
				return nil, err
			}
		}
		return raw, nil
	}
}

func readYAML(source io.Reader, schema *yaml.Node, leaves map[string]reflect.Type) (map[string]any, error) {
	decoder := yaml.NewDecoder(source)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, nil
		}

		return nil, errors.New("invalid YAML configuration")
	}

	node := document.Content[0]
	if err := validateYAML(node, schema, "", leaves); err != nil {
		return nil, err
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("configuration must contain one YAML document")
	}

	var values map[string]any
	if err := node.Decode(&values); err != nil {
		return nil, errors.New("invalid YAML configuration")
	}

	return values, nil
}

// Checking before map decoding preserves duplicate keys, explicit nulls, and
// scalar types which ordinary configuration merges may otherwise discard.
func validateYAML(node, schema *yaml.Node, path string, leaves map[string]reflect.Type) error {
	invalid := func() error { return fmt.Errorf("invalid configuration at %s", displayPath(path)) }
	tagsMatch := node.Tag == schema.Tag || (schema.Tag == "!!float" && node.Tag == "!!int")

	if node.Kind != schema.Kind || node.Anchor != "" || !tagsMatch {
		return invalid()
	}

	if node.Kind == yaml.MappingNode {
		fields := make(map[string]*yaml.Node)

		for i := 0; i < len(schema.Content); i += 2 {
			fields[schema.Content[i].Value] = schema.Content[i+1]
		}

		seen := make(map[string]bool)

		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return invalid()
			}

			seen[key.Value] = true
			expected, ok := fields[key.Value]
			if !ok {
				return fmt.Errorf("unknown configuration key %s", joinPath(path, key.Value))
			}

			if err := validateYAML(node.Content[i+1], expected, joinPath(path, key.Value), leaves); err != nil {
				return err
			}
		}

		return nil
	}

	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if child.Kind != yaml.ScalarNode || child.Tag != "!!str" {
				return invalid()
			}
		}
	}

	target := reflect.New(leaves[path]).Interface()
	if err := node.Decode(target); err != nil {
		return invalid()
	}

	return nil
}

func walkLeaves(value reflect.Value, path string, visit func(string, reflect.Value)) {
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			walkLeaves(value.Field(i), joinPath(path, value.Type().Field(i).Tag.Get("yaml")), visit)
		}
	case reflect.Map:
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			walkLeaves(value.MapIndex(key), joinPath(path, key.String()), visit)
		}
	default:
		visit(path, value)
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}

	return parent + "." + child
}
func displayPath(path string) string {
	if path == "" {
		return "root"
	}

	return path
}
