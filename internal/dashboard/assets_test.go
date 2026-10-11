package dashboard

import (
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestPackagedMetadataAndAssetsAreVerified(t *testing.T) {
	page := []byte("<!doctype html><main>unit fixture</main>")
	index, _ := json.Marshal(map[string]any{"schemaVersion": 1, "files": map[string]string{"index.html": hash(page)}})
	d := declaration{SchemaVersion: 1, AppID: "resources", AppVersion: "0.1.0"}
	d.Dashboard.EntryRef = "dashboard/index.html"
	d.Dashboard.ArtifactDigest = "sha256:" + hash(index)
	d.Dashboard.Adapter = "wujie"
	d.Dashboard.SDKMajor = 1
	d.Dashboard.MinimumSDKVersion = "1.1.0"
	d.Dashboard.RequiredCapabilities = []string{"app.request"}
	d.Dashboard.OptionalCapabilities = []string{}
	d.Dashboard.AppAPIPolicyRef = "resources-dashboard-v1"
	d.Dashboard.KeepAlive = true
	raw, _ := json.Marshal(d)
	files := fstest.MapFS{"index.html": {Data: page}, "asset-index.json": {Data: index}, "dashboard-declaration.json": {Data: raw}}
	assets, err := Load(files, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(assets.Declaration()) != string(raw) || string(assets.Index()) != string(index) {
		t.Fatal("metadata bytes changed")
	}
	if data, mime, ok := assets.File("index.html"); !ok || string(data) != string(page) || mime != "text/html; charset=utf-8" {
		t.Fatal("entry unavailable")
	}
	for _, name := range []string{"asset-index.json", "../index.html", "/index.html", "assets//main.js"} {
		if _, _, ok := assets.File(name); ok {
			t.Fatal("undeclared file served")
		}
	}
	files["index.html"] = &fstest.MapFile{Data: []byte("tampered")}
	if _, err := Load(files, "0.1.0"); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	files["index.html"] = &fstest.MapFile{Data: page}
	if _, err := Load(files, "0.2.0"); err == nil {
		t.Fatal("version mismatch accepted")
	}
}
