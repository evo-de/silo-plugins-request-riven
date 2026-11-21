package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestEmbeddedManifestLoads(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if m.GetPluginId() != "silo.requests.riven" {
		t.Fatalf("plugin_id: want silo.requests.riven got %q", m.GetPluginId())
	}
	caps := m.GetCapabilities()
	if len(caps) != 1 {
		t.Fatalf("want 1 capability, got %d", len(caps))
	}
	if caps[0].GetType() != "request_router.v1" || caps[0].GetId() != "riven" {
		t.Fatalf("capability[0]: want request_router.v1/riven got %q/%q", caps[0].GetType(), caps[0].GetId())
	}
}

func TestIndexManifestMatchesManifest(t *testing.T) {
	var manifest any
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	raw, err := os.ReadFile("index.json")
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var index struct {
		Plugins []struct {
			Manifest any `json:"manifest"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("decode index.json: %v", err)
	}
	if len(index.Plugins) != 1 {
		t.Fatalf("want 1 plugin in index.json, got %d", len(index.Plugins))
	}
	if !reflect.DeepEqual(index.Plugins[0].Manifest, manifest) {
		t.Fatal("index.json .plugins[0].manifest differs from manifest.json")
	}
}
