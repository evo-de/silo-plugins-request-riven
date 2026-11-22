package main

import (
	"encoding/json"
	"os"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	// Silo sends a series request's seasons, and asks for missing seasons of a
	// series it already has, only when the plugin declares this.
	if !caps[0].GetRequestRouter().GetSupportsSeasons() {
		t.Fatal("request_router.supports_seasons: want true")
	}
}

// Silo reads index.json with encoding/json into the SDK's generated types, so
// an enum there must be its number ("control": 3), as in Silo's own catalogs.
// The plugin reads manifest.json with protojson, which also takes the name.
func TestIndexManifestMatchesManifest(t *testing.T) {
	var manifest pluginv1.PluginManifest
	if err := protojson.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	raw, err := os.ReadFile("index.json")
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var index struct {
		Plugins []struct {
			Manifest *pluginv1.PluginManifest `json:"manifest"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("decode index.json as Silo does: %v", err)
	}
	if len(index.Plugins) != 1 {
		t.Fatalf("want 1 plugin in index.json, got %d", len(index.Plugins))
	}
	if !proto.Equal(index.Plugins[0].Manifest, &manifest) {
		t.Fatal("index.json .plugins[0].manifest differs from manifest.json")
	}
}
