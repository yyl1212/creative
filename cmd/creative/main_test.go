package main

import (
	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedProvidersOverrideFile(t *testing.T) {
	root := t.TempDir()
	db, e := store.Open(filepath.Join(root, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s, e := research.New(db)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(root, "providers.json")
	if e = os.WriteFile(file, []byte(`{"primary":"openai","providers":{"openai":{"enabled":true,"validated":true,"model":"fixture-model"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	cfg, e := loadProviders(s, file)
	if e != nil || !cfg.Providers["openai"].Enabled {
		t.Fatal(cfg, e)
	}
	disabled := model.Config{Primary: "deepseek", Providers: map[string]model.Provider{"deepseek": {}, "openai": {}}}
	if e = s.SaveModelConfig(0, disabled, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	cfg, e = loadProviders(s, "missing.json")
	if e != nil || cfg.Primary != "deepseek" || cfg.Providers["openai"].Enabled {
		t.Fatal("saved override ignored", cfg, e)
	}
}
