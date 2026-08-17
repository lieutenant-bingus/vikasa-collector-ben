package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
)

// yamlFields returns every yaml field name reachable from t, dotted for
// nesting. Slices of structs contribute their element type's fields, because
// a device's `id` needs documenting exactly once, not once per device.
func yamlFields(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + tag
		out = append(out, name)

		ft := f.Type
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, yamlFields(ft, name+".")...)
		}
	}
	return out
}

func TestConfigReferenceDocumentsEveryField(t *testing.T) {
	path := filepath.Join(repoRoot, "docs", "reference", "configuration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configuration reference: %v", err)
	}
	doc := string(raw)

	fields := yamlFields(reflect.TypeOf(config.Config{}), "")
	if len(fields) == 0 {
		t.Fatal("reflected zero config fields; the check would be vacuous")
	}

	for _, f := range fields {
		if !strings.Contains(doc, "`"+f+"`") {
			t.Errorf("config field %q is not documented in configuration.md "+
				"(expected it to appear as `%s`)", f, f)
		}
	}
}
