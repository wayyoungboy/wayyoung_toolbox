package genre

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMapAndApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "genre.csv")
	data := "\ufeffid,url,translate\nfoo,https://example/foo,Foo\nbar,https://example/bar,\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	mapping, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	got := mapping.Map([]string{"foo", "bar", "baz"})
	want := []string{"Foo", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Map = %#v, want %#v", got, want)
	}
}
