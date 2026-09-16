// ABOUTME: Loads every persisted fixture under testdata/golden and checks it
// ABOUTME: against the live codec, so a host recorder test can trust these
// ABOUTME: files without reimplementing mux-json/1, and drift fails here.
package recording

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenFixtureFile mirrors the persisted fixture shape: Input and Output
// are the literal source text a host feeds to (or expects back from) the
// codec, stored as JSON strings rather than nested JSON values so that
// parsing the fixture file itself can never normalize away the very
// whitespace, key order, or numeric literal the fixture exists to pin.
type goldenFixtureFile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // "payload" or "record"
	Input       string `json:"input"`
	Output      string `json:"output"`
	SHA256      string `json:"sha256"`
}

// TestGoldenFixtureFiles_MatchLiveCodec proves the checked-in fixtures under
// testdata/golden are not stale: every persisted input still canonicalizes
// to exactly its persisted output (and, for record-kind fixtures, its
// persisted RecordSHA256) under the current implementation. A host recorder
// test elsewhere in the module can load these same files and compare its
// own storage round-trip against Output/SHA256 without importing this
// package's test code or reimplementing mux-json/1 itself.
func TestGoldenFixtureFiles_MatchLiveCodec(t *testing.T) {
	const dir = "testdata/golden"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s has no fixture files", dir)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var fx goldenFixtureFile
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("Unmarshal fixture: %v", err)
			}
			if fx.Name == "" || fx.Input == "" || fx.Output == "" {
				t.Fatalf("fixture missing required fields: %+v", fx)
			}

			switch fx.Kind {
			case "payload":
				got, err := EncodePayload(json.RawMessage(fx.Input))
				if err != nil {
					t.Fatalf("EncodePayload: %v", err)
				}
				if string(got) != fx.Output {
					t.Fatalf("canonical bytes drifted from persisted fixture:\n got:  %s\n want: %s", got, fx.Output)
				}
			case "record":
				rec, err := DecodeRecord([]byte(fx.Input))
				if err != nil {
					t.Fatalf("DecodeRecord: %v", err)
				}
				got, err := EncodeRecord(rec)
				if err != nil {
					t.Fatalf("EncodeRecord: %v", err)
				}
				if string(got) != fx.Output {
					t.Fatalf("canonical record bytes drifted from persisted fixture:\n got:  %s\n want: %s", got, fx.Output)
				}
				if fx.SHA256 == "" {
					t.Fatal("record fixture is missing sha256")
				}
				hash, err := RecordSHA256(rec)
				if err != nil {
					t.Fatalf("RecordSHA256: %v", err)
				}
				if hash != fx.SHA256 {
					t.Fatalf("sha256 drifted from persisted fixture: got %s want %s", hash, fx.SHA256)
				}
			default:
				t.Fatalf("fixture %s has unrecognized kind %q", fx.Name, fx.Kind)
			}
		})
	}
}
