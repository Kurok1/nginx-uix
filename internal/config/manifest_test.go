/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestV1DurableConfigModelFingerprint(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		PolicyVersion: NewPolicy().Version(),
		Entries: []Entry{
			{
				Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText,
				Mode: 0o640, Size: 5, ContentDigest: digestOf("nginx"),
			},
			{
				Path: "conf.d", Type: EntryDirectory, Class: EntryDirectoryReadOnly,
			},
			{
				Path: "sites-enabled/default.conf", Type: EntrySymlink, Class: EntrySymlinkInternal,
				SafeLinkTarget: "sites-available/default.conf",
			},
			{
				Path: "private/server.key", Type: EntryRegular, Class: EntrySensitiveMaterial,
				Mode: 0o600, Size: 37,
			},
		},
		Dependencies: []Dependency{
			{
				Source: "nginx.conf", Line: 7, Column: 1, DisplayValue: "conf.d",
				Target: "conf.d", Status: DependencyResolved,
			},
			{
				Source: "nginx.conf", Line: 8, Column: 1, DisplayValue: "missing.conf",
				Target: "missing.conf", Status: DependencyMissing,
			},
		},
		EntryCount:   4,
		ManagedBytes: 5,
	}
	manifestPayload, err := manifest.MarshalBinary()
	if err != nil {
		t.Fatalf("Manifest.MarshalBinary() error = %v", err)
	}
	controlPayload, err := marshalControlState(ControlState{
		SchemaVersion: ControlSchemaVersion,
		WorkspaceID:   "11111111111111111111111111111111",
		State:         StateReady,
		Revision:      7,
		UpdatedAt:     time.Date(2026, time.July, 31, 1, 2, 3, 4, time.UTC),
	})
	if err != nil {
		t.Fatalf("marshalControlState() error = %v", err)
	}
	journalPayload, err := marshalJournal(testJournal(t))
	if err != nil {
		t.Fatalf("marshalJournal() error = %v", err)
	}

	fingerprint := sha256.New()
	for _, artifact := range []struct {
		name    string
		payload []byte
	}{
		{name: "manifest", payload: manifestPayload},
		{name: "control", payload: controlPayload},
		{name: "journal", payload: journalPayload},
	} {
		if _, err := fmt.Fprintf(fingerprint, "%s:%d:", artifact.name, len(artifact.payload)); err != nil {
			t.Fatalf("write %s fingerprint prefix: %v", artifact.name, err)
		}
		if _, err := fingerprint.Write(artifact.payload); err != nil {
			t.Fatalf("write %s fingerprint payload: %v", artifact.name, err)
		}
	}
	got := fmt.Sprintf("%x", fingerprint.Sum(nil))
	const want = "44f5c4a3b75b3f1944f78041ab31a1cc0abf33003b4077ec07ddde705455346a"
	if got != want {
		t.Fatalf("v1 durable config model fingerprint = %q, want %q; format changes require a versioned compatibility design", got, want)
	}
}

func TestManifestCanonicalEncodingIgnoresInsertionOrderAndMetadataNoise(t *testing.T) {
	a := testManifest([]Entry{
		{Path: "conf.d/z.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o640, Size: 1, ContentDigest: digestOf("z")},
		{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o644, Size: 1, ContentDigest: digestOf("n")},
	})
	b := testManifest([]Entry{
		{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o644, Size: 1, ContentDigest: digestOf("n")},
		{Path: "conf.d/z.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o640, Size: 1, ContentDigest: digestOf("z")},
	})
	aBytes, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	bBytes, err := b.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aBytes, bBytes) {
		t.Fatal("canonical manifest depends on insertion order")
	}
	if a.Digest() != b.Digest() {
		t.Fatal("manifest digest depends on insertion order")
	}

	// These metadata values deliberately differ but have no representation in Entry.
	metadataA := struct {
		MTime time.Time
		Inode uint64
		UID   uint32
		GID   uint32
		Noise map[string]string
	}{time.Unix(1, 0).In(time.FixedZone("west", -8*60*60)), 1, 1, 1, map[string]string{"a": "b"}}
	metadataB := struct {
		MTime time.Time
		Inode uint64
		UID   uint32
		GID   uint32
		Noise map[string]string
	}{time.Unix(2, 0).In(time.FixedZone("east", 8*60*60)), 2, 2, 2, map[string]string{"b": "a"}}
	if reflect.DeepEqual(metadataA, metadataB) {
		t.Fatal("test metadata must differ")
	}
	cBytes, err := testManifest(a.Entries).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aBytes, cBytes) {
		t.Fatal("canonical manifest includes ambient metadata")
	}
}

func TestManifestCanonicalEncodingSortsPathsByRawUTF8Bytes(t *testing.T) {
	manifest := testManifest([]Entry{
		{Path: "z.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("z")},
		{Path: "é.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("e")},
		{Path: "a.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("a")},
	})
	payload, err := manifest.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(payload, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []RelativePath{"a.conf", "z.conf", "é.conf"}
	for index, entry := range parsed.Entries {
		if entry.Path != want[index] {
			t.Fatalf("entry[%d] = %q, want %q", index, entry.Path, want[index])
		}
	}
}

func TestManifestDigestChangesForStableEntryMetadataAndManagedContent(t *testing.T) {
	base := Entry{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o644, Size: 1, ContentDigest: digestOf("a")}
	tests := []struct {
		name  string
		entry Entry
	}{
		{name: "mode", entry: withEntry(base, func(entry *Entry) { entry.Mode = 0o640 })},
		{name: "size", entry: withEntry(base, func(entry *Entry) { entry.Size = 2 })},
		{name: "content", entry: withEntry(base, func(entry *Entry) { entry.ContentDigest = digestOf("b") })},
	}
	baseDigest := testManifest([]Entry{base}).Digest()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := testManifest([]Entry{test.entry}).Digest(); got == baseDigest {
				t.Fatal("digest did not change")
			}
		})
	}
}

func TestManifestEncodesOnlySafeLinkAndSensitiveMetadata(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		PolicyVersion: NewPolicy().Version(),
		Entries: []Entry{
			{Path: "inside-link", Type: EntrySymlink, Class: EntrySymlinkInternal, SafeLinkTarget: "conf.d/site.conf"},
			{Path: "outside-link", Type: EntrySymlink, Class: EntrySymlinkExternal},
			{Path: "private/server.key", Type: EntryRegular, Class: EntrySensitiveMaterial, Mode: 0o600, Size: 37},
		},
		EntryCount: 3,
	}
	payload, err := manifest.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("/outside/secret")) {
		t.Fatal("manifest leaked an external target")
	}
	parsed, err := ParseManifest(payload, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	inside, ok := parsed.Entry("inside-link")
	if !ok || inside.SafeLinkTarget != "conf.d/site.conf" {
		t.Fatalf("internal link = %#v, %t", inside, ok)
	}
	sensitive, ok := parsed.Entry("private/server.key")
	if !ok || sensitive.ContentDigest != (Digest{}) || sensitive.Size != 37 || sensitive.Mode != 0o600 {
		t.Fatalf("sensitive entry = %#v, %t", sensitive, ok)
	}
}

func TestManifestCanonicalEncodingSortsDependencies(t *testing.T) {
	dependencies := []Dependency{
		{Source: "nginx.conf", Line: 2, Column: 1, DisplayValue: "z.conf", Target: "z.conf", Status: DependencyResolved},
		{Source: "nginx.conf", Line: 1, Column: 2, DisplayValue: "b.conf", Target: "b.conf", Status: DependencyMissing},
		{Source: "nginx.conf", Line: 1, Column: 1, DisplayValue: "a.conf", Target: "a.conf", Status: DependencyResolved},
	}
	a := testManifest([]Entry{{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("n")}})
	a.Dependencies = dependencies
	b := a
	b.Dependencies = []Dependency{dependencies[2], dependencies[0], dependencies[1]}
	aBytes, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	bBytes, err := b.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aBytes, bBytes) {
		t.Fatal("canonical manifest depends on dependency insertion order")
	}
}

func TestManifestRejectsInvalidAndDuplicateRecords(t *testing.T) {
	validEntry := Entry{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("n")}
	validDependency := Dependency{Source: "nginx.conf", Line: 1, Column: 1, DisplayValue: "site.conf", Target: "site.conf", Status: DependencyMissing}
	tests := []struct {
		name     string
		manifest Manifest
	}{
		{name: "schema", manifest: Manifest{SchemaVersion: ManifestSchemaVersion + 1, PolicyVersion: NewPolicy().Version()}},
		{name: "duplicate path", manifest: Manifest{SchemaVersion: ManifestSchemaVersion, PolicyVersion: NewPolicy().Version(), Entries: []Entry{validEntry, validEntry}, EntryCount: 2, ManagedBytes: 2}},
		{name: "duplicate dependency", manifest: Manifest{SchemaVersion: ManifestSchemaVersion, PolicyVersion: NewPolicy().Version(), Entries: []Entry{validEntry}, Dependencies: []Dependency{validDependency, validDependency}, EntryCount: 1, ManagedBytes: 1}},
		{name: "invalid path", manifest: Manifest{SchemaVersion: ManifestSchemaVersion, PolicyVersion: NewPolicy().Version(), Entries: []Entry{{Path: "../nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("n")}}, EntryCount: 1, ManagedBytes: 1}},
		{name: "invalid enum", manifest: Manifest{SchemaVersion: ManifestSchemaVersion, PolicyVersion: NewPolicy().Version(), Entries: []Entry{{Path: "nginx.conf", Type: EntryType("unknown"), Class: EntryManagedText}}, EntryCount: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.manifest.MarshalBinary(); err == nil {
				t.Fatal("MarshalBinary() error = nil")
			}
		})
	}
}

func TestManifestParserRejectsSchemaLengthAndTrailingBytes(t *testing.T) {
	manifest := testManifest([]Entry{{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("n")}})
	payload, err := manifest.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "schema", payload: withBytes(payload, func(value []byte) { binary.BigEndian.PutUint16(value[4:6], ManifestSchemaVersion+1) })},
		{name: "short record", payload: withBytes(payload, func(value []byte) { binary.BigEndian.PutUint32(value[24:28], uint32(len(value))) })},
		{name: "trailing bytes", payload: append(append([]byte(nil), payload...), 0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseManifest(test.payload, DefaultLimits()); err == nil {
				t.Fatal("ParseManifest() error = nil")
			}
		})
	}
}

func TestManifestValidateEnforcesAllAggregateLimits(t *testing.T) {
	manifest := testManifest([]Entry{{Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText, Mode: 0o600, Size: 1, ContentDigest: digestOf("n")}})
	limits := DefaultLimits()
	limits.MaxEntries = 0
	if err := manifest.Validate(limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Validate(entries) error = %v, want ErrLimitExceeded", err)
	}
	limits = DefaultLimits()
	limits.MaxManagedBytes = 0
	if err := manifest.Validate(limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Validate(bytes) error = %v, want ErrLimitExceeded", err)
	}
}

func TestManifestIntegerConversionsRejectOutOfRangeValues(t *testing.T) {
	if _, err := boundedUint32(-1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("boundedUint32(-1) error = %v, want ErrLimitExceeded", err)
	}
	if _, err := boundedUint32(int64(math.MaxUint32) + 1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("boundedUint32(overflow) error = %v, want ErrLimitExceeded", err)
	}
	if _, err := nonNegativeUint64(-1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("nonNegativeUint64(-1) error = %v, want ErrLimitExceeded", err)
	}
}

func testManifest(entries []Entry) Manifest {
	var managedBytes int64
	for _, entry := range entries {
		if entry.Class == EntryManagedText {
			managedBytes += entry.Size
		}
	}
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		PolicyVersion: NewPolicy().Version(),
		Entries:       entries,
		EntryCount:    len(entries),
		ManagedBytes:  managedBytes,
	}
}

func digestOf(content string) Digest {
	return Digest(sha256.Sum256([]byte(content)))
}

func withEntry(entry Entry, change func(*Entry)) Entry {
	change(&entry)
	return entry
}

func withBytes(payload []byte, change func([]byte)) []byte {
	cloned := append([]byte(nil), payload...)
	change(cloned)
	return cloned
}
